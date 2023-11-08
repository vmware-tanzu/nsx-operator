/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/auth"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/third_party/retry"
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
	// Used when JWT token is not avaiable, default value is 120s
	lockWait      time.Duration
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
	ep.lockWait = 120 * time.Second
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
	if err != nil {
		log.Error(err, "create keep alive request error")
		return err
	}
	err = ep.UpdateHttpRequestAuth(req)
	if err != nil {
		log.Error(err, "keep alive update auth error")
		ep.setStatus(DOWN)
		return err
	}

	resp, err := ep.noBalancerClient.Do(req)
	if err != nil {
		log.Error(err, "failed to validate API cluster", "endpoint", ep.Host())
		return err
	}
	var a epHealthy
	err, body := util.HandleHTTPResponse(resp, &a, true)
	if err == nil && a.Healthy {
		ep.setStatus(UP)
		return nil
	}
	log.V(2).Info("keepAlive", "body", body)
	err = util.InitErrorFromResponse(ep.Host(), resp.StatusCode, body)
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
	log.V(2).Info("begin to setup endpoint")
	err := ep.keepAlive()
	if err != nil {
		log.Error(err, "setup endpoint failed")
	} else {
		log.Info("succeeded to setup endpoint")
	}
}

func (ep *Endpoint) setStatus(s EndpointStatus) {
	ep.Lock()
	if ep.status != s {
		log.Info("endpoint status is changing", "endpoint", ep.Host(), "oldStatus", ep.status, "newStatus", s)
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

// Status return status of endpoint.
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
		log.V(2).Info("skipping session creation with client certificate auth")
		return nil
	}
	if tokenProvider != nil {
		log.V(2).Info("Skipping session create with JWT based auth")
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

	log.V(2).Info("creating auth session", "endpoint", ep.Host(), "request header", req.Header)
	resp, err := ep.noBalancerClient.Do(req)
	if err != nil {
		log.Error(err, "session creation failed", "endpoint", u.Host)
		return err
	}
	body, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		err = fmt.Errorf("session creation failed, unexpected status code %d", resp.StatusCode)
	}
	if err != nil {
		log.Error(err, "session creation failed", "endpoint", u.Host, "statusCode", resp.StatusCode, "headerDate", resp.Header["Date"], "body", string(body))
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

func (ep *Endpoint) UpdateHttpRequestAuth(request *http.Request) error {
	// retry if GetToken failed, wait for 120s to avoid user lock
	// try 10 times
	if ep.tokenProvider != nil {
		var token string
		err := retry.Do(
			func() error {
				var err error
				token, err = ep.tokenProvider.GetToken(false)
				return err
			}, retry.RetryIf(func(err error) bool {
				return err != nil
			}), retry.LastErrorOnly(true), retry.Delay(ep.lockWait), retry.MaxDelay(ep.lockWait),
		)
		if err != nil {
			log.Error(err, "retrieving JSON Web Token eror")
			return err
		}
		bearerToken := ep.tokenProvider.HeaderValue(token)
		request.Header.Add("Authorization", bearerToken)
		request.Header.Add("Accept", "application/json")
	} else {
		xsrfToken := ep.XSRFToken()
		if len(xsrfToken) > 0 {
			log.V(2).Info("update cookie")
			if request.Header.Get("Authorization") != "" {
				request.Header.Del("Authorization")
			}
			request.Header.Add("X-Xsrf-Token", ep.XSRFToken())
			url := &url.URL{Host: ep.Host()}
			ep.Lock()
			cookies := ep.client.Jar.Cookies(url)
			ep.Unlock()
			for _, cookie := range cookies {
				if cookie == nil {
					log.Error(errors.New("cookie is nil"), "update authentication info failed")
				}
				request.Header.Set("Cookie", cookie.String())
			}
		} else {
			log.V(2).Info("update user/password")
			request.SetBasicAuth(ep.user, ep.password)
		}
	}
	return nil
}
