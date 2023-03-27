// Copyright 2022 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/go-logr/logr"
	"github.com/onmetal/onmetal-api/broker/common/cleaner"
	"github.com/onmetal/onmetal-api/broker/common/idgen"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	"golang.org/x/sync/singleflight"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/yaml"
	ctrl "sigs.k8s.io/controller-runtime"
)

type Provisioner interface {
	CreateOsImage(ctx context.Context, imageName, imageId string) error
	GetOsImage(ctx context.Context, imageName string) (string, bool, error)
	DeleteOsImage(ctx context.Context, imageName, imageId string) error

	CreateCephImage(ctx context.Context, imageId string, volume *ori.Volume, class *ori.VolumeClass, osImageId string) (*CephImage, error)
	GetCephImage(ctx context.Context, imageId string) (*CephImage, error)
	ListCephImages(ctx context.Context) ([]*CephImage, error)
	DeleteCephImage(ctx context.Context, imageId string) error

	FetchAuth(ctx context.Context) (string, string, error)

	Monitors() string
}

type Server struct {
	idGen       idgen.IDGen
	provisioner Provisioner

	inProgress     sets.Set[string]
	inProgressLock sync.Mutex

	VolumeNameLabelName    string
	AvailableVolumeClasses map[string]ori.VolumeClass

	syncPopulation singleflight.Group
}

func (s *Server) lock(volumeName string) error {
	s.inProgressLock.Lock()
	defer s.inProgressLock.Unlock()

	if s.inProgress.Has(volumeName) {
		return fmt.Errorf("failed to acquire lock: %s already in use", volumeName)
	}

	s.inProgress.Insert(volumeName)

	return nil
}

func (s *Server) release(volumeName string) {
	s.inProgressLock.Lock()
	defer s.inProgressLock.Unlock()

	s.inProgress.Delete(volumeName)
}

func (s *Server) loggerFrom(ctx context.Context, keysWithValues ...interface{}) logr.Logger {
	return ctrl.LoggerFrom(ctx, keysWithValues...)
}

func setupCleaner(ctx context.Context, log logr.Logger, retErr *error) (c *cleaner.Cleaner, cleanup func()) {
	c = cleaner.New()
	cleanup = func() {
		if *retErr != nil {
			select {
			case <-ctx.Done():
				log.Info("Cannot do cleanup since context expired")
				return
			default:
				if err := c.Cleanup(ctx); err != nil {
					log.Error(err, "Error cleaning up")
				}
			}
		}
	}
	return c, cleanup
}

type Options struct {
	IDGen idgen.IDGen

	VolumeNameLabelName        string
	PathAvailableVolumeClasses string
}

func setOptionsDefaults(o *Options) {
	if o.IDGen == nil {
		o.IDGen = idgen.Default
	}
}

var _ ori.VolumeRuntimeServer = (*Server)(nil)

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumes,verbs=get;list;watch;create;update;patch;delete

func New(opts Options, provisioner Provisioner) (*Server, error) {
	classes, err := ReadVolumeClasses(opts.PathAvailableVolumeClasses)
	if err != nil {
		return nil, fmt.Errorf("unable to get volume classes: %w", err)
	}

	setOptionsDefaults(&opts)

	return &Server{
		idGen:                  opts.IDGen,
		VolumeNameLabelName:    opts.VolumeNameLabelName,
		AvailableVolumeClasses: classes,
		provisioner:            provisioner,
		inProgress:             map[string]sets.Empty{},
		inProgressLock:         sync.Mutex{},
	}, nil
}

func ReadVolumeClasses(path string) (map[string]ori.VolumeClass, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("unable to open volume class file (%s): %w", path, err)
	}

	classList := []ori.VolumeClass{}
	if err := yaml.NewYAMLOrJSONDecoder(file, 100).Decode(&classList); err != nil {
		return nil, fmt.Errorf("unable to unmarshal volume class json file (%s): %w", path, err)
	}

	classes := map[string]ori.VolumeClass{}
	for _, class := range classList {
		if _, ok := classes[class.Name]; ok {
			return nil, fmt.Errorf("multiple classes with same name (%s) found", class.Name)
		}
		classes[class.Name] = class
	}

	return classes, nil
}
