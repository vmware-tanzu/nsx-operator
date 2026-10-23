package common

import (
	"crypto/tls"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

const NCPConfigWebhookPath = "/validate-nsx-vmware-com-v1-ncpconfig"

// SetupWebhookServer registers validators only when VPC namespaces are enabled.
// T1 installations do not require webhook resources, certificates, or RBAC.
func SetupWebhookServer(mgr ctrl.Manager, vpcEnabled bool) (webhook.Server, error) {
	if !vpcEnabled {
		return nil, nil
	}
	if err := util.GenerateWebhookCerts(); err != nil {
		return nil, err
	}
	server := webhook.NewServer(webhook.Options{
		Port: config.WebhookServerPort, CertDir: config.WebhookCertDir,
		TLSOpts: []func(*tls.Config){func(cfg *tls.Config) { cfg.MinVersion = tls.VersionTLS13 }},
	})
	server.Register(NCPConfigWebhookPath, &webhook.Admission{
		Handler: &NCPConfigValidator{decoder: admission.NewDecoder(mgr.GetScheme())},
	})
	if err := mgr.Add(server); err != nil {
		return nil, err
	}
	return server, nil
}
