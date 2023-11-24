/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
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
	logf "sigs.k8s.io/controller-runtime/pkg/log"

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

// Cluster consists of endpoint and provides http.Client used to send http requests.
type Cluster struct {
	config           *Config
	endpoints        []*Endpoint
	transport        *Transport
	client           http.Client
	noBalancerClient http.Client
	sync.Mutex
}
type NsxVersion struct {
	NodeVersion string `json:"node_version"`
}

var (
	jarCache   = NewJar()
	nsxVersion = &NsxVersion{}
	log        = logf.Log.WithName("nsx").WithName("cluster")
)

// NewCluster creates a cluster based on nsx Config.
func NewCluster(config *Config) (*Cluster, error) {
	log.Info("creating cluster")
	cluster := &Cluster{}
	cluster.config = config
	cluster.transport = cluster.createTransport(time.Duration(config.ConnIdleTimeout))
	cluster.client = cluster.createHTTPClient(cluster.transport, time.Duration(config.HTTPTimeout))
	cluster.noBalancerClient = cluster.createNoBalancerClient(time.Duration(config.HTTPTimeout), time.Duration(config.ConnIdleTimeout))

	r := ratelimiter.NewRateLimiter(config.APIRateMode)
	eps, err := cluster.createEndpoints(config.APIManagers, &cluster.client, &cluster.noBalancerClient, r, config.TokenProvider)
	if err != nil {
		log.Error(err, "creating cluster failed")
		return nil, err
	}
	cluster.endpoints = eps
	cluster.transport.endpoints = eps
	cluster.transport.config = cluster.config
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

// NewRestConnector creates a RestConnector used for SDK client.
// HeaderConfig is used to use http header for request, it could be ignored if no extra header needed.
func (cluster *Cluster) NewRestConnector() (*policyclient.RestConnector, *HeaderConfig) {
	// host will be replaced by target endpoint's host when sending request to backend
	connector := policyclient.NewRestConnector(fmt.Sprintf("%s://%s", cluster.endpoints[0].Scheme(), cluster.endpoints[0].Host()), cluster.client)
	header := CreateHeaderConfig(false, false, cluster.config.AllowOverwriteHeader)
	return connector, header
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
	if cluster.config.Insecure == false {
		dial := func(network, addr string) (net.Conn, error) {
			var config *tls.Config
			cafile := cluster.getCaFile(addr)
			caCount := len(cluster.config.CAFile)
			if caCount > 0 {
				caCert, err := os.ReadFile(cafile)
				if err != nil {
					log.Error(err, "create transport", "read ca file", cafile)
					return nil, err
				}

				certPool := x509.NewCertPool()
				certPool.AppendCertsFromPEM(caCert)

				config = &tls.Config{
					RootCAs: certPool,
				}

			} else {
				thumbprint := cluster.getThumbprint(addr)
				tpCount := len(cluster.config.Thumbprint)
				config = &tls.Config{
					InsecureSkipVerify: true,
					VerifyConnection: func(cs tls.ConnectionState) error {
						// not check thumbprint if no thumbprint config
						if tpCount > 0 {
							fingerprint := calcFingerprint(cs.PeerCertificates[0].Raw)
							if strings.Compare(fingerprint, thumbprint) == 0 {
								return nil
							} else {
								err := errors.New("server certificate didn't match trusted fingerprint")
								log.Error(err, "verify thumbprint", "address", addr, "server thumbprint", fingerprint, "local thumbprint", thumbprint)
								return err
							}
						}
						return nil
					},
				}
			}
			conn, err := tls.Dial(network, addr, config)
			if err != nil {
				log.Error(err, "transport connect to", "addr", addr)
				return nil, err

			}
			return conn, nil
		}
		tr.DialTLS = dial
	} else {
		tr.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}
	return &Transport{Base: tr}
}

func calcFingerprint(der []byte) string {
	hash := sha1.Sum(der)
	hex := make([]byte, len(hash)*3)
	for i, data := range hash {
		buf := []byte(fmt.Sprintf("%02X", data))
		hex[i*3] = buf[0]
		hex[i*3+1] = buf[1]
		hex[i*3+2] = byte(':')
	}
	return string(hex[:len(hex)-1])
}

func (cluster *Cluster) createHTTPClient(tr *Transport, timeout time.Duration) http.Client {
	return http.Client{
		Transport: tr,
		Timeout:   timeout * time.Second,
	}
}

func (cluster *Cluster) createNoBalancerClient(timeout, idle time.Duration) http.Client {
	tlsConfig := tls.Config{InsecureSkipVerify: true}
	transport := &http.Transport{
		TLSClientConfig: &tlsConfig,
		IdleConnTimeout: idle * time.Second,
	}
	noBClient := http.Client{
		Transport: transport,
		Timeout:   timeout * time.Second,
	}
	return noBClient
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
	req, err := http.NewRequest("GET", fmt.Sprintf("%s://%s/api/v1/node/version", ep.Scheme(), ep.Host()), nil)
	if err != nil {
		log.Error(err, "failed to create http request")
		return nil, err
	}
	err = ep.UpdateHttpRequestAuth(req)
	if err != nil {
		log.Error(err, "keep alive update auth error")
		return nil, err
	}

	resp, err := ep.noBalancerClient.Do(req)
	if err != nil {
		log.Error(err, "failed to get nsx version")
		return nil, err
	}
	err, _ = util.HandleHTTPResponse(resp, nsxVersion, true)
	return nsxVersion, err
}

func (nsxVersion *NsxVersion) Validate() error {
	re, _ := regexp.Compile(`^([\d]+).([\d]+).([\d]+)`)
	result := re.Find([]byte(nsxVersion.NodeVersion))
	if len(result) < 1 {
		err := errors.New("error version format")
		log.Error(err, "check version", "version", nsxVersion.NodeVersion)
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
				log.Error(err, "parse version error")
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
