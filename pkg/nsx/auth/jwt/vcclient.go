/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package jwt

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/vmware/govmomi/sts"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/soap"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

// VCClient tracks a client session token.
type VCClient struct {
	url          *url.URL
	httpClient   *http.Client
	sessionMutex sync.Mutex
	session      string
	signer       *sts.Signer
	ssoDomain    string
}

var (
	stsClientMutex sync.Mutex
	stsClient      *sts.Client
)

func createHttpClient(insecureSkipVerify bool, caCertPem []byte) *http.Client {
	tlsConfig := &tls.Config{InsecureSkipVerify: insecureSkipVerify}
	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}
	if len(caCertPem) > 0 {
		log.V(1).Info("append CA cert")
		clientCertPool := x509.NewCertPool()
		clientCertPool.AppendCertsFromPEM(caCertPem)
		tlsConfig.RootCAs = clientCertPool
	}
	return &http.Client{Transport: transport, Timeout: time.Minute}
}

// NewVCClient creates a new logged in VC client with vapi session.
func NewVCClient(hostname string, port int, ssoDomain string, userName, password string, caCertPem []byte, insecureSkipVerify bool) (*VCClient, error) {
	httpClient := createHttpClient(insecureSkipVerify, caCertPem)
	baseurl := fmt.Sprintf("https://%s:%d/rest", hostname, port)
	vcurl, _ := url.Parse(baseurl)
	vcurl.User = url.UserPassword(userName, password)
	vcClient := &VCClient{
		url:        vcurl,
		httpClient: httpClient,
		ssoDomain:  ssoDomain,
	}
	// remove vcClient.getorRenewVAPISession here, defer to cache.refreshJWT
	return vcClient, nil
}

// createVAPISession creates a VAPI session using the specified STS signer and sets it on the vcClient.
func (vcClient *VCClient) createVAPISession() (string, error) {
	log.Info("creating new vapi session for vcClient")
	request, err := vcClient.prepareRequest(http.MethodPost, "com/vmware/cis/session", nil)
	if err != nil {
		return "", err
	}
	response, err := vcClient.httpClient.Do(request)
	if err != nil {
		return "", err
	}
	var sessionData map[string]string
	err, _ = util.HandleHTTPResponse(response, &sessionData, false)
	if err != nil {
		return "", err
	}

	session, ok := sessionData["value"]
	if !ok {
		msg := fmt.Sprintf("unexpected session data %v from vapi-endpoint", sessionData)
		err := errors.New(msg)
		log.Error(err, "failed to create VAPI session")
		return "", errors.New(msg)
	}
	return session, nil
}

// getorRenewVAPISession gets a new VAPI session for the vcClient.
func (vcClient *VCClient) getorRenewVAPISession() error {
	signer, err := vcClient.createHOKSigner()
	if err != nil {
		return err
	}
	vcClient.signer = signer
	session, err := vcClient.createVAPISession()
	if err != nil {
		return err
	}

	vcClient.sessionMutex.Lock()
	vcClient.session = session
	vcClient.sessionMutex.Unlock()
	return nil
}

func (vcClient *VCClient) reloadUsernamePass() error {
	f, err := ioutil.ReadFile(VC_SVCACCOUNT_USER_PATH)
	if err != nil {
		log.Error(err, "failed to read user name")
		return err
	}
	username := strings.TrimRight(string(f), "\n\r")

	f, err = ioutil.ReadFile(VC_SVCACCOUNT_PWD_PATH)
	if err != nil {
		log.Error(err, "failed to read password")
		return err
	}
	password := strings.TrimRight(string(f), "\n\r")
	tmppass, _ := vcClient.url.User.Password()
	if username != vcClient.url.User.Username() ||
		password != tmppass {
		log.Info("VC credentials updated")
		vcClient.url.User = url.UserPassword(username, password)
	} else {
		log.Info("VC credentials not changed")
	}
	return nil
}

