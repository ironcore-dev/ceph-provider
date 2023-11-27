// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	objectbucketv1alpha1 "github.com/kube-object-storage/lib-bucket-provisioner/pkg/apis/objectbucket.io/v1alpha1"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
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

	"github.com/ironcore-dev/ceph-provider/iri/bucket/apiutils"
	"github.com/ironcore-dev/ironcore/broker/common/idgen"
	iriv1alpha1 "github.com/ironcore-dev/ironcore/iri/apis/bucket/v1alpha1"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(kubernetes.AddToScheme(scheme))
	utilruntime.Must(rookv1.AddToScheme(scheme))
	utilruntime.Must(objectbucketv1alpha1.AddToScheme(scheme))
}

type BucketClassRegistry interface {
	Get(bucketClassName string) (*iriv1alpha1.BucketClass, bool)
	List() []*iriv1alpha1.BucketClass
}

type Server struct {
	idGen  idgen.IDGen
	client client.Client

	bucketClassess      BucketClassRegistry
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
	BucketEndpoint             string
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

var _ iriv1alpha1.BucketRuntimeServer = (*Server)(nil)

//+kubebuilder:rbac:groups=objectbucket.io,resources=objectbucketclaims,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=objectbucket.io,resources=objectbucketclaims/status,verbs=get

//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch

func New(cfg *rest.Config, bucketClassRegistry BucketClassRegistry, opts Options) (*Server, error) {
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
		bucketClassess:             bucketClassRegistry,
		bucketClassSelector:        opts.BucketClassSelector,
		namespace:                  opts.Namespace,
		bucketPoolStorageClassName: opts.BucketPoolStorageClassName,
		bucketEndpoint:             opts.BucketEndpoint,
	}, nil
}

func (s *Server) getManagedAndCreated(ctx context.Context, name string, obj client.Object) error {
	key := client.ObjectKey{Namespace: s.namespace, Name: name}
	if err := s.client.Get(ctx, key, obj); err != nil {
		return err
	}
	if !apiutils.IsManagedBy(obj, apiutils.BucketManager) {
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
