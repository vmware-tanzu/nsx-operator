/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package server

import (
	"context"
	"fmt"
	"net"
	"os"
	"path"
	"strconv"

	openapinamer "k8s.io/apiserver/pkg/endpoints/openapi"
	apirest "k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/healthz"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	apiservercompat "k8s.io/apiserver/pkg/util/compatibility"
	restclient "k8s.io/client-go/rest"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/rest"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/storage"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

const (
	defaultPort       = "9553"
	easPortEnv        = "EAS_PORT"
	easBindAddressEnv = "EAS_BIND_ADDRESS"
)

// EASServer is an Extension API Server that serves EAS read-only resources by
// fetching data from the NSX API on each request.  It is built on the generic
// apiserver framework (k8s.io/apiserver/pkg/server), which provides delegated
// authentication, delegated authorization, content negotiation, and table output.
//
// Because EAS is read-only it does not need leader election; all replicas serve
// concurrently and the Kubernetes Service load-balances across Ready pods.
type EASServer struct {
	vpcProvider     eas.VPCInfoProvider
	vpcIPUsage      *storage.VPCIPAddressUsageStorage
	ipBlockUsage    *storage.IPBlockUsageStorage
	subnetIPPools   *storage.SubnetIPPoolsStorage
	subnetDHCPStats *storage.SubnetDHCPStatsStorage
	// nsxHealthChecker is added to the generic API server's /readyz endpoint
	// so that the pod is removed from Service endpoints when NSX is unreachable.
	nsxHealthChecker healthz.HealthChecker
	// restConfig, when non-nil, is used to self-register the APIService object
	// in kube-apiserver's aggregation layer on startup.  Its TLSClientConfig
	// fields (CAFile / CAData) are also used to configure delegated
	// authentication so the server does not need to read the
	// extension-apiserver-authentication ConfigMap from kube-system.
	restConfig *restclient.Config
	// kubeConfigFile, when non-empty, is the path to the kubeconfig that the
	// delegated auth/authz webhook should use when calling TokenReview and
	// SubjectAccessReview on kube-apiserver.  Set from ncp.ini [k8s].kubeconfig.
	kubeConfigFile string
	// caCert is the PEM-encoded CA that signed the EAS TLS certificate.
	// When non-nil it is injected into the APIService caBundle so that
	// kube-apiserver can verify the EAS TLS connection.
	caCert []byte
}

// NewEASServer creates a fully wired EASServer.
// restConfig may be nil; when non-nil, the APIService for
// eas.nsx.vmware.com/v1alpha1 is registered in the kube-apiserver aggregation
// layer via a PostStartHook — i.e., only after the TLS listener is bound and
// ready to serve, so kube-apiserver does not proxy requests to EAS prematurely.
// kubeConfigFile is the kubeconfig path from ncp.ini [k8s].kubeconfig; pass ""
// to fall back to the in-cluster service-account token.
// caCert is the PEM-encoded CA returned by GenerateEASCerts; pass nil to fall
// back to insecureSkipTLSVerify on the APIService.
func NewEASServer(
	nsxClient *nsx.Client,
	vpcProvider eas.VPCInfoProvider,
	k8sClient k8sclient.Client,
	restConfig *restclient.Config,
	kubeConfigFile string,
	caCert []byte,
) *EASServer {
	return &EASServer{
		vpcProvider:     vpcProvider,
		vpcIPUsage:      storage.NewVPCIPAddressUsageStorage(nsxClient, vpcProvider),
		ipBlockUsage:    storage.NewIPBlockUsageStorage(nsxClient, vpcProvider),
		subnetIPPools:   storage.NewSubnetIPPoolsStorage(nsxClient, k8sClient),
		subnetDHCPStats: storage.NewSubnetDHCPStatsStorage(nsxClient, k8sClient),
		// /readyz reports not-ready when NSX is unreachable, causing kube-proxy
		// to stop routing traffic to this pod until connectivity is restored.
		nsxHealthChecker: healthz.NamedCheck("nsx", nsxClient.NSXChecker.CheckNSXHealth),
		restConfig:       restConfig,
		kubeConfigFile:   kubeConfigFile,
		caCert:           caCert,
	}
}

// Start builds the generic API server (which
// registers the APIService via a PostStartHook once the TLS listener is ready)
// and then runs it until ctx is cancelled.
func (s *EASServer) Start(ctx context.Context) error {
	srv, err := s.buildGenericAPIServer()
	if err != nil {
		return err
	}
	return srv.PrepareRun().RunWithContext(ctx)
}

// buildGenericAPIServer constructs and configures the generic apiserver with:
//   - TLS from the EAS cert files (same files used by the previous net/http server)
//   - Delegated authentication via TokenReview to kube-apiserver (in-cluster)
//   - Delegated authorization via SubjectAccessReview to kube-apiserver
//   - The four EAS resource types registered as REST storage
func (s *EASServer) buildGenericAPIServer() (*genericapiserver.GenericAPIServer, error) {
	port, bindAddr, certFile, keyFile := listenerConfig()

	// ── TLS / listener ──────────────────────────────────────────────────────
	// Use .WithLoopback() so that ApplyTo also populates
	// cfg.Config.LoopbackClientConfig — genericapiserver.New() requires it to
	// be non-nil even though EAS never calls the loopback endpoint itself.
	servingOpts := genericoptions.NewSecureServingOptions().WithLoopback()
	servingOpts.BindPort = port
	if bindAddr != nil {
		servingOpts.BindAddress = bindAddr
	}
	servingOpts.ServerCert.CertKey.CertFile = certFile
	servingOpts.ServerCert.CertKey.KeyFile = keyFile

	// ── Delegated authentication ─────────────────────────────────────────────
	// SkipInClusterLookup=true prevents the generic apiserver from starting a
	// background reflector that watches kube-system/extension-apiserver-authentication.
	// Instead, the k8s connection parameters already present in restConfig
	// (CA file / CA data from ncp.ini [k8s].kubeconfig or the in-cluster
	// service-account token) are used directly.
	authnOpts := genericoptions.NewDelegatingAuthenticationOptions()
	authnOpts.SkipInClusterLookup = true
	if s.kubeConfigFile != "" {
		// Explicit kubeconfig from ncp.ini: use it for TokenReview calls so
		// the CA and server address are taken from the file rather than the
		// in-cluster environment.
		authnOpts.RemoteKubeConfigFile = s.kubeConfigFile
	}
	// ClientCert.ClientCA is intentionally NOT set.
	// Setting it to the cluster CA (from the service-account token) would
	// enable client-certificate authentication, but the kube-apiserver
	// aggregation-layer proxy presents a certificate signed by a WCP/Tanzu
	// CA that is different from the cluster CA.  Configuring the wrong CA
	// produces "x509: certificate signed by unknown authority" errors for
	// every proxied request.  Bearer-token authentication (the standard path
	// for kubectl users) works without any client-CA configuration.

	// ── Authorization ────────────────────────────────────────────────────────
	// Use the local easAuthorizer (pkg/eas/server/authorizer.go) instead of
	// delegating back to kube-apiserver via SubjectAccessReview.
	// See authorizer.go for the full rationale.

	// ── Build server config ──────────────────────────────────────────────────
	cfg := genericapiserver.NewRecommendedConfig(codecs)

	// NewRecommendedConfig leaves OpenAPIConfig and OpenAPIV3Config nil.
	// InstallAPIGroup requires OpenAPIV3Config to be non-nil, with a definition
	// for every type the EAS types reference.  zz_generated.openapi.go is
	// produced by "make generate-eas-openapi", which runs openapi-gen against
	// our EAS package plus k8s.io/apimachinery/pkg/apis/meta/v1 and
	// k8s.io/apimachinery/pkg/version, so ObjectMeta, ListMeta, and
	// version.Info are all covered without any runtime stubs.
	defNamer := openapinamer.NewDefinitionNamer(scheme)
	cfg.Config.OpenAPIConfig = genericapiserver.DefaultOpenAPIConfig(easv1alpha1.GetOpenAPIDefinitions, defNamer)
	cfg.Config.OpenAPIConfig.Info.Title = "nsx-eas"
	cfg.Config.OpenAPIConfig.Info.Version = "v1alpha1"
	cfg.Config.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(easv1alpha1.GetOpenAPIDefinitions, defNamer)
	cfg.Config.OpenAPIV3Config.Info.Title = "nsx-eas"
	cfg.Config.OpenAPIV3Config.Info.Version = "v1alpha1"

	// EffectiveVersion is required by Config.Complete() (introduced in
	// k8s.io/apiserver v0.35) but is not set by NewRecommendedConfig.
	// DefaultBuildEffectiveVersion derives the version from the binary build
	// info, matching what ServerRunOptions.ApplyTo would do in a flag-driven
	// server setup.
	cfg.Config.EffectiveVersion = apiservercompat.DefaultBuildEffectiveVersion()

	// Add the NSX health check to /readyz only:
	//   /healthz (/livez) = liveness: is the process alive?  (ping only — NSX
	//                        downtime should NOT restart the pod)
	//   /readyz           = readiness: can we serve useful data?  (fails when
	//                        NSX is unreachable so traffic stops routing here)
	if s.nsxHealthChecker != nil {
		cfg.Config.ReadyzChecks = append(cfg.Config.ReadyzChecks, s.nsxHealthChecker)
	}

	if err := servingOpts.ApplyTo(&cfg.Config.SecureServing, &cfg.Config.LoopbackClientConfig); err != nil {
		return nil, fmt.Errorf("apply secure serving options: %w", err)
	}
	if err := authnOpts.ApplyTo(&cfg.Authentication, cfg.SecureServing, cfg.OpenAPIConfig); err != nil {
		return nil, fmt.Errorf("apply authentication options: %w", err)
	}
	cfg.Config.Authorization.Authorizer = easAuthorizer{}

	// ── Create server ────────────────────────────────────────────────────────
	srv, err := cfg.Complete().New("nsx-eas", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, fmt.Errorf("create generic API server: %w", err)
	}

	// ── APIService self-registration ─────────────────────────────────────────
	// Register AFTER the TLS listener is bound (PostStartHook) so that
	// kube-apiserver does not start proxying requests before EAS is ready.
	if s.restConfig != nil {
		if err := srv.AddPostStartHook("register-eas-apiservice",
			func(hookCtx genericapiserver.PostStartHookContext) error {
				if err := s.registerExtensionAPIService(hookCtx.Context); err != nil {
					logger.Log.Error(err, "Failed to register EAS APIService; "+
						"ensure RBAC allows apiregistration.k8s.io/apiservices "+
						"or install build/yaml/eas/apiservice.yaml manually")
				}
				return nil // registration failure is non-fatal
			},
		); err != nil {
			return nil, fmt.Errorf("add APIService registration hook: %w", err)
		}
	}

	// ── Install EAS API group ────────────────────────────────────────────────
	apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(
		easv1alpha1.GroupVersion.Group,
		scheme,
		parameterCodec,
		codecs,
	)
	apiGroupInfo.VersionedResourcesStorageMap[easv1alpha1.GroupVersion.Version] = map[string]apirest.Storage{
		"vpcipaddressusages":    rest.NewVPCIPUsageStorage(s.vpcIPUsage, s.vpcProvider),
		"ipblockusages":         rest.NewIPBlockUsageStorage(s.ipBlockUsage, s.vpcProvider),
		"subnetippools":         rest.NewSubnetIPPoolsStorage(s.subnetIPPools),
		"subnetdhcpserverstats": rest.NewSubnetDHCPStatsStorage(s.subnetDHCPStats),
	}

	if err := srv.InstallAPIGroup(&apiGroupInfo); err != nil {
		return nil, fmt.Errorf("install EAS API group: %w", err)
	}
	return srv, nil
}

// listenerConfig returns the port, optional bind address, and TLS cert paths
// derived from environment variables and the shared webhook cert directory.
func listenerConfig() (port int, bindAddr net.IP, certFile, keyFile string) {
	portStr := os.Getenv(easPortEnv)
	if portStr == "" {
		portStr = defaultPort
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p < 1 || p > 65535 {
		p = 9553
	}
	if host := os.Getenv(easBindAddressEnv); host != "" {
		bindAddr = net.ParseIP(host)
	}
	certFile = path.Join(config.WebhookCertDir, config.EASCertFile)
	keyFile = path.Join(config.WebhookCertDir, config.EASKeyFile)
	return p, bindAddr, certFile, keyFile
}
