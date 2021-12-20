/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/security"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/auth"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/core"
	policyclient "github.com/vmware/vsphere-automation-sdk-go/runtime/protocol/client"
)

// ClusterHealth indicates cluster status.
type ClusterHealth int32

const (
	// RED means all endpoints status are DOWN.
	RED ClusterHealth = iota
	// ORANGE means not all endpoints status are UP.
	ORANGE
	// GREEN means endpoints status are UP.
	GREEN
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

var (
	jarCache = NewJar()
	log      = logf.Log.WithName("nsx").WithName("cluster")
)

// NewCluster creates a cluster based on nsx Config.
func NewCluster(config *Config) (*Cluster, error) {
	log.Info("creating cluster")
	cluster := &Cluster{}
	cluster.config = config
	cluster.transport = cluster.createTransport(config.TokenProvider, time.Duration(config.ConnIdleTimeout))
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

func (cluster *Cluster) createTransport(tokenProvider auth.TokenProvider, idle time.Duration) *Transport {
	// TODO: support the case if InsecureSkipVerify is false
	tlsConfig := tls.Config{InsecureSkipVerify: true}
	tlsConfig.BuildNameToCertificate()
	tr := &http.Transport{
		TLSClientConfig: &tlsConfig,
		IdleConnTimeout: idle * time.Second,
	}
	return &Transport{Base: tr, tokenProvider: tokenProvider}
}
func (cluster *Cluster) createHTTPClient(tr *Transport, timeout time.Duration) http.Client {
	return http.Client{
		Transport: tr,
		Timeout:   timeout * time.Second,
	}
}

func (cluster *Cluster) createNoBalancerClient(timeout, idle time.Duration) http.Client {
	tlsConfig := tls.Config{InsecureSkipVerify: true}
	tlsConfig.BuildNameToCertificate()
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

func (cluster *Cluster) createSecurity(user string, password string) core.SecurityContext {
	securityCtx := core.NewSecurityContextImpl()
	securityCtx.SetProperty(security.AUTHENTICATION_SCHEME_ID, security.USER_PASSWORD_SCHEME_ID)
	securityCtx.SetProperty(security.USER_KEY, user)
	securityCtx.SetProperty(security.PASSWORD_KEY, password)
	return securityCtx
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
		if ep.status == UP {
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
