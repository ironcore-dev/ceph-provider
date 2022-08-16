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

	flag "github.com/spf13/pflag"
	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/onmetal/cephlet/controllers"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

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
	var monitorEndpointConfigMapDataKey string
	var monintorEndpointConfigMapName string

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
	flag.StringVar(&monintorEndpointConfigMapName, "rook-ceph-mon-cm-name", "rook-ceph-mon-endpoints", "ConfigMap name containing actual ceph monitor list")
	flag.StringVar(&monitorEndpointConfigMapDataKey, "rook-ceph-mon-cm-data-key", "csi-cluster-config-json", "Ceph monitor ConfigMap key")
	flag.StringVar(&rookNamespace, "rook-namespace", "rook-ceph", "namespace for rook operator and ceph cluster")
	flag.BoolVar(&enableRBDStats, "pool-enable-rbd-stats", false, "Enables collecting RBD per-image IO statistics by enabling dynamic OSD performance counters.")

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
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controllers.VolumeReconciler{
		Client:                              mgr.GetClient(),
		Scheme:                              mgr.GetScheme(),
		VolumePoolReplication:               volumePoolReplication,
		VolumePoolName:                      volumePoolName,
		VolumePoolLabels:                    volumePoolLabels,
		VolumePoolAnnotations:               volumePoolAnnotations,
		RookNamespace:                       rookNamespace,
		RookMonitorEndpointConfigMapDataKey: monitorEndpointConfigMapDataKey,
		RookMonitorEndpointConfigMapName:    monintorEndpointConfigMapName,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Volume")
		os.Exit(1)
	}
	if err = (&controllers.VolumePoolReconciler{
		Client:                mgr.GetClient(),
		Scheme:                mgr.GetScheme(),
		VolumePoolReplication: volumePoolReplication,
		VolumePoolName:        volumePoolName,
		VolumePoolLabels:      volumePoolLabels,
		VolumePoolAnnotations: volumePoolAnnotations,
		VolumeClassSelector:   volumeClassSelector,
		RookNamespace:         rookNamespace,
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
