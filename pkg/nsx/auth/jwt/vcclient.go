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
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/vmware/govmomi/sts"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/soap"
)

// VcClient tracks a client session token.
type VcClient struct {
	baseURL      string
	hostName     string
	port         int
	httpClient   *http.Client
	userName     string
	password     string
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
		clientCertPool := x509.NewCertPool()
		clientCertPool.AppendCertsFromPEM(caCertPem)
		tlsConfig.RootCAs = clientCertPool
	}
	return &http.Client{Transport: transport, Timeout: time.Minute}
}

// NewVcClient creates a new logged in VC client with vapi session.
func NewVcClient(hostname string, port int, ssoDomain string, userName, password string, caCertPem []byte, insecureSkipVerify bool) (*VcClient, error) {
	httpClient := createHttpClient(insecureSkipVerify, caCertPem)
	vcClient := &VcClient{
		baseURL:    fmt.Sprintf("https://%s:%d/rest", hostname, port),
		hostName:   hostname,
		port:       port,
		httpClient: httpClient,
		userName:   userName,
		password:   password,
		ssoDomain:  ssoDomain,
	}

	err := vcClient.getorRenewVAPISession()
	if err != nil {
		return nil, err
	}
	return vcClient, nil
}

// createVAPISession creates a VAPI session using the specified STS signer and sets it on the vcClient.
func (vcClient *VcClient) createVAPISession() (string, error) {
	log.Info("Creating new vapi session for vcClient")
	request, err := vcClient.prepareRequest(http.MethodPost, "/com/vmware/cis/session", nil)
	if err != nil {
		return "", err
	}
	response, err := vcClient.httpClient.Do(request)
	var sessionData map[string]string
	err = handleHTTPResponse(response, &sessionData)
	if err != nil {
		return "", err
	}

	session, ok := sessionData["value"]
	if !ok {
		msg := fmt.Sprintf("unexpected session data %v from vapi-endpoint", sessionData)
		err := errors.New(msg)
		log.Error(err, "Failed to create VAPI session")
		return "", errors.New(msg)
	}
	return session, nil
}

// getorRenewVAPISession gets a new VAPI session for the vcClient.
func (vcClient *VcClient) getorRenewVAPISession() error {
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

// createHOKSigner creates a Hok token for the service account user.
func (vcClient *VcClient) createHOKSigner() (*sts.Signer, error) {
	log.V(4).Info("Creating Holder of Key signer")
	userName, password := vcClient.userName, vcClient.password

	client, err := vcClient.getorCreateSTSClient()
	if err != nil {
		return nil, err
	}

	cert, err := createCertificate(userName)
	if err != nil {
		log.Error(err, "Failed to process service account keypair")
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
		log.Error(err, "Failed to get token from cert,key pair")
		return nil, err
	}
	return signed, nil
}

// getorCreateSTSClient return a STS client of the vCenter. Creates a new client only if doesn't exist.
func (vcClient *VcClient) getorCreateSTSClient() (*sts.Client, error) {
	stsClientMutex.Lock()
	defer stsClientMutex.Unlock()

	if stsClient != nil {
		return stsClient, nil
	}

	vimSdkURL := fmt.Sprintf("https://%s:%d/sdk", vcClient.hostName, vcClient.port)
	vimClient, err := vcClient.createVimClient(context.Background(), vimSdkURL)
	if err != nil {
		return nil, err
	}

	sc := vcClient.createSCClient(vimClient)
	return &sts.Client{Client: sc, RoundTripper: sc}, nil
}

func (vcClient *VcClient) createSCClient(vimClient *vim25.Client) *soap.Client {
	url := fmt.Sprintf("https://%s:%d/sts/STSService/%s", vcClient.hostName, vcClient.port, vcClient.ssoDomain)
	return vimClient.Client.NewServiceClient(url, "oasis:names:tc:SAML:2.0:assertion")
}

func (vcClient *VcClient) createVimClient(ctx context.Context, vimSdkURL string) (*vim25.Client, error) {
	log.V(4).Info("Creating vmomi client")
	vcURL, err := url.Parse(vimSdkURL)
	if err != nil {
		return nil, err
	}
	vimClient, err := vim25.NewClient(ctx, soap.NewClient(vcURL, true))
	if err != nil {
		log.Error(err, "Failed to create VIM client", "vimSdkURL", vimSdkURL)
		return nil, err
	}
	return vimClient, nil
}

// HandleRequest sends a POST request
func (client *VcClient) HandleRequest(urlPath string, data []byte, responseData interface{}) error {
	// reew vAPISession if status code is '401'
	for i := 0; i < 3; i++ {
		request, err := client.prepareRequest(http.MethodPost, urlPath, data)
		if err != nil {
			return err
		}

		response, err := client.httpClient.Do(request)
		if err != nil {
			return err
		}
		log.V(4).Info("HTTP request: %v, response %v", request, response)
		if response.StatusCode == http.StatusUnauthorized {
			if err = client.getorRenewVAPISession(); err != nil {
				log.Error(err, "Failed to renew VAPI session")
				return err
			}
			continue
		}
		err = handleHTTPResponse(response, responseData)
		return err
	}
	return nil
}

func (client *VcClient) prepareRequest(method string, urlPath string, data []byte) (*http.Request, error) {
	url := client.baseURL + urlPath
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
		log.Error(err, "Failed to generate RSA private key")
		return nil, err
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		log.Error(err, "Failed to generate random serial number")
		return nil, err
	}
	currentTime := time.Now()
	notBeforeTime := currentTime.Add(-6 * time.Minute).UTC()
	notAfterTime := currentTime.Add(60 * time.Minute).UTC()
	log.V(4).Info("Generating certificate", "user", userName, "notBefore", notBeforeTime, "notAfter", notAfterTime)
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
		log.Error(err, "Failed to generate certificate")
		return nil, err
	}

	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBytes})
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privKey)})
	certificate, err := tls.X509KeyPair([]byte(cert), []byte(privateKey))
	if err != nil {
		log.Error(err, "Failed to process service account keypair")
		return nil, err
	}

	return &certificate, nil
}

func handleHTTPResponse(response *http.Response, result interface{}) error {
	if response.StatusCode >= 300 {
		err := errors.New("Received HTTP Error")
		log.Error(err, "Handle http response", "status", response.StatusCode, "requestUrl", response.Request.URL, "response", response)
		return err
	}
	if result == nil {
		return nil
	}

	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(body, result); err != nil {
		log.Error(err, "Error converting HTTP response to result", "result type", result)
		return err
	}
	log.V(4).Info("Response body", result)

	return nil
}
