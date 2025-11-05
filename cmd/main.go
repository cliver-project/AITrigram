/*
Copyright 2025 Lin Gao.

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
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
	"github.com/cliver-project/AITrigram/internal/controller"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
	version  = "0.0.1"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(aitrigramv1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	ctrl.SetLogger(zap.New(zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))

	cmd := &cobra.Command{
		Use: "llmengine-operator",
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
			os.Exit(1)
		},
	}

	cmd.Version = version

	cmd.AddCommand(NewRunCommand())

	if err := cmd.Execute(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

type StartOptions struct {
	Namespace            string
	MetricsAddr          string
	probeAddr            string
	enableLeaderElection bool
}

func NewRunCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Runs the LLMEngine operator",
	}

	opts := StartOptions{
		Namespace:   "aitrigram-system",
		MetricsAddr: "0",
	}

	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace,
		"The namespace where the operator creates Jobs and other resources.")
	cmd.Flags().StringVar(&opts.probeAddr, "health-probe-bind-address", ":8081",
		"The address the probe endpoint binds to.")
	cmd.Flags().BoolVar(&opts.enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager."+
			"Enabling this will ensure there is only one active controller manager.")
	cmd.Flags().StringVar(&opts.MetricsAddr, "metrics-bind-address", opts.MetricsAddr,
		"The address the metric endpoint binds to.")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(ctrl.SetupSignalHandler())
		defer cancel()

		if err := run(ctx, &opts, ctrl.Log.WithName("setup")); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}

	return cmd
}

func run(ctx context.Context, opts *StartOptions, log logr.Logger) error {
	log.Info("Starting llmengine-operator", "version", version)
	kubeConfig := ctrl.GetConfigOrDie()
	kubeConfig.UserAgent = "aitrigram-controller"
	leaseDuration := time.Second * 60
	renewDeadline := time.Second * 40
	retryPeriod := time.Second * 15
	ctrlOptions := ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: opts.probeAddr,
		Metrics: metricsserver.Options{
			BindAddress: opts.MetricsAddr,
		},
		Client: crclient.Options{
			Cache: &crclient.CacheOptions{
				Unstructured: true,
			},
		},
		LeaderElection:                opts.enableLeaderElection,
		LeaderElectionID:              "llmengine-operator-leader-elect",
		LeaderElectionResourceLock:    "leases",
		LeaderElectionReleaseOnCancel: true,
		LeaderElectionNamespace:       opts.Namespace,
		LeaseDuration:                 &leaseDuration,
		RenewDeadline:                 &renewDeadline,
		RetryPeriod:                   &retryPeriod,
	}
	mgr, err := ctrl.NewManager(kubeConfig, ctrlOptions)
	if err != nil {
		return fmt.Errorf("unable to start manager: %w", err)

	}
	llmEngineReconciler := &controller.LLMEngineReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		OperatorNamespace: opts.Namespace,
	}
	if err = llmEngineReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "LLMEngine")
		return err
	}
	// LLMModel controller has been replaced by ModelRepository controller
	if err = (&controller.ModelRepositoryReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		OperatorNamespace: opts.Namespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ModelRepository")
		return err
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		return err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		return err
	}

	setupLog.Info("starting manager")
	return mgr.Start(ctx)
}
