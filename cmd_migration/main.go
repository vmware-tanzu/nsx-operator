/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration"
	pkgutil "github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// Keep the existing lease identity across API group changes to preserve leader fencing.
const migrationLeaseID = "supervisor-network-migration-operator.iaas.vmware.com"

type options struct {
	enabled   bool
	namespace string
}

func registerFlags(flags *flag.FlagSet) (*options, error) {
	config.RegisterFlags(flags)
	for key, value := range map[string]string{"health-probe-bind-address": ":9924", "metrics-bind-address": "0"} {
		if err := flags.Set(key, value); err != nil {
			return nil, err
		}
		flags.Lookup(key).DefValue = value
	}
	namespace := os.Getenv("NSX_OPERATOR_NAMESPACE")
	if namespace == "" {
		namespace = "vmware-system-nsx"
	}
	opts := &options{}
	flags.BoolVar(&opts.enabled, "enable-network-migration", false, "Enable migration dispatch (no external migration handlers are implemented)")
	flags.StringVar(&opts.namespace, "leader-election-namespace", namespace, "Namespace for the migration operator's independent lease")
	return opts, nil
}

func run() error {
	opts, err := registerFlags(flag.CommandLine)
	if err != nil {
		return err
	}
	flag.Parse()
	log := logger.ZapCustomLogger(false, config.LogLevel)
	logger.Log = log
	ctrl.SetLogger(log.Logger)
	if opts.namespace == "" {
		return fmt.Errorf("leader election namespace must not be empty")
	}
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return err
	}
	if err := api.AddToScheme(scheme); err != nil {
		return err
	}
	cfg, err := pkgutil.GetConfig()
	if err != nil {
		return err
	}
	leaderConfig := rest.CopyConfig(cfg)
	leaderConfig.Timeout = 5 * time.Second
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                  scheme,
		Metrics:                 metricsserver.Options{BindAddress: config.MetricsAddr},
		HealthProbeBindAddress:  config.ProbeAddr,
		LeaderElection:          true,
		LeaderElectionID:        migrationLeaseID,
		LeaderElectionNamespace: opts.namespace,
		LeaderElectionConfig:    leaderConfig,
	})
	if err != nil {
		return err
	}
	operator := migration.New(opts.enabled)
	if err := operator.SetupWithManager(mgr); err != nil {
		return err
	}
	// Health describes this process; an NSX outage must not restart the container
	// responsible for reporting and recovering migration failures.
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return err
	}
	return mgr.Start(ctrl.SetupSignalHandler())
}

func main() {
	if err := run(); err != nil {
		ctrl.Log.Error(err, "Migration manager stopped")
		os.Exit(1)
	}
}
