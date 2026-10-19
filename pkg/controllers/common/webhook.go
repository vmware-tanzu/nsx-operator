package common

import (
	"context"
	"crypto/tls"
	"fmt"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

const NCPConfigWebhookPath = "/validate-nsx-vmware-com-v1-ncpconfig"

// SetupWebhookServer also registers the shared NCPConfig validator in T1 mode.
// Legacy T1 installations without webhook resources/RBAC keep running without it.
func SetupWebhookServer(mgr ctrl.Manager, required bool) (webhook.Server, error) {
	if !required {
		installed := &admissionregistrationv1.ValidatingWebhookConfiguration{}
		err := mgr.GetAPIReader().Get(context.Background(), client.ObjectKey{Name: "nsx-operator-validating-webhook-configuration"}, installed)
		if apierrors.IsNotFound(err) || apierrors.IsForbidden(err) {
			log.Info("Webhook is not available in this T1 installation, skipping webhook server", "error", err)
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("failed to check webhook installation: %w", err)
		}
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
