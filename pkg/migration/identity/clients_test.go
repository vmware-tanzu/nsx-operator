/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vmware/govmomi/vapi/rest"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

func TestVCenterCredentialsFromFiles(t *testing.T) {
	dir := t.TempDir()
	usernamePath := filepath.Join(dir, "username")
	passwordPath := filepath.Join(dir, "password")
	require.NoError(t, os.WriteFile(usernamePath, []byte(" service-user \n"), 0600))
	require.NoError(t, os.WriteFile(passwordPath, []byte(" secret-value \n"), 0600))

	username, password, err := vCenterCredentialsFromFiles(&config.VCConfig{}, usernamePath, passwordPath)
	require.NoError(t, err)
	require.Equal(t, "service-user", username)
	require.Equal(t, "secret-value", password)

	username, password, err = vCenterCredentialsFromFiles(&config.VCConfig{VCUser: "configured", VCPassword: "configured-secret"}, "", "")
	require.NoError(t, err)
	require.Equal(t, "configured", username)
	require.Equal(t, "configured-secret", password)

	_, _, err = vCenterCredentialsFromFiles(&config.VCConfig{}, usernamePath, filepath.Join(dir, "missing"))
	require.ErrorContains(t, err, "read vCenter service-account password")
	_, _, err = vCenterCredentialsFromFiles(&config.VCConfig{VCUser: "only-user"}, usernamePath, passwordPath)
	require.ErrorContains(t, err, "must be provided together")
}

func TestVCenterRootCAsAndTLSConfig(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	certificateTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "migration-test-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	certificate, err := x509.CreateCertificate(rand.Reader, certificateTemplate, certificateTemplate, &key.PublicKey, key)
	require.NoError(t, err)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate}), 0600))

	rootCAs, err := vCenterRootCAs(caPath)
	require.NoError(t, err)
	transportConfig := vCenterTLSConfig(rootCAs)
	require.Equal(t, uint16(tls.VersionTLS12), transportConfig.MinVersion)
	require.Same(t, rootCAs, transportConfig.RootCAs)

	invalidCAPath := filepath.Join(t.TempDir(), "invalid.pem")
	require.NoError(t, os.WriteFile(invalidCAPath, []byte("not a certificate"), 0600))
	_, err = vCenterRootCAs(invalidCAPath)
	require.ErrorContains(t, err, "contains no valid certificates")
	_, err = vCenterRootCAs(filepath.Join(t.TempDir(), "missing.pem"))
	require.ErrorContains(t, err, "read vCenter CA certificate")
}

func TestCleanupContext(t *testing.T) {
	t.Run("replaces canceled context with bounded background context", func(t *testing.T) {
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		cleanupCtx, cleanupCancel := cleanupContext(canceled)
		defer cleanupCancel()
		require.NoError(t, cleanupCtx.Err())
		_, hasDeadline := cleanupCtx.Deadline()
		require.True(t, hasDeadline)
	})

	t.Run("preserves active context", func(t *testing.T) {
		ctx := context.Background()
		cleanupCtx, cleanupCancel := cleanupContext(ctx)
		defer cleanupCancel()
		require.True(t, ctx == cleanupCtx)
	})

}

type fakeRESTSessionManager struct {
	session     *rest.Session
	sessionErr  error
	loginErr    error
	logins      int
	credentials *url.Userinfo
}

func (manager *fakeRESTSessionManager) Session(context.Context) (*rest.Session, error) {
	return manager.session, manager.sessionErr
}

func (manager *fakeRESTSessionManager) Login(_ context.Context, credentials *url.Userinfo) error {
	manager.logins++
	manager.credentials = credentials
	if manager.loginErr == nil {
		manager.session = &rest.Session{User: credentials.Username()}
	}
	return manager.loginErr
}

func TestEnsureVCenterRESTSession(t *testing.T) {
	credentials := url.UserPassword("migration-user", "secret")
	t.Run("relogs expired session", func(t *testing.T) {
		manager := &fakeRESTSessionManager{}
		refreshed, err := ensureVCenterRESTSession(context.Background(), manager, credentials)
		require.NoError(t, err)
		require.True(t, refreshed)
		require.Equal(t, 1, manager.logins)
		require.Same(t, credentials, manager.credentials)
		refreshed, err = ensureVCenterRESTSession(context.Background(), manager, credentials)
		require.NoError(t, err)
		require.False(t, refreshed)
		require.Equal(t, 1, manager.logins)
	})

	t.Run("leaves valid session alone", func(t *testing.T) {
		manager := &fakeRESTSessionManager{session: &rest.Session{User: "migration-user"}}
		refreshed, err := ensureVCenterRESTSession(context.Background(), manager, credentials)
		require.NoError(t, err)
		require.False(t, refreshed)
		require.Zero(t, manager.logins)
	})

	t.Run("fails when expired session has no credentials", func(t *testing.T) {
		manager := &fakeRESTSessionManager{}
		_, err := ensureVCenterRESTSession(context.Background(), manager, nil)
		require.ErrorContains(t, err, "credentials are unavailable")
		require.Zero(t, manager.logins)
	})

	t.Run("reports relogin failures", func(t *testing.T) {
		manager := &fakeRESTSessionManager{loginErr: os.ErrPermission}
		_, err := ensureVCenterRESTSession(context.Background(), manager, credentials)
		require.ErrorIs(t, err, os.ErrPermission)
		require.ErrorContains(t, err, "refresh vCenter REST session")
	})
}
