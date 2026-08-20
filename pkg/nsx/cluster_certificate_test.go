/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

func TestClusterCertificateInputForms(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "verified")
	}))
	defer server.Close()
	require.True(t, server.Certificate().IsCA, "the server certificate must also exercise CA trust")
	matchingPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	untrustedCA := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "untrusted NSX CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	untrustedDER, err := x509.CreateCertificate(rand.Reader, untrustedCA, untrustedCA, publicKey, privateKey)
	require.NoError(t, err)
	untrustedPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: untrustedDER})

	for _, certType := range []string{"ca", "leaf"} {
		for _, inputForm := range []string{"file", "inline"} {
			for _, clientType := range []string{"normal", "no-balancer"} {
				for _, trust := range []string{"matching", "untrusted"} {
					t.Run(certType+"/"+inputForm+"/"+clientType+"/"+trust, func(t *testing.T) {
						certPEM := matchingPEM
						if trust == "untrusted" {
							certPEM = untrustedPEM
						}
						input := string(certPEM)
						if inputForm == "file" {
							input = filepath.Join(t.TempDir(), "nsx.crt")
							require.NoError(t, os.WriteFile(input, certPEM, 0600))
						}
						cfg := &config.NsxConfig{NsxApiManagers: []string{server.Listener.Addr().String()}}
						if certType == "leaf" {
							cfg.LeafCertFile = []string{input}
						} else {
							cfg.CaFile = []string{input}
						}
						require.NoError(t, cfg.ValidateConfigFromCmd())
						cluster := &Cluster{config: &Config{
							CAFile: cfg.CaFile, IsLeafCert: len(cfg.LeafCertFile) > 0,
							HTTPTimeout: 2, Insecure: cfg.Insecure,
						}}
						client := cluster.createNoBalancerClient(2, 1)
						if clientType == "normal" {
							client = &http.Client{Transport: cluster.createTransport(1).Base, Timeout: 2 * time.Second}
						}
						t.Cleanup(client.CloseIdleConnections)
						response, err := client.Get(server.URL)
						if response != nil {
							defer response.Body.Close()
						}
						if trust == "untrusted" {
							require.Error(t, err)
							if certType == "leaf" {
								require.ErrorContains(t, err, "server certificate didn't match pinned leaf cert")
							} else {
								require.ErrorContains(t, err, "certificate signed by unknown authority")
							}
							return
						}
						require.NoError(t, err)
						require.Equal(t, http.StatusOK, response.StatusCode)
						body, err := io.ReadAll(response.Body)
						require.NoError(t, err)
						require.Equal(t, "verified", string(body))
					})
				}
			}
		}
	}
}
