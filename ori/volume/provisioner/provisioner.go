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
	"sync"
	"time"

	"github.com/ceph/go-ceph/rados"
	librbd "github.com/ceph/go-ceph/rbd"
	"github.com/go-logr/logr"
	"github.com/onmetal/cephlet/ori/volume/server"
	"github.com/onmetal/cephlet/pkg/limits"
	"github.com/onmetal/cephlet/pkg/populate"
	"github.com/onmetal/onmetal-image/utils/sets"
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

		inProgress:     map[string]sets.Empty{},
		inProgressLock: sync.Mutex{},
	}
}

type Provisioner struct {
	log  logr.Logger
	auth *Credentials
	cfg  *CephConfig

	conn     *rados.Conn
	connLock sync.Mutex

	inProgress     sets.Set[string]
	inProgressLock sync.Mutex
}

func (p *Provisioner) Lock(volumeName string) error {
	p.inProgressLock.Lock()
	defer p.inProgressLock.Unlock()

	if p.inProgress.Has(volumeName) {
		return fmt.Errorf("failed to acquire lock: %s already in use", volumeName)
	}

	p.inProgress.Insert(volumeName)

	return nil
}

func (p *Provisioner) Monitors() string {
	return p.auth.Monitors
}

func (p *Provisioner) Release(volumeName string) {
	p.inProgressLock.Lock()
	defer p.inProgressLock.Unlock()

	p.inProgress.Delete(volumeName)
}

func (p *Provisioner) GetMapping(ctx context.Context, volumeName string) (string, bool, error) {
	idMap, err := p.GetAllMappings(ctx)
	if err != nil {
		return "", false, fmt.Errorf("unable to get omap: %w", err)
	}

	if idMap == nil {
		return "", false, nil
	}

	id, found := idMap[volumeName]
	if !found {
		return "", false, nil
	}

	return id, true, nil
}

