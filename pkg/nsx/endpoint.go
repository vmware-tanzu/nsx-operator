/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/auth"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

// EndpointStatus is endpoint status.
type EndpointStatus string

const (
	// UP means endpoint is available.
	UP EndpointStatus = "UP"
	// DOWN means endpoint is not available.
	DOWN EndpointStatus = "DOWN"
)

// Endpoint represents one nsx-t manager.
// It will run a go routine to check nsx-t manager status.
// It also maintains connection number to nsx-t manager.
type Endpoint struct {
	status           EndpointStatus
	client           *http.Client
	noBalancerClient *http.Client
	ratelimiter      ratelimiter.RateLimiter
	lastAliveTime    time.Time
	xXSRFToken       string
	keepaliveperiod  int
	connnumber       int32
	stop             chan bool
	printKeepAlive   int32
	// Used in keepAlive only when token is not avaiable.
	user          string
	password      string
	tokenProvider auth.TokenProvider
	sync.RWMutex
	provider
}

type provider interface {
	Host() string
	Scheme() string
}

type address struct {
	host   string
	scheme string
}

func (addr *address) Scheme() string {
	return addr.scheme
}
func (addr *address) Host() string {
	return addr.host
}

const (
	healthURL = "%s://%s/api/v1/reverse-proxy/node/health"
)

// NewEndpoint creates an endpoint.
func NewEndpoint(url string, client *http.Client, noBClient *http.Client, r ratelimiter.RateLimiter, tokenProvider auth.TokenProvider) (*Endpoint, error) {
	host, scheme, err := parseURL(url)
	if err != nil {
		return &Endpoint{}, err
	}
	addr := new(address)
	addr.host = host
	addr.scheme = scheme
	ep := Endpoint{client: client, noBalancerClient: noBClient, keepaliveperiod: ratelimiter.KeepAlivePeriod, ratelimiter: r, status: DOWN, tokenProvider: tokenProvider}
	ep.provider = addr
	ep.stop = make(chan bool)
	return &ep, nil
}

func parseURL(u string) (string, string, error) {
	// TODO: doesn't handle invalid url, check if needs to add valid.
	if !strings.HasPrefix(u, "http") {
		u = "https://" + u
	}
	ur, err := url.Parse(u)
	if err != nil {
		log.Error(err, "endpoint url format is invalid", "url", u)
		return "", "", err
	}
	return ur.Host, ur.Scheme, nil
}

type epHealthy struct {
	Healthy bool `json:"healthy"`
}

func (ep *Endpoint) keepAlive() error {
	req, err := http.NewRequest("GET", fmt.Sprintf(healthURL, ep.Scheme(), ep.Host()), nil)
	if ep.tokenProvider != nil {
		err = UpdateHttpRequestAuth(ep.tokenProvider, req)
		if err != nil {
			log.Error(err, "keepalive request creation failed")
			return err
		}
	} else {
		log.V(1).Info("no token provider, using user/password to keep alive")
		req.SetBasicAuth(ep.user, ep.password)
		req.Header.Add("X-Xsrf-Token", ep.XSRFToken())
		if err != nil {
			log.Error(err, "keepalive request creation failed")
			return err
		}
	}
	resp, err := ep.noBalancerClient.Do(req)
	if err != nil {
		log.Error(err, "failed to validate API cluster", "endpoint", ep.Host())
		return err
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Error(err, "failed to read response", "endpoint", ep.Host())
		return err
	}
	log.V(1).Info("received HTTP response", "response", string(body))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		var a epHealthy
		if err = json.Unmarshal(body, &a); err == nil && a.Healthy {
			ep.setStatus(UP)
			return nil
		}
		log.Error(err, "failed to validate API cluster", "endpoint", ep.Host(), "healthy", a)
		return err
	}
	resp.Body = ioutil.NopCloser(bytes.NewReader(body))
	err = util.InitErrorFromResponse(ep.Host(), resp)

	if util.ShouldRegenerate(err) {
		log.Error(err, "failed to validate API cluster due to an exception that calls for regeneration", "endpoint", ep.Host())
		// TODO, should we regenerate the token here ?
		ep.setXSRFToken("")
		ep.setStatus(DOWN)
		return err
	} else if util.ShouldRetry(err) {
		log.Info("error is retriable, endpoint stays up")
		ep.setStatus(UP)
	} else {
		ep.setStatus(DOWN)
	}
	log.Error(err, "failed to validate API cluster", "endpoint", ep.Host())
	return err
}

func (ep *Endpoint) nextInterval() int {
	t := time.Now()
	ep.Lock()
	i := t.Sub(ep.lastAliveTime)
	ep.Unlock()
	if int(i) > ep.keepaliveperiod {
		return ep.keepaliveperiod
	}
	return ep.keepaliveperiod - int(i)
}

