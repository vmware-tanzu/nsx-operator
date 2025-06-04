/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	policyclient "github.com/vmware/vsphere-automation-sdk-go/runtime/protocol/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/auth"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

// ClusterHealth indicates cluster status.
type ClusterHealth string

const (
	// RED means all endpoints status are DOWN.
	RED ClusterHealth = "RED"
	// ORANGE means not all endpoints status are UP.
	ORANGE ClusterHealth = "ORANGE"
	// GREEN means endpoints status are UP.
	GREEN ClusterHealth = "GREEN"
)

const (
	EnvoyUrlWithCert       = "http://%s:%d/external-cert/http1/%s"
	EnvoyUrlWithThumbprint = "http://%s:%d/external-tp/http1/%s/%s"
	LicenseAPI             = "api/v1/licenses/licensed-features"
)

const (
	maxNSXGetRetries = 10
	NSXGetDelay      = 2 * time.Second
)

// Cluster consists of endpoint and provides http.Client used to send http requests.
type Cluster struct {
	config           *Config
	endpoints        []*Endpoint
	transport        *Transport
	client           *http.Client
	noBalancerClient *http.Client
	sync.Mutex
}
type NsxVersion struct {
	NodeVersion string `json:"node_version"`
}

var (
	jarCache   = NewJar()
	nsxVersion = &NsxVersion{}
	log        = &logger.Log
)

// NewCluster creates a cluster based on nsx Config.
func NewCluster(config *Config) (*Cluster, error) {
	log.Info("Creating cluster")
	cluster := &Cluster{}
	cluster.config = config
	cluster.transport = cluster.createTransport(time.Duration(config.ConnIdleTimeout))
	cluster.client = cluster.createHTTPClient(cluster.transport, time.Duration(config.HTTPTimeout))
	cluster.noBalancerClient = cluster.createNoBalancerClient(time.Duration(config.HTTPTimeout), time.Duration(config.ConnIdleTimeout))

	r := ratelimiter.NewRateLimiter(config.APIRateMode)
	eps, err := cluster.createEndpoints(config.APIManagers, cluster.client, cluster.noBalancerClient, r, config.TokenProvider)
	if err != nil {
		log.Error(err, "Failed to create cluster")
		return nil, err
	}

	cluster.endpoints = eps
	cluster.transport.endpoints = eps
	cluster.transport.config = cluster.config
	cluster.loadCAforEnvoy()
	for _, ep := range cluster.endpoints {
		envoyUrl := cluster.CreateServerUrl(ep.Host(), ep.Scheme())
		ep.SetEnvoyUrl(envoyUrl)
	}
	cluster.createAuthSessions()
	for _, ep := range cluster.endpoints {
		ep.setUserPassword(config.Username, config.Password)
		ep.setup()
		if ep.Status() == UP {
			break
		}
	}
	for _, ep := range cluster.endpoints {
		go ep.KeepAlive()
	}

	return cluster, err
}

// Convert colon separated thumbprint to colon-free for envoy sidecar
func thumbprintToUrlPath(thumbprint string) string {
	return strings.ReplaceAll(strings.ToUpper(thumbprint), ":", "")
}

// loadCAforEnvoy should be called after endpoint is created
func (cluster *Cluster) loadCAforEnvoy() {
	if !cluster.UsingEnvoy() {
		return
	}
	for i, caFile := range cluster.config.CAFile {
		cert := util.CertPemBytesToHeader(caFile)
		if cert != "" {
			cluster.endpoints[i].caFile = cert
			log.Info("Load CA for envoy sidecar", "caFile", caFile)
			return
		} else {
			log.Info("Failed to load CA for envoy sidecar", "caFile", caFile)
		}
	}

	for i, thumbprint := range cluster.config.Thumbprint {
		cluster.endpoints[i].Thumbprint = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(thumbprint, ":", "")))
	}
}