// createHOKSigner creates a Hok token for the service account user.
func (vcClient *VCClient) createHOKSigner() (*sts.Signer, error) {
	log.V(1).Info("creating Holder of Key signer")
	userName := vcClient.url.User.Username()
	password, _ := vcClient.url.User.Password()
	client, err := vcClient.getorCreateSTSClient()
	if err != nil {
		return nil, err
	}

	cert, err := createCertificate(userName)
	if err != nil {
		log.Error(err, "failed to process service account keypair")
		return nil, err
	}

	req := sts.TokenRequest{
		Certificate: cert,
		Userinfo:    url.UserPassword(userName, password),
		Delegatable: true,
		Renewable:   true,
	}

	signed, err := client.Issue(context.Background(), req)
	if err != nil {
		log.Error(err, "failed to get token from cert,key pair")
		return nil, err
	}
	return signed, nil
}

// getorCreateSTSClient return a STS client of the vCenter. Creates a new client only if doesn't exist.
func (vcClient *VCClient) getorCreateSTSClient() (*sts.Client, error) {
	stsClientMutex.Lock()
	defer stsClientMutex.Unlock()

	if stsClient != nil {
		return stsClient, nil
	}

	vimSdkURL := fmt.Sprintf("https://%s/sdk", vcClient.url.Host)
	vimClient, err := vcClient.createVimClient(context.Background(), vimSdkURL)
	if err != nil {
		return nil, err
	}

	sc := vcClient.createSCClient(vimClient)
	return &sts.Client{Client: sc, RoundTripper: sc}, nil
}

func (vcClient *VCClient) createSCClient(vimClient *vim25.Client) *soap.Client {
	url := fmt.Sprintf("https://%s/sts/STSService/%s", vcClient.url.Host, vcClient.ssoDomain)
	return vimClient.Client.NewServiceClient(url, "oasis:names:tc:SAML:2.0:assertion")
}

func (vcClient *VCClient) createVimClient(ctx context.Context, vimSdkURL string) (*vim25.Client, error) {
	log.V(1).Info("creating vmomi client")
	vcURL, err := url.Parse(vimSdkURL)
	if err != nil {
		return nil, err
	}
	vimClient, err := vim25.NewClient(ctx, soap.NewClient(vcURL, true))
	if err != nil {
		log.Error(err, "failed to create VIM client", "vimSdkURL", vimSdkURL)
		return nil, err
	}
	return vimClient, nil
}

// HandleRequest sends a POST request
func (client *VCClient) HandleRequest(urlPath string, data []byte, responseData interface{}) error {
	request, err := client.prepareRequest(http.MethodPost, urlPath, data)
	if err != nil {
		return err
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return err
	}
	log.V(1).Info("HTTP request", "request", request.URL, "response status", response.StatusCode)
	err, _ = util.HandleHTTPResponse(response, responseData, false)
	return err
}

func (client *VCClient) prepareRequest(method string, urlPath string, data []byte) (*http.Request, error) {
	url := fmt.Sprintf("%s://%s/rest/%s", client.url.Scheme, client.url.Host, urlPath)
	req, err := http.NewRequest(method, url, bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if client.signer != nil {
		client.signer.SignRequest(req)
	} else if client.session != "" {
		req.Header.Set("vmware-api-session-id", client.session)
	} else {
		return nil, errors.New("invalid client - either session id or token should be set")
	}
	return req, nil
}

func createCertificate(userName string) (*tls.Certificate, error) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Error(err, "failed to generate RSA private key")
		return nil, err
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		log.Error(err, "failed to generate random serial number")
		return nil, err
	}
	currentTime := time.Now()
	notBeforeTime := currentTime.Add(-6 * time.Minute).UTC()
	notAfterTime := currentTime.Add(60 * time.Minute).UTC()
	log.V(1).Info("generating certificate", "user", userName, "notBefore", notBeforeTime, "notAfter", notAfterTime)
	certTemplate := x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               pkix.Name{CommonName: userName},
		Issuer:                pkix.Name{CommonName: userName},
		NotBefore:             notBeforeTime,
		NotAfter:              notAfterTime,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, &certTemplate, &certTemplate, &privKey.PublicKey, privKey)
	if err != nil {
		log.Error(err, "failed to generate certificate")
		return nil, err
	}

	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBytes})
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privKey)})
	certificate, err := tls.X509KeyPair([]byte(cert), []byte(privateKey))
	if err != nil {
		log.Error(err, "failed to process service account keypair")
		return nil, err
	}

	return &certificate, nil
}