func (p *Provisioner) PutMapping(ctx context.Context, volumeName, imageName string) error {
	if err := p.reconnect(ctx); err != nil {
		return fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	if err := ioCtx.SetOmap(p.cfg.OmapNameMappings, map[string][]byte{
		volumeName: []byte(imageName),
	}); err != nil {
		return fmt.Errorf("unablet to set omap values: %w", err)
	}

	return nil
}

func (p *Provisioner) DeleteMapping(ctx context.Context, volumeName string) error {
	if err := p.reconnect(ctx); err != nil {
		return fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	if err := ioCtx.RmOmapKeys(p.cfg.OmapNameMappings, []string{volumeName}); err != nil {
		return fmt.Errorf("unable to delete mapping omap value: %w", err)
	}

	return nil
}

func (p *Provisioner) GetCephImage(ctx context.Context, imageName string, image *server.Image) error {
	log := p.log.WithValues("imageName", imageName)

	if err := p.reconnect(ctx); err != nil {
		return fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	img, err := librbd.OpenImageReadOnly(ioCtx, imageName, librbd.NoSnapshot)
	if err != nil {
		return fmt.Errorf("failed to open rbd image: %w", err)
	}
	defer img.Close()

	created, err := img.GetCreateTimestamp()
	if err != nil {
		return fmt.Errorf("failed to get rbd image created timestamp: %w", err)
	}
	log.V(2).Info("Fetched created timestamp")

	attributes, err := ioCtx.GetAllOmapValues(p.cfg.OmapVolumeAttributesKey(imageName), "", "", 10)
	if err != nil {
		return fmt.Errorf("unable to get attribute omap: %w", err)
	}
	log.V(2).Info("Fetched attributes")

	imageId, ok := attributes[p.cfg.OmapImageIdKey]
	if !ok {
		return fmt.Errorf("unable to get omap attribute: %s", p.cfg.OmapImageIdKey)
	}
	log.V(2).Info("Found image id", "imageId", imageId)

	imageWwn, ok := attributes[p.cfg.OmapWwnKey]
	if !ok {
		return fmt.Errorf("unable to get omap attribute: %s", p.cfg.OmapWwnKey)
	}
	log.V(2).Info("Found image wwn", "imageWwn", imageWwn)

	imageClass, ok := attributes[p.cfg.OmapClassKey]
	if !ok {
		return fmt.Errorf("unable to get omap attribute: %s", p.cfg.OmapClassKey)
	}
	log.V(2).Info("Found image class", "imageClass", imageClass)

	populatedImage := attributes[p.cfg.OmapPopulatedImageKey]
	log.V(2).Info("Found populated image", "populatedImage", populatedImage)

	image.Name = imageName
	image.Id = string(imageId)
	image.Wwn = string(imageWwn)
	image.Class = string(imageClass)
	image.PopulatedImage = string(populatedImage)
	image.Created = time.Unix(created.Sec, created.Nsec)
	image.Pool = p.cfg.Pool

	return nil
}

func (p *Provisioner) OsImageExists(ctx context.Context, imageName string) (bool, error) {
	log := p.log.WithValues("osImageName", imageName)

	if err := p.reconnect(ctx); err != nil {
		return false, fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return false, fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	log.V(2).Info("Try to open os image", "osImageName", imageName)
	img, err := librbd.OpenImageReadOnly(ioCtx, imageName, p.cfg.OsImageSnapshotVersion)
	if err != nil {
		if errors.Is(err, librbd.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("failed to open rbd image: %w", err)
	}
	defer img.Close()

	log.V(2).Info("Found os image", "osImageName", imageName, "snapshot", p.cfg.OsImageSnapshotVersion)

	return true, nil
}

func (p *Provisioner) CreateOSImage(ctx context.Context, volume *server.AggregateVolume) error {
	log := p.log.WithValues("osImageName", volume.Requested.Image.Name)

	if err := p.reconnect(ctx); err != nil {
		return fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	options := librbd.NewRbdImageOptions()
	defer options.Destroy()

	//TODO: different pool for OS images?
	if err := options.SetString(librbd.RbdImageOptionDataPool, p.cfg.Pool); err != nil {
		return fmt.Errorf("failed to set data pool: %w", err)
	}
	log.V(2).Info("Configured pool", "pool", p.cfg.Pool)

	if err = librbd.CreateImage(ioCtx, volume.Requested.Image.Name, volume.Requested.Image.Bytes, options); err != nil {
		return fmt.Errorf("failed to create rbd image: %w", err)
	}
	log.V(2).Info("Created image", "bytes", volume.Requested.Image.Bytes)

	img, err := librbd.OpenImage(ioCtx, volume.Requested.Image.Name, librbd.NoSnapshot)
	if err != nil {
		return fmt.Errorf("failed to open rbd image: %w", err)
	}
	defer img.Close()

	if err := populate.Image(ctx, log, volume.Requested.Image.Name, img); err != nil {
		return fmt.Errorf("failed to populate os image: %w", err)
	}
	log.V(2).Info("Populated os image on rbd image", "os image", volume.Requested.Image.Name)

	imgSnap, err := img.CreateSnapshot(p.cfg.OsImageSnapshotVersion)
	if err != nil {
		return fmt.Errorf("unable to create snapshot: %w", err)
	}

	if err := imgSnap.Protect(); err != nil {
		return fmt.Errorf("unable to protect snapshot: %w", err)
	}

	volume.Provisioned.PopulatedImage = volume.Requested.Image.Name
	return nil
}

func (p *Provisioner) CreateCephImage(ctx context.Context, volume *server.AggregateVolume) error {
	log := p.log.WithValues("imageName", volume.Provisioned.Name)

	if err := p.reconnect(ctx); err != nil {
		return fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	options := librbd.NewRbdImageOptions()
	defer options.Destroy()

	if err := options.SetString(librbd.RbdImageOptionDataPool, p.cfg.Pool); err != nil {
		return fmt.Errorf("failed to set data pool: %w", err)
	}
	log.V(2).Info("Configured pool", "pool", p.cfg.Pool)

	if volume.Requested.Image != nil {
		ioCtx2, err := p.conn.OpenIOContext(p.cfg.Pool)
		if err != nil {
			return fmt.Errorf("unable to get io context: %w", err)
		}
		defer ioCtx2.Destroy()

		if err = librbd.CloneImage(ioCtx2, volume.Provisioned.PopulatedImage, p.cfg.OsImageSnapshotVersion, ioCtx, volume.Provisioned.Name, options); err != nil {
			return fmt.Errorf("failed to clone rbd image: %w", err)
		}

		log.V(2).Info("Cloned image")
	} else {
		if err = librbd.CreateImage(ioCtx, volume.Provisioned.Name, volume.Provisioned.Bytes, options); err != nil {
			return fmt.Errorf("failed to create rbd image: %w", err)
		}
		log.V(2).Info("Created image", "bytes", volume.Provisioned.Bytes)
	}

	img, err := librbd.OpenImage(ioCtx, volume.Provisioned.Name, librbd.NoSnapshot)
	if err != nil {
		return fmt.Errorf("failed to open rbd image: %w", err)
	}
	defer img.Close()

	if volume.Requested.Image != nil {
		if err := img.Resize(volume.Provisioned.Bytes); err != nil {
			return fmt.Errorf("failed to resize rbd image: %w", err)
		}
		log.V(2).Info("Resized cloned image", "bytes", volume.Provisioned.Bytes)
	}

	imageId, err := img.GetId()
	if err != nil {
		return fmt.Errorf("failed to fetch rbd image id: %w", err)
	}

	created, err := img.GetCreateTimestamp()
	if err != nil {
		return fmt.Errorf("failed to get rbd image created timestamp: %w", err)
	}
	volume.Provisioned.Created = time.Unix(created.Sec, created.Nsec)

	volume.Provisioned.Pool = p.cfg.Pool

	attributes := map[string][]byte{
		p.cfg.OmapImageIdKey:    []byte(imageId),
		p.cfg.OmapImageNameKey:  []byte(volume.Provisioned.Name),
		p.cfg.OmapVolumeNameKey: []byte(volume.Requested.Name),
		p.cfg.OmapWwnKey:        []byte(volume.Provisioned.Wwn),
		p.cfg.OmapClassKey:      []byte(volume.Requested.Class),
	}
	if volume.Requested.Image != nil {
		attributes[p.cfg.OmapPopulatedImageKey] = []byte(volume.Requested.Image.Name)
	}

	if err := ioCtx.SetOmap(p.cfg.OmapVolumeAttributesKey(volume.Provisioned.Name), attributes); err != nil {
		return fmt.Errorf("unablet to set omap values: %w", err)
	}
	log.V(2).Info("Set image attributes", "omap", p.cfg.OmapVolumeAttributesKey(volume.Provisioned.Name))

	calculatedLimits := limits.Calculate(volume.Requested.IOPS, volume.Requested.TPS, p.cfg.BurstFactor, p.cfg.BurstDurationInSeconds)
	for limit, value := range calculatedLimits.String() {
		if err := img.SetMetadata(fmt.Sprintf("%s%s", p.cfg.LimitMetadataPrefix, limit), value); err != nil {
			return fmt.Errorf("failed to set limit (%s): %w", limit, err)
		}
		log.V(3).Info("Set image limit", "limit", limit, "value", value)
	}
	log.V(2).Info("Successfully configured all limits")

	return nil
}

func (p *Provisioner) GetAllMappings(ctx context.Context) (map[string]string, error) {
	if err := p.reconnect(ctx); err != nil {
		return nil, fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return nil, fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	idMap, err := ioCtx.GetAllOmapValues(p.cfg.OmapNameMappings, "", "", 10)
	if err != nil {
		if errors.Is(err, rados.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("unable to get omap: %w", err)
	}

	result := map[string]string{}
	for volumeName, imageName := range idMap {
		result[volumeName] = string(imageName)
	}

	return result, nil
}

type fetchAuthResponse struct {
	Key string `json:"key"`
}

func (p *Provisioner) FetchAuth(ctx context.Context, image *server.Image) (string, string, error) {
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

	data, _, err := p.conn.MonCommand(cmd1)
	if err != nil {
		return "", "", fmt.Errorf("failed to execute mon command: %w", err)
	}

	response := fetchAuthResponse{}
	if err := json.Unmarshal(data, &response); err != nil {
		return "", "", fmt.Errorf("unable to unmarshal response: %w", err)
	}

	return p.cfg.Client, response.Key, nil
}

func (p *Provisioner) DeleteCephImage(ctx context.Context, imageName string) error {
	log := p.log.WithValues("imageName", imageName)

	if err := p.reconnect(ctx); err != nil {
		return fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	if err := ioCtx.Delete(p.cfg.OmapVolumeAttributesKey(imageName)); err != nil {
		if !errors.Is(err, librbd.ErrNotFound) {
			return err
		}
		log.V(2).Info("Image attributes already deleted", "omap", p.cfg.OmapVolumeAttributesKey(imageName))
	}
	log.V(2).Info("Image attributes deleted", "name", p.cfg.OmapVolumeAttributesKey(imageName))

	if err := librbd.RemoveImage(ioCtx, imageName); err != nil {
		if !errors.Is(err, librbd.ErrNotFound) {
			return err
		}
		log.V(2).Info("Image already deleted")
	}
	log.V(2).Info("Image deleted")

	return nil
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