func (cluster *Cluster) CreateServerUrl(host string, scheme string) string {
	serverUrl := ""
	if cluster.UsingEnvoy() {
		envoyUrl := ""
		index := strings.Index(host, ":")
		mgrIP := ""
		if index == -1 {
			log.Info("No port provided, use default port 443", "host", host)
			mgrIP = host + "/443"
		} else {
			mgrIP = strings.ReplaceAll(host, ":", "/")
		}

		cf := cluster.config
		if len(cf.CAFile) > 0 {
			envoyUrl = fmt.Sprintf(EnvoyUrlWithCert, cf.EnvoyHost, cf.EnvoyPort, mgrIP)
		} else if len(cf.Thumbprint) > 0 {
			thumbprint := thumbprintToUrlPath(cf.Thumbprint[0])
			envoyUrl = fmt.Sprintf(
				EnvoyUrlWithThumbprint, cf.EnvoyHost, cf.EnvoyPort, mgrIP, thumbprint)
		}
		serverUrl = envoyUrl
	} else {
		serverUrl = fmt.Sprintf("%s://%s", scheme, host)
	}
	log.V(1).Info("Create serverUrl", "serverUrl", serverUrl)
	return serverUrl
}

// NewRestConnector creates a RestConnector used for SDK client.
func (cluster *Cluster) NewRestConnector() policyclient.Connector {
	nsxtUrl := cluster.CreateServerUrl(cluster.endpoints[0].Host(), cluster.endpoints[0].Scheme())
	connector := policyclient.NewConnector(nsxtUrl, policyclient.UsingRest(nil), policyclient.WithHttpClient(cluster.client))
	connector.NewExecutionContext()
	return connector
}
func SetAllowOverwriteHeader(req *http.Request) error {
	// Set the header X-Allow-Overwrite to True
	req.Header.Set("X-Allow-Overwrite", "True")
	return nil
}
func (cluster *Cluster) NewRestConnectorAllowOverwrite() policyclient.Connector {
	nsxtUrl := cluster.CreateServerUrl(cluster.endpoints[0].Host(), cluster.endpoints[0].Scheme())
	policyclient.WithRequestProcessors()
	connector := policyclient.NewConnector(nsxtUrl, policyclient.UsingRest(nil), policyclient.WithHttpClient(cluster.client), policyclient.WithRequestProcessors(SetAllowOverwriteHeader))
	connector.NewExecutionContext()
	return connector
}

func (cluster *Cluster) UsingEnvoy() bool {
	return cluster.config.EnvoyPort != 0
}

func (cluster *Cluster) getThumbprint(addr string) string {
	host := addr[:strings.Index(addr, ":")]
	var thumbprint string
	tpCount := len(cluster.config.Thumbprint)
	if tpCount == 1 {
		thumbprint = cluster.config.Thumbprint[0]
	}
	if tpCount > 1 {
		for index, ep := range cluster.endpoints {
			epHost := ep.Host()
			if pos := strings.Index(ep.Host(), ":"); pos > 0 {
				epHost = epHost[:pos]
			}
			if epHost == host {
				thumbprint = cluster.config.Thumbprint[index]
				break
			}
		}
	}
	return thumbprint
}

func (cluster *Cluster) getCaFile(addr string) string {
	host := addr[:strings.Index(addr, ":")]
	var cafile string
	tpCount := len(cluster.config.CAFile)
	if tpCount == 1 {
		cafile = cluster.config.CAFile[0]
	}
	if tpCount > 1 {
		for index, ep := range cluster.endpoints {
			epHost := ep.Host()
			if pos := strings.Index(ep.Host(), ":"); pos > 0 {
				epHost = epHost[:pos]
			}
			if epHost == host {
				cafile = cluster.config.CAFile[index]
				break
			}
		}
	}
	return cafile
}

