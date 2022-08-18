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

package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	populator_machinery "github.com/kubernetes-csi/lib-volume-populator/populator-machinery"
	"github.com/onmetal/cephlet/pkg/image"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	"github.com/onmetal/onmetal-image/oci/remote"
	"github.com/onmetal/onmetal-image/oci/store"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	prefix     = "cephlet.onmetal.de"
	devicePath = "/dev/block"
)

func main() {
	var (
		mode         string
		image        string
		storePath    string
		httpEndpoint string
		metricsPath  string
		masterURL    string
		kubeconfig   string
		imageName    string
		namespace    string
	)
	// Main arg
	flag.StringVar(&mode, "mode", "", "Mode to run in (controller, populate)")
	// Populate args
	flag.StringVar(&image, "iamge", "", "Image location which the PVC should be populated with")
	flag.StringVar(&storePath, "storePath", "/tmp", "Location of the local image store")
	// Controller args
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&imageName, "image-name", "", "Image to use for populating")
	// Metrics args
	flag.StringVar(&httpEndpoint, "http-endpoint", "", "The TCP network address where the HTTP server for diagnostics, including metrics and leader election health check, will listen (example: `:8080`). The default is empty string, which means the server is disabled.")
	flag.StringVar(&metricsPath, "metrics-path", "/metrics", "The HTTP path where prometheus metrics will be exposed. Default is `/metrics`.")
	// Other args
	flag.StringVar(&namespace, "namespace", "hello", "Namespace to deploy controller")
	flag.Parse()

	switch mode {
	case "controller":
		const (
			groupName  = "storage.api.onmetal.de"
			apiVersion = "v1alpha1"
			kind       = "Volume"
			resource   = "volumes"
		)
		var (
			gk  = schema.GroupKind{Group: groupName, Kind: kind}
			gvr = schema.GroupVersionResource{Group: groupName, Version: apiVersion, Resource: resource}
		)
		populator_machinery.RunController(masterURL, kubeconfig, imageName, httpEndpoint, metricsPath,
			namespace, prefix, gk, gvr, "", devicePath, getPopulatorPodArgs)
	case "populate":
		populate(storePath, image, devicePath)
	default:
		klog.Fatalf("Invalid mode: %s", mode)
	}
}

func populate(storePath string, ref string, devicePath string) {
	ctx := ctrl.SetupSignalHandler()
	log := ctrl.LoggerFrom(ctx)

	reg, err := remote.DockerRegistry(nil)
	if err != nil {
		log.Error(err, "Failed to initialize registry")
		os.Exit(1)
	}
	s, err := store.New(storePath)
	if err != nil {
		log.Error(err, "Failed to initialize image store")
		os.Exit(1)
	}
	img, err := image.Pull(ctx, reg, s, ref)
	if err != nil {
		log.Error(err, "Failed to pull image", "Image", ref)
		os.Exit(1)
	}

	src, err := os.Open(img.RootFS.Path)
	if err != nil {
		log.Error(err, "Failed to open rootfs file", "RootFS", img.RootFS.Path)
		os.Exit(1)
	}
	defer src.Close()

	dst, err := os.OpenFile(devicePath, os.O_RDWR, 0755)
	if err != nil {
		log.Error(err, "Failed to open block device", "Device", devicePath)
		os.Exit(1)
	}
	defer dst.Close()

	// TODO: stream oci image to block device (support both modes: copy and stream)
	_, err = io.Copy(dst, src)
	if err != nil {
		log.Error(err, "Failed to copy rootfs to device", "RootFS", img.RootFS.Path, "Device", devicePath)
		os.Exit(1)
	}
	log.Info("Successfully populated device", "Device", devicePath)
}

func getPopulatorPodArgs(rawBlock bool, u *unstructured.Unstructured) ([]string, error) {
	var volume storagev1alpha1.Volume
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.UnstructuredContent(), &volume); err != nil {
		return nil, fmt.Errorf("failed to convert volume: %w", err)
	}
	if !rawBlock {
		return nil, fmt.Errorf("only raw block device population is supported: %s", volume.Name)
	}
	args := []string{"--mode=populate"}
	args = append(args, "--image="+volume.Spec.Image)
	return args, nil
}
