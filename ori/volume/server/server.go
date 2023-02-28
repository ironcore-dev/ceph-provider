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

	"github.com/go-logr/logr"
	"github.com/onmetal/onmetal-api/broker/common/idgen"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
)

type Server struct {
	idGen idgen.IDGen
}

func (s *Server) loggerFrom(ctx context.Context, keysWithValues ...interface{}) logr.Logger {
	return ctrl.LoggerFrom(ctx, keysWithValues...)
}

//func (s *Server) setupCleaner(ctx context.Context, log logr.Logger, retErr *error) (c *cleaner.Cleaner, cleanup func()) {
//	c = cleaner.New()
//	cleanup = func() {
//		if *retErr != nil {
//			select {
//			case <-ctx.Done():
//				log.Info("Cannot do cleanup since context expired")
//				return
//			default:
//				if err := c.Cleanup(ctx); err != nil {
//					log.Error(err, "Error cleaning up")
//				}
//			}
//		}
//	}
//	return c, cleanup
//}

type Options struct {
	IDGen idgen.IDGen
}

func setOptionsDefaults(o *Options) {
	if o.IDGen == nil {
		o.IDGen = idgen.Default
	}
}

var _ ori.VolumeRuntimeServer = (*Server)(nil)

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumes,verbs=get;list;watch;create;update;patch;delete

func New(opts Options) (*Server, error) {
	setOptionsDefaults(&opts)

	return &Server{
		idGen: opts.IDGen,
	}, nil
}
