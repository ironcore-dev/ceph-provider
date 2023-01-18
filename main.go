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

	bucketv1alpha1 "github.com/kube-object-storage/lib-bucket-provisioner/pkg/apis/objectbucket.io/v1alpha1"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	"github.com/onmetal/cephlet/controllers"
	"github.com/onmetal/cephlet/pkg/rook"
	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	flag "github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	//+kubebuilder:scaffold:imports
)

var (
	scheme    = runtime.NewScheme()
	setupLog  = ctrl.Log.WithName("setup")
	poolUsage = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: "cephlet",
			Name:      "volume_pool_usage",
			Help:      "Current pool usage, partitioned by pool and resource.",
		},
		[]string{
			"pool",
			"resource",
		},
	)
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(storagev1alpha1.AddToScheme(scheme))
	utilruntime.Must(rookv1.AddToScheme(scheme))
	utilruntime.Must(snapshotv1.AddToScheme(scheme))
	utilruntime.Must(bucketv1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme

	metrics.Registry.MustRegister(poolUsage)
}

func main() {
	var (
		metricsAddr          string
		enableLeaderElection bool
		probeAddr            string
	)

	var (
		volumePoolName        string
		volumePoolReplication int
		volumePoolProviderID  string
		volumeClassSelector   map[string]string
		volumePoolLabels      map[string]string
		volumePoolAnnotations map[string]string
	)

	var (
		bucketPoolName        string
		bucketPoolReplication int
		bucketPoolProviderID  string
		bucketClassSelector   map[string]string
		bucketPoolLabels      map[string]string
		bucketPoolAnnotations map[string]string

		bucketBaseUrl string
	)

	var (
		populatorImage      string
		populatorDevicePath string
		populatorNamespace  string
		populatorPrefix     string
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	flag.StringVar(&volumePoolName, "volume-pool-name", "ceph", "The name of the volume pool.")
	flag.IntVar(&volumePoolReplication, "volume-pool-replication", 3, "The replication factor of the volume pool.")
	flag.StringVar(&volumePoolProviderID, "volume-pool-provider-id", "cephlet://pool", "Provider ID of the pool.")
	flag.StringToStringVar(&volumeClassSelector, "volume-class-selector", nil, "Selector for volume classes to report as available.")
	flag.StringToStringVar(&volumePoolLabels, "volume-pool-labels", nil, "Labels to apply to the volume pool upon startup.")
	flag.StringToStringVar(&volumePoolAnnotations, "volume-pool-annotations", nil, "Annotations to apply to the volume pool upon startup.")

	flag.StringVar(&bucketPoolName, "bucket-pool-name", "ceph", "The name of the bucket pool.")
	flag.IntVar(&bucketPoolReplication, "bucket-pool-replication", 3, "The replication factor of the bucket pool.")
	flag.StringVar(&bucketPoolProviderID, "bucket-pool-provider-id", "cephlet://pool", "Provider ID of the pool.")
	flag.StringToStringVar(&bucketClassSelector, "bucket-class-selector", nil, "Selector for bucket classes to report as available.")
	flag.StringToStringVar(&bucketPoolLabels, "bucket-pool-labels", nil, "Labels to apply to the bucket pool upon startup.")
	flag.StringToStringVar(&bucketPoolAnnotations, "bucket-pool-annotations", nil, "Annotations to apply to the bucket pool upon startup.")
	flag.StringVar(&bucketBaseUrl, "bucket-base-url", "example.com", "Base url of the buckets.")

	// image populator
	flag.StringVar(&populatorImage, "populator-image", "", "Container image of the populator pod.")
	flag.StringVar(&populatorDevicePath, "populator-device-path", "/dev/block", "Device path presented in the populator pod.")
	flag.StringVar(&populatorNamespace, "populator-namespace", "populator-system", "Namespace for the populator resources.")
	flag.StringVar(&populatorPrefix, "populator-prefix", "populator", "Prefix to use for populator resources.")

	rookConfig := &rook.Config{}
	flag.StringVar(&rookConfig.ClusterId, "rook-cluster-id", rook.ClusterIdDefaultValue, "rook ceph cluster ID")
	flag.StringVar(&rookConfig.MonitorConfigMapName, "rook-ceph-mon-cm-name", rook.MonitorConfigMapNameDefaultValue, "ConfigMap name containing actual ceph monitor list")
	flag.StringVar(&rookConfig.MonitorConfigMapDataKey, "rook-ceph-mon-cm-data-key", rook.MonitorConfigMapDataKeyDefaultValue, "Ceph monitor ConfigMap key")
	flag.StringVar(&rookConfig.Namespace, "rook-namespace", rook.NamespaceDefaultValue, "namespace for rook operator and ceph cluster")
	flag.StringVar(&rookConfig.BucketProvisioner, "rook-bucket-provisioner", rook.BucketProvisionerDefaultValue, "Name of Ceph CSI driver for buckets")
	flag.BoolVar(&rookConfig.EnableRBDStats, "pool-enable-rbd-stats", rook.EnableRBDStatsDefaultValue, "Enables collecting RBD per-image IO statistics by enabling dynamic OSD performance counters.")
	flag.StringVar(&rookConfig.CSIRBDProvisionerSecretName, "rook-csi-rbd-provisioner-secret-name", rook.CSIRBDProvisionerSecretNameDefaultValue, "Secret name containing Ceph csi rbd provisioner secrets")
	flag.StringVar(&rookConfig.CSIRBDNodeSecretName, "rook-csi-rbd-node-secret-name", rook.CSIRBDNodeSecretNameDefaultValue, "Secret name containing Ceph csi rbd node secrets")
	flag.BoolVar(&rookConfig.StorageClassAllowVolumeExpansion, "ceph-sc-allow-volume-expansion", rook.StorageClassAllowVolumeExpansionDefaultValue, "Ceph StorageClass: value for 'allowVolumeExpansion' field")
	flag.StringVar(&rookConfig.StorageClassFSType, "ceph-sc-fs-type", rook.StorageClassFSTypeDefaultValue, "Ceph StorageClass: value for 'csi.storage.k8s.io/fstype' parameter")
	flag.StringVar(&rookConfig.StorageClassImageFeatures, "ceph-sc-image-features", rook.StorageClassImageFeaturesDefaultValue, "Ceph StorageClass: value for 'imageFeatures' parameter")
	flag.StringSliceVar(&rookConfig.StorageClassMountOptions, "ceph-sc-mount-options", rook.StorageClassMountOptionsDefaultValue, "Ceph StorageClass: value for 'mountOptions' field, comma-separated values.")
	flag.StringVar(&rookConfig.StorageClassReclaimPolicy, "ceph-sc-reclaim-policy", rook.StorageClassReclaimPolicyDefaultValue, "Ceph StorageClass: value for 'reclaimPolicy' field")
	flag.StringVar(&rookConfig.StorageClassVolumeBindingMode, "ceph-sc-volume-binding-mode", rook.StorageClassVolumeBindingModeDefaultValue, "Ceph StorageClass: value for 'volumeBindingMode' field")
	flag.StringVar(&rookConfig.CSIDriverName, "ceph-csi-driver", rook.CSIDriverNameDefaultValue, "Name of Ceph CSI driver")
	flag.StringVar(&rookConfig.DashboardEndpoint, "rook-dashboard-endpoint", "https://rook-ceph-mgr-dashboard.rook-ceph.svc.cluster.local:8443", "Endpoint pointing to the ceph dashboard")
	flag.BoolVar(&rookConfig.DashboardInsecureSkipVerify, "rook-dashboard-insecure-skip-verify", false, "DashboardInsecureSkipVerify controls whether a client verifies the ceph dashboard's certificate chain and host name")
	flag.StringVar(&rookConfig.DashboardUser, "rook-dashboard-user", "admin", "user name which is used talk to the ceph dashboard api")
	flag.StringVar(&rookConfig.DashboardSecretName, "rook-dashboard-secret-name", "rook-ceph-dashboard-password", "Secret name containing password for the dashboard user")
	flag.IntVar(&rookConfig.DashboardTokenRefreshInMinutes, "rook-dashboard-token-refresh", 7*60, "Defines when the ceph dashboard token should be refreshed")
	flag.Int64Var(&rookConfig.BurstFactor, "burst-factor", 10, "Defines the factor to calculate the burst limits")
	flag.Int64Var(&rookConfig.BurstDurationInSeconds, "burst-duration", 15, "Defines the duration how long a volume can burst")

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
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		VolumePoolName: volumePoolName,
		RookConfig:     rookConfig,
		PoolUsage:      poolUsage,
		EventRecorder:  mgr.GetEventRecorderFor("volume"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Volume")
		os.Exit(1)
	}
	if err = (&controllers.VolumePoolReconciler{
		Client:                mgr.GetClient(),
		Scheme:                mgr.GetScheme(),
		VolumePoolName:        volumePoolName,
		VolumePoolProviderID:  volumePoolProviderID,
		VolumePoolLabels:      volumePoolLabels,
		VolumePoolAnnotations: volumePoolAnnotations,
		VolumeClassSelector:   volumeClassSelector,
		VolumePoolReplication: volumePoolReplication,
		RookConfig:            rookConfig,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VolumePool")
		os.Exit(1)
	}

	if err = (&controllers.BucketReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		BucketPoolName: bucketPoolName,
		BucketBaseUrl:  bucketBaseUrl,
		RookConfig:     rookConfig,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Bucket")
		os.Exit(1)
	}

	if err = (&controllers.BucketPoolReconciler{
		Client:                mgr.GetClient(),
		Scheme:                mgr.GetScheme(),
		BucketPoolName:        bucketPoolName,
		BucketPoolProviderID:  bucketPoolProviderID,
		BucketPoolLabels:      bucketPoolLabels,
		BucketPoolAnnotations: bucketPoolAnnotations,
		BucketClassSelector:   bucketClassSelector,
		BucketPoolReplication: bucketPoolReplication,
		RookConfig:            rookConfig,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "BucketPool")
		os.Exit(1)
	}

	if err = (&controllers.ImagePopulatorReconciler{
		Client:                 mgr.GetClient(),
		Scheme:                 mgr.GetScheme(),
		PopulatorImageName:     populatorImage,
		PopulatorPodDevicePath: populatorDevicePath,
		PopulatorNamespace:     populatorNamespace,
		Prefix:                 populatorPrefix,
		RookConfig:             rookConfig,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ImagePopulator")
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
