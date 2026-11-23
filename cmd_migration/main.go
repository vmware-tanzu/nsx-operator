/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package main

import (
	"context"
	"crypto/tls"
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
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	webhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	netop "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration"
	migrationadmission "github.com/vmware-tanzu/nsx-operator/pkg/migration/admission"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/identity"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/infrastructure"
	pkgutil "github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// Keep the existing lease identity across API group changes to preserve leader fencing.
const migrationLeaseID = "supervisor-network-migration-operator.iaas.vmware.com"

type options struct {
	enabled           bool
	namespace         string
	operatorNamespace string
	supervisorID      string
	netOperatorNS     string
	admissionEnabled  bool
	admissionPort     int
	admissionCertDir  string
}

func registerFlags(flags *flag.FlagSet) (*options, error) {
	config.RegisterFlags(flags)
	for key, value := range map[string]string{"health-probe-bind-address": ":9924", "metrics-bind-address": "0"} {
		if err := flags.Set(key, value); err != nil {
			return nil, err
		}
		flags.Lookup(key).DefValue = value
	}
	operatorNamespace := os.Getenv("NSX_OPERATOR_NAMESPACE")
	if operatorNamespace == "" {
		operatorNamespace = "vmware-system-nsx"
	}
	supervisorID := os.Getenv("NSX_MIGRATION_SUPERVISOR_ID")
	netOperatorNamespace := os.Getenv("NET_OPERATOR_NAMESPACE")
	if netOperatorNamespace == "" {
		netOperatorNamespace = "vmware-system-netop"
	}
	opts := &options{operatorNamespace: operatorNamespace, supervisorID: supervisorID, netOperatorNS: netOperatorNamespace}
	flags.BoolVar(&opts.enabled, "enable-network-migration", false, "Enable migration controllers and the infrastructure Evaluate handler")
	flags.StringVar(&opts.namespace, "leader-election-namespace", operatorNamespace, "Namespace for the migration operator's independent lease")
	flags.StringVar(&opts.supervisorID, "supervisor-id", supervisorID, "Override the Supervisor ID read from nsx-ncp-config")
	flags.StringVar(&opts.netOperatorNS, "net-operator-namespace", netOperatorNamespace, "Namespace containing Net Operator source Networks")
	flags.BoolVar(&opts.admissionEnabled, "enable-migration-admission", false, "Serve migration admission independently of transition controllers")
	flags.IntVar(&opts.admissionPort, "migration-admission-port", 9982, "Port for the migration admission webhook")
	flags.StringVar(&opts.admissionCertDir, "migration-admission-cert-dir", "/etc/nsx-operator/migration-webhook-certs", "Directory containing tls.crt and tls.key for the migration admission webhook")
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
	ctx := ctrl.SetupSignalHandler()
	var migrationClients *identity.AuthenticatedClients
	defer func() {
		if migrationClients == nil {
			return
		}
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := migrationClients.Close(closeCtx); err != nil {
			ctrl.Log.Error(err, "Failed to close migration client sessions")
		}
	}()
	setupControllers := func(mgr ctrl.Manager) error {
		supervisorID := opts.supervisorID
		if supervisorID == "" {
			var err error
			supervisorID, err = infrastructure.ReadSupervisorIDCandidateFromNCPConfig(ctx, mgr.GetAPIReader(), opts.operatorNamespace)
			if err != nil {
				return fmt.Errorf("resolve Supervisor ID from nsx-ncp-config: %w", err)
			}
		}
		operatorConfig, err := config.NewNSXOperatorConfigFromFile()
		if err != nil {
			return fmt.Errorf("load NSX Operator configuration for migration: %w", err)
		}
		migrationClients, err = identity.NewAuthenticatedClients(ctx, operatorConfig)
		if err != nil {
			return err
		}
		identityResolver, err := identity.NewResolver(migrationClients)
		if err != nil {
			return err
		}
		evaluateHandler := infrastructure.RuntimeEvaluateHandler{
			Clients:              migrationClients,
			Identity:             identityResolver,
			SupervisorID:         supervisorID,
			KubernetesClient:     mgr.GetClient(),
			KubernetesReader:     mgr.GetAPIReader(),
			DefaultProject:       operatorConfig.DefaultProject,
			NetOperatorNamespace: opts.netOperatorNS,
		}
		return migration.SetupWithEvaluateHandler(mgr, evaluateHandler)
	}
	return startManager(ctx, opts, managerDependencies{
		loadConfig:       ctrl.GetConfig,
		getConfig:        pkgutil.GetConfig,
		newManager:       ctrl.NewManager,
		setupControllers: setupControllers,
	})
}

// managerDependencies keeps startup wiring testable without a live Kubernetes API.
type managerDependencies struct {
	loadConfig       func() (*rest.Config, error)
	getConfig        func() (*rest.Config, error)
	newManager       func(*rest.Config, ctrl.Options) (ctrl.Manager, error)
	setupControllers func(ctrl.Manager) error
}

func startManager(ctx context.Context, opts *options, deps managerDependencies) error {
	if opts.enabled && opts.namespace == "" {
		return fmt.Errorf("leader election namespace must not be empty")
	}
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return err
	}
	if err := api.AddToScheme(scheme); err != nil {
		return err
	}
	if err := netop.AddToScheme(scheme); err != nil {
		return err
	}
	// Disabled startup only needs local REST configuration to construct the
	// manager. Do not gate its process probes on the API server's /healthz.
	// Enabled startup retains the existing health check and loopback fallback.
	getConfig := deps.loadConfig
	if opts.enabled {
		getConfig = deps.getConfig
	}
	cfg, err := getConfig()
	if err != nil {
		return err
	}
	leaderConfig := rest.CopyConfig(cfg)
	leaderConfig.Timeout = 5 * time.Second
	managerOptions := ctrl.Options{
		Scheme:                  scheme,
		Metrics:                 metricsserver.Options{BindAddress: config.MetricsAddr},
		HealthProbeBindAddress:  config.ProbeAddr,
		LeaderElection:          opts.enabled,
		LeaderElectionID:        migrationLeaseID,
		LeaderElectionNamespace: opts.namespace,
		LeaderElectionConfig:    leaderConfig,
	}
	if opts.admissionEnabled {
		if opts.admissionPort < 1 || opts.admissionPort > 65535 || opts.admissionCertDir == "" {
			return fmt.Errorf("invalid migration admission configuration: port=%d (expected 1-65535), certDir=%q (must not be empty)", opts.admissionPort, opts.admissionCertDir)
		}
		managerOptions.WebhookServer = webhook.NewServer(webhook.Options{
			Port: opts.admissionPort, CertDir: opts.admissionCertDir,
			TLSOpts: []func(*tls.Config){func(c *tls.Config) { c.MinVersion = tls.VersionTLS13 }},
		})
	}
	mgr, err := deps.newManager(cfg, managerOptions)
	if err != nil {
		return err
	}
	if opts.enabled {
		if err := deps.setupControllers(mgr); err != nil {
			return err
		}
	}
	if opts.admissionEnabled {
		server := mgr.GetWebhookServer()
		server.Register(migrationadmission.TransitionValidationPath, &webhookadmission.Webhook{Handler: migrationadmission.TransitionValidator{Reader: mgr.GetAPIReader()}})
		server.Register(migrationadmission.NamespaceValidationPath, &webhookadmission.Webhook{Handler: migrationadmission.NamespaceLifecycleValidator{
			Reader:         mgr.GetAPIReader(),
			ControllerUser: fmt.Sprintf("system:serviceaccount:%s:%s", opts.operatorNamespace, migrationadmission.MigrationControllerServiceAccount),
		}})
		server.Register(migrationadmission.WorkloadValidationPath, &webhookadmission.Webhook{Handler: migrationadmission.WorkloadCutoverValidator{
			Reader: mgr.GetAPIReader(),
			ControllerUsers: []string{
				migrationadmission.NCPControllerUser,
				migrationadmission.NetOperatorControllerUser,
				migrationadmission.VMOperatorControllerUser,
				migrationadmission.AKOControllerUser,
			},
		}})
		if err := mgr.AddReadyzCheck("migration-admission", server.StartedChecker()); err != nil {
			return err
		}
	}
	ctrl.LoggerFrom(ctx).WithName("migration").Info("Starting migration framework", "controllersEnabled", opts.enabled, "leaderElectionEnabled", opts.enabled)
	// Process health remains independent of NSX connectivity and migration state.
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return err
	}
	return mgr.Start(ctx)
}

func main() {
	if err := run(); err != nil {
		ctrl.Log.Error(err, "Migration manager stopped")
		os.Exit(1)
	}
}