func (cluster *Cluster) createTransport(idle time.Duration) *Transport {
	tr := &http.Transport{
		IdleConnTimeout: idle * time.Second,
	}
	log.Info("Cluster envoy mode", "envoy mode", cluster.UsingEnvoy())
	if cluster.config.Insecure == false {
		dial := func(ctx context.Context, network, addr string) (net.Conn, error) { // #nosec G402: ignore insecure options
			var config *tls.Config
			cafile := cluster.getCaFile(addr)
			caCount := len(cluster.config.CAFile)
			log.Info("Create Transport", "ca file", cafile, "caCount", caCount)
			if caCount > 0 {
				caCert, err := os.ReadFile(cafile)
				if err != nil {
					log.Error(err, "Create transport", "read ca file", cafile)
					return nil, err
				}

				config, err = util.GetTLSConfigForCert(caCert)
				if err != nil {
					log.Error(err, "Create transport", "get TLS config from cert", cafile)
					return nil, err
				}
			} else {
				thumbprint := cluster.getThumbprint(addr)
				tpCount := len(cluster.config.Thumbprint)
				log.Info("Create Transport", "thumbprint", thumbprint, "tpCount", tpCount)
				// #nosec G402: ignore insecure options
				config = &tls.Config{
					InsecureSkipVerify: true,
					VerifyConnection: func(cs tls.ConnectionState) error {
						// not check thumbprint if no thumbprint config
						if tpCount > 0 {
							if err := util.VerifyNsxCertWithThumbprint(cs.PeerCertificates[0].Raw, thumbprint); err != nil {
								return err
							}
						}
						return nil
					},
				}
			}
			conn, err := tls.Dial(network, addr, config)
			if err != nil {
				log.Error(err, "Failed to do transport connect to", "addr", addr)
				return nil, err

			}
			return conn, nil
		}
		tr.DialTLSContext = dial
	} else {
		// #nosec G402: ignore insecure options
		tr.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}
	return &Transport{Base: tr}
}

func (cluster *Cluster) createHTTPClient(tr *Transport, timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: tr,
		Timeout:   timeout * time.Second,
	}
}

func (cluster *Cluster) createNoBalancerClient(timeout, idle time.Duration) *http.Client {
	// #nosec G402: ignore insecure options
	tlsConfig := tls.Config{InsecureSkipVerify: true}
	transport := &http.Transport{
		TLSClientConfig: &tlsConfig,
		IdleConnTimeout: idle * time.Second,
	}
	noBClient := http.Client{
		Transport: transport,
		Timeout:   timeout * time.Second,
	}
	return &noBClient
}

func (cluster *Cluster) createEndpoints(apiManagers []string, client *http.Client, noBClient *http.Client, r ratelimiter.RateLimiter, tokenProvider auth.TokenProvider) ([]*Endpoint, error) {
	eps := make([]*Endpoint, len(apiManagers))
	for i := range eps {
		ep, err := NewEndpoint(apiManagers[i], client, noBClient, r, tokenProvider)
		if err != nil {
			return nil, err
		}
		eps[i] = ep
	}
	return eps, nil
}

func (cluster *Cluster) createAuthSessions() {
	for _, ep := range cluster.endpoints {
		ep.createAuthSession(cluster.config.ClientCertProvider, cluster.config.TokenProvider, cluster.config.Username, cluster.config.Password, jarCache)
	}
}

// Health checks cluster health status.
func (cluster *Cluster) Health() ClusterHealth {
	down := 0
	up := 0
	for _, ep := range cluster.endpoints {
		if ep.Status() == UP {
			up++
		} else {
			down++
		}
	}

	if down == len(cluster.endpoints) {
		return RED
	}
	if up == len(cluster.endpoints) {
		return GREEN
	}
	return ORANGE
}

func (cluster *Cluster) GetVersion() (*NsxVersion, error) {
	ep := cluster.endpoints[0]
	serverUrl := cluster.CreateServerUrl(cluster.endpoints[0].Host(), cluster.endpoints[0].Scheme())
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/node/version", serverUrl), nil)
	if err != nil {
		log.Error(err, "Failed to create HTTP request")
		return nil, err
	}
	log.V(1).Info("Get version", "url", req.URL)
	err = ep.UpdateHttpRequestAuth(req)
	if err != nil {
		log.Error(err, "Failed to keep alive update auth")
		return nil, err
	}
	ep.UpdateCAforEnvoy(req)
	resp, err := ep.noBalancerClient.Do(req)
	if err != nil {
		log.Error(err, "Failed to get NSX version")
		return nil, err
	}
	err, _ = util.HandleHTTPResponse(resp, nsxVersion, true)
	return nsxVersion, err
}

