// Copyright 2023 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package provisioner

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ceph/go-ceph/rados"
	librbd "github.com/ceph/go-ceph/rbd"
	"github.com/go-logr/logr"
	"github.com/onmetal/cephlet/ori/volume/server"
	"github.com/onmetal/cephlet/pkg/limits"
	"github.com/onmetal/cephlet/pkg/populate"
	"github.com/onmetal/cephlet/pkg/round"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/util/json"
)

func New(log logr.Logger, auth *Credentials, config *CephConfig) *Provisioner {
	config.Defaults()
	return &Provisioner{
		log:  log,
		auth: auth,
		cfg:  config,

		connLock: sync.Mutex{},
	}
}

type Provisioner struct {
	log  logr.Logger
	auth *Credentials
	cfg  *CephConfig

	conn     *rados.Conn
	connLock sync.Mutex
}

func (p *Provisioner) Monitors() string {
	return p.auth.Monitors
}

func (p *Provisioner) setOmapValue(ctx context.Context, omapName, key, value string) error {
	if err := p.reconnect(ctx); err != nil {
		return fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	if err := ioCtx.SetOmap(omapName, map[string][]byte{
		key: []byte(value),
	}); err != nil {
		return fmt.Errorf("unable to set omap values: %w", err)
	}

	return nil
}

func (p *Provisioner) getOmapValue(ctx context.Context, omapName, key string) (string, bool, error) {
	data, err := p.getAllOmapValues(ctx, omapName)
	if err != nil {
		return "", false, fmt.Errorf("unable to get omap: %w", err)
	}

	if data == nil {
		return "", false, nil
	}

	value, found := data[key]
	if !found {
		return "", false, nil
	}

	return value, true, nil
}
func (p *Provisioner) deleteOmapValue(ctx context.Context, omapName, key string) error {
	if err := p.reconnect(ctx); err != nil {
		return fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	if err := ioCtx.RmOmapKeys(omapName, []string{key}); err != nil {
		return fmt.Errorf("unable to delete mapping omap value: %w", err)
	}

	return nil
}

func (p *Provisioner) getAllOmapValues(ctx context.Context, omapName string) (map[string]string, error) {
	if err := p.reconnect(ctx); err != nil {
		return nil, fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return nil, fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	omap, err := ioCtx.GetAllOmapValues(omapName, "", "", 10)
	if err != nil {
		if errors.Is(err, rados.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("unable to get omap: %w", err)
	}

	result := map[string]string{}
	for key, value := range omap {
		result[key] = string(value)
	}

	return result, nil
}

func (p *Provisioner) CreateOsImage(ctx context.Context, imageName, imageId string) error {
	log := p.log.WithValues("osImageName", imageName)

	if err := p.reconnect(ctx); err != nil {
		return fmt.Errorf("unable to reconnect: %w", err)
	}
	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	if err := p.setOmapValue(ctx, OmapNameOsImages, imageName, imageId); err != nil {
		return fmt.Errorf("failed to put os image mapping: %w", err)
	}

	onmetalImage, err := populate.ResolveImage(ctx, log, imageName)
	if err != nil {
		return fmt.Errorf("failed to resolve os image: %w", err)
	}

	size := uint64(onmetalImage.RootFS.Descriptor().Size)

	options := librbd.NewRbdImageOptions()
	defer options.Destroy()

	//TODO: different pool for OS images?
	if err := options.SetString(librbd.RbdImageOptionDataPool, p.cfg.Pool); err != nil {
		return fmt.Errorf("failed to set data pool: %w", err)
	}
	log.V(2).Info("Configured pool", "pool", p.cfg.Pool)

	if err = librbd.CreateImage(ioCtx, imageId, size, options); err != nil {
		return fmt.Errorf("failed to create os rbd image: %w", err)
	}
	log.V(2).Info("Created image", "bytes", size)

	img, err := librbd.OpenImage(ioCtx, imageId, librbd.NoSnapshot)
	if err != nil {
		return fmt.Errorf("failed to open rbd image: %w", err)
	}
	defer img.Close()

	if err := populate.Image(ctx, log, onmetalImage, img, p.cfg.PopulatorBufferSize); err != nil {
		return fmt.Errorf("failed to populate os image: %w", err)
	}
	log.V(2).Info("Populated os image on rbd image", "os image", imageName)

	imgSnap, err := img.CreateSnapshot(p.cfg.OsImageSnapshotVersion)
	if err != nil {
		return fmt.Errorf("unable to create snapshot: %w", err)
	}

	if err := imgSnap.Protect(); err != nil {
		return fmt.Errorf("unable to protect snapshot: %w", err)
	}

	return nil
}

func (p *Provisioner) GetOsImage(ctx context.Context, imageName string) (string, bool, error) {
	log := p.log.WithValues("osImageName", imageName)

	if err := p.reconnect(ctx); err != nil {
		return "", false, fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return "", false, fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	imageId, found, err := p.getOmapValue(ctx, OmapNameOsImages, imageName)
	if err != nil {
		return "", false, fmt.Errorf("unable to os image mapping: %w", err)
	}

	if !found {
		return "", false, nil
	}

	img, err := librbd.OpenImageReadOnly(ioCtx, imageId, p.cfg.OsImageSnapshotVersion)
	if err != nil {
		return "", false, IgnoreNotFoundError(fmt.Errorf("failed to open rbd image: %w", err))
	}
	defer img.Close()

	log.Info("Found os image", "name", imageName, "snapshot", p.cfg.OsImageSnapshotVersion)

	return imageId, true, nil
}

func (p *Provisioner) DeleteOsImage(ctx context.Context, imageName, imageId string) error {
	log := p.log.WithValues("imageName", imageName)

	if err := p.reconnect(ctx); err != nil {
		return fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	if err := p.deleteOmapValue(ctx, OmapNameOsImages, imageName); IgnoreNotFoundError(err) != nil {
		return fmt.Errorf("failed to delete os image mapping: %w", err)
	}
	log.V(2).Info("Deleted os image mapping", "imageName", imageName, "imageId", imageId)

	img, err := librbd.OpenImage(ioCtx, imageId, librbd.NoSnapshot)
	if err != nil {
		if IgnoreNotFoundError(err) == nil {
			log.V(2).Info("Os image not found: done")
			return nil
		}
	}
	defer img.Close()

	pools, imgs, err := img.ListChildren()
	if err != nil {
		return fmt.Errorf("unable to list children: %w", err)
	}
	log.V(2).Info("Os image references", "pools", len(pools), "images", len(imgs))

	if len(pools) != 0 || len(imgs) != 0 {
		return fmt.Errorf("unable to delete os image: still in use")
	}

	snaps, err := img.GetSnapshotNames()
	if err != nil {
		return fmt.Errorf("unable to list snapshots: %w", err)
	}
	log.V(2).Info("Found snapshots", "count", len(snaps))

	for _, snapInfo := range snaps {
		log.V(2).Info("Start to delete snapshot", "name", snapInfo.Name)

		snap := img.GetSnapshot(snapInfo.Name)
		isProtected, err := snap.IsProtected()
		if err != nil {
			return fmt.Errorf("unable to chek if image is protected: %w", err)
		}

		if isProtected {
			if err := snap.Unprotect(); err != nil {
				return fmt.Errorf("unable to unprotect os image: %w", err)
			}
		}

		if err := snap.Remove(); err != nil {
			return fmt.Errorf("unable to remove os image snapshot: %w", err)
		}
	}

	if err := img.Close(); err != nil {
		return fmt.Errorf("unable to close os image: %w", err)
	}

	log.V(2).Info("Remove os image")
	if err := img.Remove(); err != nil {
		return fmt.Errorf("unable to remove os image: %w", err)
	}

	log.V(2).Info("Image deleted")

	return nil
}

func (p *Provisioner) CreateCephImage(ctx context.Context, imageId string, volume *ori.Volume, class *ori.VolumeClass, osImageId string) (*server.CephImage, error) {
	log := p.log.WithValues("imageId", imageId)

	if err := p.reconnect(ctx); err != nil {
		return nil, fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return nil, fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	wwn, err := generateWWN()
	if err != nil {
		return nil, fmt.Errorf("unable to generate wwn: %w", err)
	}

	annotations, err := json.Marshal(volume.Metadata.Annotations)
	if err != nil {
		return nil, fmt.Errorf("unable to marshal annotations: %w", err)
	}

	labels, err := json.Marshal(volume.Metadata.Labels)
	if err != nil {
		return nil, fmt.Errorf("unable to marshal labels: %w", err)
	}

	imageSize := round.OffBytes(volume.Spec.Resources.StorageBytes)

	attributes := map[string][]byte{
		OmapImageAnnotationsKey:    annotations,
		OmapImageLabelsKey:         labels,
		OmapImageWwnKey:            []byte(wwn),
		OmapImageClassKey:          []byte(volume.Spec.Class),
		OmapImagePopulatedImageKey: []byte(volume.Spec.Image),
		OmapImageGenerationKey:     []byte(strconv.FormatInt(0, 10)),
	}

	if err := ioCtx.SetOmap(p.cfg.OmapVolumeAttributesKey(imageId), attributes); err != nil {
		return nil, fmt.Errorf("unable to set omap values: %w", err)
	}
	log.V(2).Info("Set image attributes", "omap", p.cfg.OmapVolumeAttributesKey(imageId))

	if err := p.setOmapValue(ctx, OmapNameVolumes, imageId, fmt.Sprintf("%d", ori.VolumeState_VOLUME_PENDING)); err != nil {
		return nil, fmt.Errorf("failed to update state: %w", err)
	}

	options := librbd.NewRbdImageOptions()
	defer options.Destroy()
	if err := options.SetString(librbd.RbdImageOptionDataPool, p.cfg.Pool); err != nil {
		return nil, fmt.Errorf("failed to set data pool: %w", err)
	}
	log.V(2).Info("Configured pool", "pool", p.cfg.Pool)

	if volume.Spec.Image != "" {
		ioCtx2, err := p.conn.OpenIOContext(p.cfg.Pool)
		if err != nil {
			return nil, fmt.Errorf("unable to get io context: %w", err)
		}
		defer ioCtx2.Destroy()

		if err = librbd.CloneImage(ioCtx2, osImageId, p.cfg.OsImageSnapshotVersion, ioCtx, imageId, options); err != nil {
			return nil, fmt.Errorf("failed to clone rbd image: %w", err)
		}

		log.V(2).Info("Cloned image")
	} else {
		if err = librbd.CreateImage(ioCtx, imageId, imageSize, options); err != nil {
			return nil, fmt.Errorf("failed to create rbd image: %w", err)
		}
		log.V(2).Info("Created image", "bytes", imageSize)
	}

	img, err := librbd.OpenImage(ioCtx, imageId, librbd.NoSnapshot)
	if err != nil {
		return nil, fmt.Errorf("failed to open rbd image: %w", err)
	}
	defer img.Close()

	if volume.Spec.Image != "" {
		if err := img.Resize(imageSize); err != nil {
			return nil, fmt.Errorf("failed to resize rbd image: %w", err)
		}
		log.V(2).Info("Resized cloned image", "bytes", imageSize)
	}

	created, err := img.GetCreateTimestamp()
	if err != nil {
		return nil, fmt.Errorf("failed to get rbd image created timestamp: %w", err)
	}

	cephImage := &server.CephImage{
		Id:                 imageId,
		Annotations:        volume.Metadata.Annotations,
		Labels:             volume.Metadata.Labels,
		CreatedAt:          time.Unix(created.Sec, created.Nsec),
		Wwn:                wwn,
		Pool:               p.cfg.Pool,
		Size:               imageSize,
		PopulatedImageName: volume.Spec.Image,
		Class:              volume.Spec.Class,
		State:              ori.VolumeState_VOLUME_AVAILABLE,
	}

	if p.cfg.LimitingEnabled {
		calculatedLimits := limits.Calculate(class.Capabilities.Iops, class.Capabilities.Tps, p.cfg.BurstFactor, p.cfg.BurstDurationInSeconds)
		for limit, value := range calculatedLimits.String() {
			if err := img.SetMetadata(fmt.Sprintf("%s%s", p.cfg.LimitMetadataPrefix, limit), value); err != nil {
				return nil, fmt.Errorf("failed to set limit (%s): %w", limit, err)
			}
			log.V(3).Info("Set image limit", "limit", limit, "value", value)
		}
		log.V(2).Info("Successfully configured all limits")
	}

	if err := p.setOmapValue(ctx, OmapNameVolumes, imageId, fmt.Sprintf("%d", ori.VolumeState_VOLUME_AVAILABLE)); err != nil {
		return nil, fmt.Errorf("failed to update state: %w", err)
	}

	return cephImage, nil
}

func (p *Provisioner) GetCephImage(ctx context.Context, imageId string) (*server.CephImage, error) {
	log := p.log.WithValues("imageId", imageId)

	if err := p.reconnect(ctx); err != nil {
		return nil, fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return nil, fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	state, found, err := p.getOmapValue(ctx, OmapNameVolumes, imageId)
	if err != nil {
		return nil, fmt.Errorf("unable to get omap for image: %w", err)
	}

	imageState, err := strconv.Atoi(state)
	if err != nil {
		return nil, fmt.Errorf("unable parse image state: %w", err)
	}

	if !found {
		return nil, fmt.Errorf("image not found")
	}

	img, err := librbd.OpenImageReadOnly(ioCtx, imageId, librbd.NoSnapshot)
	if err != nil {
		return nil, IgnoreNotFoundError(fmt.Errorf("failed to open rbd image: %w", err))
	}
	defer img.Close()

	imageSize, err := img.GetSize()
	if err != nil {
		return nil, fmt.Errorf("unable parse size: %w", err)
	}
	log.V(3).Info("Found image size", "imageSize", imageSize)

	created, err := img.GetCreateTimestamp()
	if err != nil {
		return nil, fmt.Errorf("failed to get rbd image created timestamp: %w", err)
	}
	log.V(3).Info("Fetched created timestamp")

	attributes, err := ioCtx.GetAllOmapValues(p.cfg.OmapVolumeAttributesKey(imageId), "", "", 10)
	if err != nil {
		return nil, fmt.Errorf("unable to get attribute omap: %w", err)
	}
	log.V(3).Info("Fetched attributes")

	if attributes == nil {
		return nil, fmt.Errorf("image attributes map empty: %w", err)
	}

	imageWwn, ok := attributes[OmapImageWwnKey]
	if !ok {
		return nil, fmt.Errorf("unable to get omap attribute: %s", OmapImageWwnKey)
	}
	log.V(3).Info("Found image wwn", "imageWwn", imageWwn)

	imageClass, ok := attributes[OmapImageClassKey]
	if !ok {
		return nil, fmt.Errorf("unable to get omap attribute: %s", OmapImageClassKey)
	}
	log.V(3).Info("Found image class", "imageClass", imageClass)

	imageGeneration, ok := attributes[OmapImageGenerationKey]
	if !ok {
		return nil, fmt.Errorf("unable to get omap attribute: %s", OmapImageGenerationKey)
	}

	generation, err := strconv.ParseInt(string(imageGeneration), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("unable parse size: %w", err)
	}
	log.V(3).Info("Found image generation", "imageGeneration", generation)

	populatedImage := attributes[OmapImagePopulatedImageKey]
	log.V(3).Info("Found populated image", "populatedImage", populatedImage)

	imageLabels, ok := attributes[OmapImageLabelsKey]
	if !ok {
		return nil, fmt.Errorf("unable to get omap attribute: %s", OmapImageLabelsKey)
	}
	log.V(3).Info("Found image labels")

	labels := map[string]string{}
	if err := json.Unmarshal(imageLabels, &labels); err != nil {
		return nil, fmt.Errorf("unable to unmarshal image labels: %w", err)
	}

	imageAnnotations, ok := attributes[OmapImageAnnotationsKey]
	if !ok {
		return nil, fmt.Errorf("unable to get omap attribute: %s", OmapImageAnnotationsKey)
	}
	log.V(3).Info("Found image annotations")

	annotations := map[string]string{}
	if err := json.Unmarshal(imageAnnotations, &annotations); err != nil {
		return nil, fmt.Errorf("unable to unmarshal image annotations: %w", err)
	}

	return &server.CephImage{
		Id:                 imageId,
		Annotations:        annotations,
		Labels:             labels,
		Generation:         generation,
		CreatedAt:          time.Unix(created.Sec, created.Nsec),
		Wwn:                string(imageWwn),
		Pool:               p.cfg.Pool,
		Size:               imageSize,
		PopulatedImageName: string(populatedImage),
		Class:              string(imageClass),
		State:              ori.VolumeState(imageState),
	}, nil
}

func (p *Provisioner) ListCephImages(ctx context.Context) ([]*server.CephImage, error) {
	if err := p.reconnect(ctx); err != nil {
		return nil, fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return nil, fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	images, err := p.getAllOmapValues(ctx, OmapNameVolumes)
	if err != nil {
		return nil, fmt.Errorf("unable to get all images: %w", err)
	}

	var cephImages []*server.CephImage
	for imageId := range images {
		cephImage, err := p.GetCephImage(ctx, imageId)
		if err != nil {
			return nil, fmt.Errorf("failed to get image: %w", err)
		}

		cephImages = append(cephImages, cephImage)
	}

	return cephImages, nil
}

func (p *Provisioner) DeleteCephImage(ctx context.Context, imageId string) error {
	log := p.log.WithValues("imageId", imageId)

	if err := p.reconnect(ctx); err != nil {
		return fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	if err := ioCtx.Delete(p.cfg.OmapVolumeAttributesKey(imageId)); IgnoreNotFoundError(err) != nil {
		return err
	}
	log.V(2).Info("Image attributes deleted", "name", p.cfg.OmapVolumeAttributesKey(imageId))

	if err := p.deleteOmapValue(ctx, OmapNameVolumes, imageId); IgnoreNotFoundError(err) != nil {
		return fmt.Errorf("failed to delete image mapping: %w", err)
	}

	if err := librbd.RemoveImage(ioCtx, imageId); IgnoreNotFoundError(err) != nil {
		return err
	}
	log.V(2).Info("Image deleted")

	return nil
}

type fetchAuthResponse struct {
	Key string `json:"key"`
}

func (p *Provisioner) FetchAuth(ctx context.Context) (string, string, error) {
	if err := p.reconnect(ctx); err != nil {
		return "", "", fmt.Errorf("unable to reconnect: %w", err)
	}

	cmd1, err := json.Marshal(map[string]string{
		"prefix": "auth get-key",
		"entity": p.cfg.Client,
		"format": "json",
	})
	if err != nil {
		return "", "", fmt.Errorf("unable to marshal command: %w", err)
	}

	p.log.V(3).Info("Try to fetch client", "name", p.cfg.Client)
	data, _, err := p.conn.MonCommand(cmd1)
	if err != nil {
		return "", "", fmt.Errorf("failed to execute mon command: %w", err)
	}

	response := fetchAuthResponse{}
	if err := json.Unmarshal(data, &response); err != nil {
		return "", "", fmt.Errorf("unable to unmarshal response: %w", err)
	}

	return strings.TrimPrefix(p.cfg.Client, "client."), response.Key, nil
}

func (p *Provisioner) reconnect(ctx context.Context) error {
	p.connLock.Lock()
	defer p.connLock.Unlock()

	if p.conn == nil {
		return p.connect(ctx)
	}

	_, err := p.conn.ListPools()
	if err != nil {
		return p.connect(ctx)
	}

	return nil
}

func (p *Provisioner) connect(ctx context.Context) error {
	conn, err := p.auth.Connect(ctx)
	if err != nil {
		return err
	}
	p.conn = conn

	return nil
}
