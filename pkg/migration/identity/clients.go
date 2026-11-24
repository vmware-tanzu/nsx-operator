/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package identity

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/vapi/rest"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/soap"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/auth/jwt"
)

const defaultVCenterCAFile = "/etc/vmware/wcp/tls/vmca.pem"

// AuthenticatedClients owns the authenticated sessions used by migration.
// Call Close during manager shutdown to release both vCenter sessions.
type AuthenticatedClients struct {
	VCenter     *govmomi.Client
	VCenterREST *rest.Client
	NSX         *nsx.Client
}

// NewAuthenticatedClients builds the migration clients from the operator's
// existing vCenter/NSX configuration and service-account credential mounts.
func NewAuthenticatedClients(ctx context.Context, operatorConfig *config.NSXOperatorConfig) (*AuthenticatedClients, error) {
	if operatorConfig == nil || operatorConfig.VCConfig == nil || operatorConfig.NsxConfig == nil {
		return nil, fmt.Errorf("vCenter and NSX operator configuration are required")
	}
	if operatorConfig.Insecure {
		return nil, fmt.Errorf("migration requires TLS certificate verification for vCenter and NSX")
	}
	if strings.TrimSpace(operatorConfig.VCEndPoint) == "" || operatorConfig.HttpsPort < 1 || operatorConfig.HttpsPort > 65535 {
		return nil, fmt.Errorf("vCenter endpoint and a valid HTTPS port are required")
	}
	username, password, err := vCenterCredentials(operatorConfig.VCConfig)
	if err != nil {
		return nil, err
	}
	caFile := operatorConfig.VCCAFile
	if caFile == "" {
		caFile = defaultVCenterCAFile
	}
	rootCAs, err := vCenterRootCAs(caFile)
	if err != nil {
		return nil, err
	}
	endpoint := net.JoinHostPort(strings.Trim(operatorConfig.VCEndPoint, "[]"), strconv.Itoa(operatorConfig.HttpsPort))
	vcURL := &url.URL{Scheme: "https", Host: endpoint, Path: "/sdk"}
	userinfo := url.UserPassword(username, password)

	soapClient := soap.NewClient(vcURL, false)
	soapClient.DefaultTransport().TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: rootCAs}
	vimClient, err := vim25.NewClient(ctx, soapClient)
	if err != nil {
		return nil, fmt.Errorf("connect to vCenter: %w", err)
	}
	vcenter := &govmomi.Client{Client: vimClient, SessionManager: session.NewManager(vimClient)}
	if err := vcenter.Login(ctx, userinfo); err != nil {
		return nil, fmt.Errorf("authenticate to vCenter: %w", err)
	}
	vimClient.RoundTripper = session.KeepAliveHandler(vimClient.RoundTripper, 5*time.Minute, nil)

	vcenterREST := rest.NewClient(vimClient)
	if err := vcenterREST.Login(ctx, userinfo); err != nil {
		_ = vcenter.Logout(ctx)
		return nil, fmt.Errorf("authenticate to vCenter REST API: %w", err)
	}

	// Avoid changing the caller's shared config and request a fresh token provider
	// instead of reusing a package-level cached provider.
	migrationConfig := *operatorConfig
	migrationConfig.LibMode = true
	nsxClient, err := nsx.NewMigrationClient(&migrationConfig)
	if err != nil {
		_ = vcenterREST.Logout(ctx)
		_ = vcenter.Logout(ctx)
		return nil, fmt.Errorf("initialize authenticated NSX migration client: %w", err)
	}
	return &AuthenticatedClients{VCenter: vcenter, VCenterREST: vcenterREST, NSX: nsxClient}, nil
}

// Close releases authenticated sessions. It is safe to call more than once.
func (clients *AuthenticatedClients) Close(ctx context.Context) error {
	if clients == nil {
		return nil
	}
	if ctx == nil || ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
	}
	var restErr, soapErr error
	if clients.VCenterREST != nil {
		restErr = clients.VCenterREST.Logout(ctx)
	}
	if clients.VCenter != nil {
		soapErr = clients.VCenter.Logout(ctx)
	}
	if restErr != nil || soapErr != nil {
		return fmt.Errorf("close vCenter sessions: REST=%v SOAP=%v", restErr, soapErr)
	}
	return nil
}

func vCenterCredentials(vcConfig *config.VCConfig) (string, string, error) {
	if (vcConfig.VCUser == "") != (vcConfig.VCPassword == "") {
		return "", "", fmt.Errorf("vCenter username and password must be provided together")
	}
	if vcConfig.VCUser != "" {
		return vcConfig.VCUser, vcConfig.VCPassword, nil
	}
	username, err := readCredential(jwt.VC_SVCACCOUNT_USER_PATH)
	if err != nil {
		return "", "", fmt.Errorf("read vCenter service-account username: %w", err)
	}
	password, err := readCredential(jwt.VC_SVCACCOUNT_PWD_PATH)
	if err != nil {
		return "", "", fmt.Errorf("read vCenter service-account password: %w", err)
	}
	if username == "" || password == "" {
		return "", "", fmt.Errorf("vCenter service-account credentials are empty")
	}
	return username, password, nil
}

func readCredential(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(contents)), nil
}

func vCenterRootCAs(caFile string) (*x509.CertPool, error) {
	rootCAs, err := x509.SystemCertPool()
	if err != nil || rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	contents, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read vCenter CA certificate %q: %w", caFile, err)
	}
	if !rootCAs.AppendCertsFromPEM(contents) {
		return nil, fmt.Errorf("vCenter CA file %q contains no valid certificates", caFile)
	}
	return rootCAs, nil
}
