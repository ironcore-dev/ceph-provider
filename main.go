/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	goflag "flag"
	"os"

	"github.com/onmetal/cephlet/controllers"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	flag "github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(storagev1alpha1.AddToScheme(scheme))
	utilruntime.Must(rookv1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string

	var volumePoolName string
	var volumePoolReplication int
	var providerID string
	var volumeClassSelector map[string]string
	var volumePoolLabels map[string]string
	var volumePoolAnnotations map[string]string

	var rookNamespace string
	var enableRBDStats bool
	var rookClusterID string
	var rookMonitorConfigMapDataKey string
	var rookMonitorConfigMapName string
	var rookCSIRBDProvisionerSecretName string
	var rookCSIRBDNodeSecretName string
	var rookStorageClassAllowVolumeExpansion bool
	var rookStorageClassFSType string
	var rookStorageClassImageFeatures string
	var rookStorageClassMountOptions []string
	var rookStorageClassReclaimPolicy string
	var rookStorageClassVolumeBindingMode string
	var rookCSIDriverName string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&volumePoolName, "volume-pool-name", "ceph", "The name of the volume pool.")
	flag.IntVar(&volumePoolReplication, "volume-pool-replication", 3, "The replication factor of the volume pool.")
	flag.StringVar(&providerID, "provider-id", "cephlet://pool", "Provider ID of the pool.")
	flag.StringToStringVar(&volumeClassSelector, "volume-class-selector", nil, "Selector for volume classes to report as available.")
	flag.StringToStringVar(&volumePoolLabels, "volume-pool-labels", nil, "Labels to apply to the volume pool upon startup.")
	flag.StringToStringVar(&volumePoolAnnotations, "volume-pool-annotations", nil, "Annotations to apply to the volume pool upon startup.")

	//Rook
	flag.StringVar(&rookClusterID, "rook-cluster-id", controllers.RookClusterIdDefaultValue, "rook ceph cluster ID")
	flag.StringVar(&rookMonitorConfigMapName, "rook-ceph-mon-cm-name", controllers.RookMonitorConfigMapNameDefaultValue, "ConfigMap name containing actual ceph monitor list")
	flag.StringVar(&rookMonitorConfigMapDataKey, "rook-ceph-mon-cm-data-key", controllers.RookMonitorConfigMapDataKeyDefaultValue, "Ceph monitor ConfigMap key")
	flag.StringVar(&rookNamespace, "rook-namespace", "rook-ceph", "namespace for rook operator and ceph cluster")
	flag.BoolVar(&enableRBDStats, "pool-enable-rbd-stats", controllers.EnableRBDStatsDefaultValue, "Enables collecting RBD per-image IO statistics by enabling dynamic OSD performance counters.")
	flag.StringVar(&rookCSIRBDProvisionerSecretName, "rook-csi-rbd-provisioner-secret-name", controllers.RookCSIRBDProvisionerSecretNameDefaultValue, "Secret name containing Ceph csi rbd provisioner secrets")
	flag.StringVar(&rookCSIRBDNodeSecretName, "rook-csi-rbd-node-secret-name", controllers.RookCSIRBDNodeSecretNameDefaultValue, "Secret name containing Ceph csi rbd node secrets")
	flag.BoolVar(&rookStorageClassAllowVolumeExpansion, "ceph-sc-allow-volume-expansion", controllers.RookStorageClassAllowVolumeExpansionDefaultValue, "Ceph StorageClass: value for 'allowVolumeExpansion' field")
	flag.StringVar(&rookStorageClassFSType, "ceph-sc-fs-type", controllers.RookStorageClassFSTypeDefaultValue, "Ceph StorageClass: value for 'csi.storage.k8s.io/fstype' parameter")
	flag.StringVar(&rookStorageClassImageFeatures, "ceph-sc-image-features", controllers.RookStorageClassImageFeaturesDefaultValue, "Ceph StorageClass: value for 'imageFeatures' parameter")
	flag.StringSliceVar(&rookStorageClassMountOptions, "ceph-sc-mount-options", controllers.RookStorageClassMountOptionsDefaultValue, "Ceph StorageClass: value for 'mountOptions' field, comma-separated values.")
	flag.StringVar(&rookStorageClassReclaimPolicy, "ceph-sc-reclaim-policy", controllers.RookStorageClassReclaimPolicyDefaultValue, "Ceph StorageClass: value for 'reclaimPolicy' field")
	flag.StringVar(&rookStorageClassVolumeBindingMode, "ceph-sc-volume-binding-mode", controllers.RookStorageClassVolumeBindingModeDefaultValue, "Ceph StorageClass: value for 'volumeBindingMode' field")
	flag.StringVar(&rookCSIDriverName, "ceph-csi-driver", controllers.RookCSIDriverNameDefaultValue, "Name of Ceph CSI driver")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(goflag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "926d3280.onmetal.de",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controllers.VolumeReconciler{
		Client:                               mgr.GetClient(),
		Scheme:                               mgr.GetScheme(),
		VolumePoolName:                       volumePoolName,
		RookClusterID:                        rookClusterID,
		RookNamespace:                        rookNamespace,
		RookMonitorEndpointConfigMapName:     rookMonitorConfigMapName,
		RookMonitorEndpointConfigMapDataKey:  rookMonitorConfigMapDataKey,
		RookCSIDriverName:                    rookCSIDriverName,
		RookCSIRBDNodeSecretName:             rookCSIRBDNodeSecretName,
		RookCSIRBDProvisionerSecretName:      rookCSIRBDProvisionerSecretName,
		RookStoragClassImageFeatures:         rookStorageClassImageFeatures,
		RookStorageClassFSType:               rookStorageClassFSType,
		RookStorageClassMountOptions:         rookStorageClassMountOptions,
		RookStorageClassReclaimPolicy:        rookStorageClassReclaimPolicy,
		RookStorageClassAllowVolumeExpansion: rookStorageClassAllowVolumeExpansion,
		RookStorageClassVolumeBindingMode:    rookStorageClassVolumeBindingMode,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Volume")
		os.Exit(1)
	}
	if err = (&controllers.VolumePoolReconciler{
		Client:                mgr.GetClient(),
		Scheme:                mgr.GetScheme(),
		VolumePoolName:        volumePoolName,
		VolumePoolProviderID:  providerID,
		VolumePoolLabels:      volumePoolLabels,
		VolumePoolAnnotations: volumePoolAnnotations,
		VolumeClassSelector:   volumeClassSelector,
		VolumePoolReplication: volumePoolReplication,
		RookNamespace:         rookNamespace,
		EnableRBDStats:        enableRBDStats,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VolumePool")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
