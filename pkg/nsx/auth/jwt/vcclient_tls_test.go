/* Copyright © 2026 Broadcom. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package jwt

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vmware/govmomi/sts"
)

const testSDKResponse = `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/">
<soapenv:Body><RetrieveServiceContentResponse xmlns="urn:vim25"><returnval>
<sessionManager type="SessionManager">SessionManager</sessionManager>
</returnval></RetrieveServiceContentResponse></soapenv:Body></soapenv:Envelope>`

const testSTSFault = `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/">
<soapenv:Body><soapenv:Fault><faultcode>soapenv:Server</faultcode>
<faultstring>test STS fault</faultstring></soapenv:Fault></soapenv:Body></soapenv:Envelope>`

// Generate independent certificates: httptest's default TLS servers share a certificate.
func newTestVCCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "local vCenter test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func newTestVCClient(t *testing.T, serverURL string, ca []byte, insecure bool) *VCClient {
	t.Helper()
	u, err := url.Parse(serverURL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	client, err := NewVCClient(u.Hostname(), port, "vsphere.local", "test-user", "test-password", ca, insecure, u.Scheme)
	require.NoError(t, err)
	t.Cleanup(client.httpClient.CloseIdleConnections)
	return client
}

func TestVCClientSOAPTLSVerification(t *testing.T) {
	cert, ca := newTestVCCertificate(t)
	_, otherCA := newTestVCCertificate(t)
	for _, tt := range []struct {
		name     string
		ca       []byte
		insecure bool
		trusted  bool
	}{
		{name: "untrusted certificate"},
		{name: "configured CA", ca: ca, trusted: true},
		{name: "unrelated CA", ca: otherCA},
		{name: "invalid CA", ca: []byte("invalid CA")},
		{name: "explicit insecure mode", insecure: true, trusted: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/xml; charset=utf-8")
				_, _ = io.WriteString(w, testSDKResponse)
			}))
			server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
			server.StartTLS()
			t.Cleanup(server.Close)
			client := newTestVCClient(t, server.URL, tt.ca, tt.insecure)

			// REST and SOAP must make the same trust decision.
			response, restErr := client.httpClient.Get(server.URL + "/rest")
			if response != nil {
				_ = response.Body.Close()
			}
			vimClient, soapErr := client.createVimClient(context.Background(), server.URL+"/sdk")
			if tt.trusted {
				require.NoError(t, restErr)
				require.NoError(t, soapErr)
				t.Cleanup(vimClient.Client.CloseIdleConnections)
				require.EqualValues(t, 2, requests.Load())
			} else {
				var verificationErr *tls.CertificateVerificationError
				require.ErrorAs(t, restErr, &verificationErr)
				require.ErrorAs(t, soapErr, &verificationErr)
				require.Zero(t, requests.Load(), "untrusted peers must not receive HTTP requests")
			}
		})
	}
}

func TestVCClientSTSTLSVerification(t *testing.T) {
	sdkCert, sdkCA := newTestVCCertificate(t)
	stsCert, stsCA := newTestVCCertificate(t)
	certificate, err := createCertificate("test-user")
	require.NoError(t, err)
	for _, tt := range []struct {
		name     string
		ca       []byte
		insecure bool
		trusted  bool
	}{
		{name: "trusted SDK does not bypass untrusted STS", ca: sdkCA},
		{name: "configured CA also trusts STS", ca: append(append([]byte{}, sdkCA...), stsCA...), trusted: true},
		{name: "explicit insecure mode", insecure: true, trusted: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sdkServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/xml; charset=utf-8")
				_, _ = io.WriteString(w, testSDKResponse)
			}))
			sdkServer.TLS = &tls.Config{Certificates: []tls.Certificate{sdkCert}}
			sdkServer.StartTLS()
			t.Cleanup(sdkServer.Close)

			bodies := make(chan string, 1)
			stsServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				bodies <- string(body)
				w.Header().Set("Content-Type", "text/xml; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, testSTSFault)
			}))
			stsServer.TLS = &tls.Config{Certificates: []tls.Certificate{stsCert}}
			stsServer.StartTLS()
			t.Cleanup(stsServer.Close)

			client := newTestVCClient(t, stsServer.URL, tt.ca, tt.insecure)
			vimClient, err := client.createVimClient(context.Background(), sdkServer.URL+"/sdk")
			require.NoError(t, err, "SDK initialization must succeed before checking STS")
			t.Cleanup(vimClient.Client.CloseIdleConnections)
			sc := client.createSCClient(vimClient)
			stsClient := &sts.Client{Client: sc, RoundTripper: sc}
			_, err = stsClient.Issue(context.Background(), sts.TokenRequest{
				Certificate: certificate,
				Userinfo:    client.url.User,
				Delegatable: true,
				Renewable:   true,
			})
			if tt.trusted {
				require.ErrorContains(t, err, "test STS fault")
				require.Len(t, bodies, 1)
				body := <-bodies
				require.Contains(t, body, "test-user")
				require.Contains(t, body, "test-password")
			} else {
				var verificationErr *tls.CertificateVerificationError
				require.ErrorAs(t, err, &verificationErr)
				require.Empty(t, bodies, "untrusted STS must not receive credentials")
			}
		})
	}
}

func TestVCClientHOKSignerHTTP(t *testing.T) {
	var stsRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		switch r.URL.Path {
		case "/sdk":
			_, _ = io.WriteString(w, testSDKResponse)
		case "/sts/STSService/vsphere.local":
			stsRequests.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, testSTSFault)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := newTestVCClient(t, server.URL, nil, false)
	_, err := client.createHOKSigner()
	require.ErrorContains(t, err, "test STS fault")
	require.EqualValues(t, 1, stsRequests.Load(), "HTTP transport for Envoy must still reach STS")
}