// KeepAlive maintains a heart beat for each endpoint.
func (ep *Endpoint) KeepAlive() {
	for {
		ep.keepAlive()
		inter := ep.nextInterval()
		select {
		case <-ep.stop:
			log.Info("keepalive stopped by cluster")
			return
		case <-time.After(time.Second * time.Duration(inter)):
		}
	}
}

func (ep *Endpoint) setup() {
	log.V(1).Info("begin to setup endpoint")
	err := ep.keepAlive()
	if err != nil {
		log.Error(err, "setup endpoint failed")
	} else {
		log.V(1).Info("setup endpoint successfully")
	}
}

func (ep *Endpoint) setStatus(s EndpointStatus) {
	ep.Lock()
	if ep.status != s {
		log.Info("endpoint status is changing", "endpoing", ep.Host(), "oldStatus", ep.status, "newStatus", s)
		ep.status = s
	}
	ep.Unlock()
}

func (ep *Endpoint) setXSRFToken(token string) {
	ep.Lock()
	ep.xXSRFToken = token
	ep.Unlock()
}

// XSRFToken gets XsrfToken.
func (ep *Endpoint) XSRFToken() string {
	ep.RLock()
	defer ep.RUnlock()
	return ep.xXSRFToken
}

// Status return status of endpoiont.
func (ep *Endpoint) Status() EndpointStatus {
	ep.RLock()
	defer ep.RUnlock()
	return ep.status
}

func (ep *Endpoint) wait() {
	ep.ratelimiter.Wait()
}

func (ep *Endpoint) adjustRate(wait time.Duration, status int) {
	ep.ratelimiter.AdjustRate(wait, status)
}

func (ep *Endpoint) setAliveTime(time time.Time) {
	ep.Lock()
	ep.lastAliveTime = time
	ep.Unlock()
}

func (ep *Endpoint) setUserPassword(user, password string) {
	ep.Lock()
	ep.user = user
	ep.password = password
	ep.Unlock()
}

func (ep *Endpoint) increaseConnNumber() {
	atomic.AddInt32(&ep.connnumber, 1)
}

func (ep *Endpoint) decreaseConnNumber() {
	atomic.AddInt32(&ep.connnumber, -1)
}

// ConnNumber get the connection number of nsx-t.
func (ep *Endpoint) ConnNumber() int {
	return int(atomic.LoadInt32(&ep.connnumber))
}

func (ep *Endpoint) createAuthSession(certProvider auth.ClientCertProvider, tokenProvider auth.TokenProvider, username string, password string, jar *Jar) error {
	if certProvider != nil {
		log.V(1).Info("skipping session creation with client certificate auth")
		return nil
	}
	if tokenProvider != nil {
		_, err := tokenProvider.GetToken(true)
		if err != nil {
			log.Error(err, "failed to retrieve JSON Web Token for session creation", "endpoint", ep.Host())
			return err
		}
		log.V(1).Info("Skipping session create with JWT based auth")
		return nil
	}

	u := &url.URL{Host: ep.Host(), Scheme: ep.Scheme()}
	postValues := url.Values{}
	postValues.Add("j_username", username)
	postValues.Add("j_password", password)
	req, err := http.NewRequest("POST", fmt.Sprintf("%s://%s/api/session/create", u.Scheme, u.Host), strings.NewReader(postValues.Encode()))
	if err != nil {
		log.Error(err, "failed to generate request for session creation", "endpoint", ep.Host())
		return err
	}

	req.Header.Add("Accept", "application/json")
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	log.V(1).Info("creating auth session", "endpoint", ep, "request header", req.Header)
	resp, err := ep.noBalancerClient.Do(req)
	if err != nil {
		log.Error(err, "session creation failed", "endpoint", u.Host)
		return err
	}
	body, err := ioutil.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		err = fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}
	if err != nil {
		log.Error(err, "session creation failed", "endpoint", u.Host, "statusCode", resp.StatusCode, "headerDate", resp.Header["Date"], "body", body)
		return err
	}
	tokens, ok := resp.Header["X-Xsrf-Token"]
	if !ok {
		err = errors.New("no token in response")
		log.Error(err, "session creation failed", "endpoint", u.Host, "statusCode", resp.StatusCode, "headerDate", resp.Header["Date"], "body", body)
		return err
	}
	ep.setXSRFToken(tokens[0])
	jar.SetCookies(u, resp.Cookies())
	ep.Lock()
	ep.noBalancerClient.Jar = jar
	ep.client.Jar = jar
	ep.Unlock()
	ep.setStatus(UP)
	log.Info("session creation succeeded", "endpoint", u.Host)
	return nil
}

func UpdateHttpRequestAuth(tokenProvider auth.TokenProvider, request *http.Request) error {
	token, err := tokenProvider.GetToken(false)
	if err != nil {
		log.Error(err, "retrieving JSON Web Token eror")
		return err
	}
	bearerToken := tokenProvider.HeaderValue(token)
	request.Header.Add("Authorization", bearerToken)
	request.Header.Add("Accept", "application/json")
	request.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	return nil
}