// HttpGet sends a http GET request to the cluster, exported for use
func (cluster *Cluster) HttpGet(url string) (map[string]interface{}, error) {
	resp, err := cluster.httpAction(url, "GET")
	if err != nil {
		log.Error(err, "Failed to do HTTP GET operation")
		return nil, err
	}
	respJson := make(map[string]interface{})
	err, _ = util.HandleHTTPResponse(resp, &respJson, true)
	return respJson, err
}

func (cluster *Cluster) httpAction(url, method string) (*http.Response, error) {
	ep := cluster.endpoints[0]
	serverUrl := cluster.CreateServerUrl(cluster.endpoints[0].Host(), cluster.endpoints[0].Scheme())
	url = fmt.Sprintf("%s/%s", serverUrl, url)
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		log.Error(err, "Failed to create HTTP request")
		return nil, err
	}
	log.V(1).Info(method+" url", "url", req.URL)
	resp, err := ep.client.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// HttpDelete sends a http DELETE request to the cluster, exported for use
func (cluster *Cluster) HttpDelete(url string) error {
	_, err := cluster.httpAction(url, "DELETE")
	if err != nil {
		log.Error(err, "Failed to do HTTP DELETE operation")
		return err
	}
	return nil
}

func (nsxVersion *NsxVersion) Validate() error {
	re, _ := regexp.Compile(`^([\d]+).([\d]+).([\d]+)`)
	result := re.Find([]byte(nsxVersion.NodeVersion))
	if len(result) < 1 {
		err := errors.New("error version format")
		log.Error(err, "Failed to check NSX version", "version", nsxVersion.NodeVersion)
		return err
	}

	return nil
}

func (nsxVersion *NsxVersion) featureSupported(feature int) bool {
	var minVersion [3]int64
	validFeature := false
	switch feature {
	case VPC:
		minVersion = nsx411Version
		validFeature = true
	case SecurityPolicy:
		minVersion = nsx320Version
		validFeature = true
	case ServiceAccount:
		minVersion = nsx401Version
		validFeature = true
	case StaticRoute:
		minVersion = nsx401Version
		validFeature = true
	case ServiceAccountRestore:
		minVersion = nsx412Version
		validFeature = true
	case ServiceAccountCertRotation:
		minVersion = nsx413Version
		validFeature = true
	case VPCPreferredDefaultSNATIP:
		minVersion = nsx910Version
		validFeature = true
	}

	if validFeature {
		// only compared major.minor.patch
		// NodeVersion should have at least three sections
		// each section only have digital value
		buff := strings.Split(nsxVersion.NodeVersion, ".")
		sections := make([]int64, len(buff))
		for i, str := range buff {
			val, err := strconv.ParseInt(str, 10, 64)
			if err != nil {
				log.Error(err, "Failed to parse NSX version")
				return false
			}
			sections[i] = val
		}

		for i := 0; i < 3; i++ {
			if sections[i] > minVersion[i] {
				return true
			}
			if sections[i] < minVersion[i] {
				return false
			}
		}
		return true
	}
	return false
}

func (cluster *Cluster) FetchLicense() error {
	resp, err := cluster.httpAction(LicenseAPI, "GET")
	if err != nil {
		log.Error(err, "Failed to get NSX license")
		return err
	}
	nsxLicense := &util.NsxLicense{}
	err, _ = util.HandleHTTPResponse(resp, nsxLicense, true)
	if err != nil {
		return err
	}
	util.UpdateFeatureLicense(nsxLicense)
	return nil
}
