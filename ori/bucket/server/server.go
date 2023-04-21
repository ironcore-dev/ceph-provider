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
	"github.com/onmetal/cephlet/ori/bucket/apiutils"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"

	"github.com/go-logr/logr"
	objectbucketv1alpha1 "github.com/kube-object-storage/lib-bucket-provisioner/pkg/apis/objectbucket.io/v1alpha1"
	bucketv1alpha1 "github.com/onmetal/cephlet/ori/bucket/api/v1alpha1"
	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	"github.com/onmetal/onmetal-api/broker/common/idgen"
	ori "github.com/onmetal/onmetal-api/ori/apis/bucket/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	kubernetes "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(kubernetes.AddToScheme(scheme))
	utilruntime.Must(storagev1alpha1.AddToScheme(scheme))
	utilruntime.Must(rookv1.AddToScheme(scheme))
	utilruntime.Must(objectbucketv1alpha1.AddToScheme(scheme))
}

type BucketClassRegistry interface {
	Get(volumeClassName string) (*ori.BucketClass, bool)
	List() []*ori.BucketClass
}

type Server struct {
	idGen  idgen.IDGen
	client client.Client

	bucketClassSelector client.MatchingLabels

	namespace string

	bucketEndpoint             string
	bucketPoolStorageClassName string
}

func (s *Server) loggerFrom(ctx context.Context, keysWithValues ...interface{}) logr.Logger {
	return ctrl.LoggerFrom(ctx, keysWithValues...)
}

type Options struct {
	IDGen idgen.IDGen

	Namespace                  string
	BucketPoolStorageClassName string
	BucketClassSelector        map[string]string
}

func setOptionsDefaults(o *Options) {
	if o.Namespace == "" {
		o.Namespace = corev1.NamespaceDefault
	}

	if o.IDGen == nil {
		o.IDGen = idgen.Default
	}
}

var _ ori.BucketRuntimeServer = (*Server)(nil)

//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=buckets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=buckets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=buckets/finalizers,verbs=update

//+kubebuilder:rbac:groups=objectbucket.io,resources=objectbucketclaims,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=objectbucket.io,resources=objectbucketclaims/status,verbs=get

//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch

func New(cfg *rest.Config, opts Options) (*Server, error) {
	setOptionsDefaults(&opts)

	c, err := client.New(cfg, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		return nil, fmt.Errorf("error creating client: %w", err)
	}

	return &Server{
		client:                     c,
		idGen:                      opts.IDGen,
		bucketClassSelector:        opts.BucketClassSelector,
		namespace:                  opts.Namespace,
		bucketPoolStorageClassName: opts.BucketPoolStorageClassName,
	}, nil
}

func (s *Server) getManagedAndCreated(ctx context.Context, name string, obj client.Object) error {
	key := client.ObjectKey{Namespace: s.namespace, Name: name}
	if err := s.client.Get(ctx, key, obj); err != nil {
		return err
	}
	if !apiutils.IsManagedBy(obj, bucketv1alpha1.BucketManager) {
		gvk, err := apiutil.GVKForObject(obj, s.client.Scheme())
		if err != nil {
			return err
		}

		return apierrors.NewNotFound(schema.GroupResource{
			Group:    gvk.Group,
			Resource: gvk.Kind, // Yes, kind is good enough here
		}, key.Name)
	}
	return nil
}
