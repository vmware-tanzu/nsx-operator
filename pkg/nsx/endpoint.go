/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
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
	user     string
	password string
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
func NewEndpoint(url string, client *http.Client, noBClient *http.Client, r ratelimiter.RateLimiter) (*Endpoint, error) {
	host, scheme, err := parseURL(url)
	if err != nil {
		return &Endpoint{}, err
	}
	addr := new(address)
	addr.host = host
	addr.scheme = scheme
	ep := Endpoint{client: client, noBalancerClient: noBClient, keepaliveperiod: ratelimiter.KeepAlivePeriod, ratelimiter: r, status: DOWN}
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
		log.Error(fmt.Sprintf("Endpoint url %v format error %s", u, err))
		return "", "", err
	}
	return ur.Host, ur.Scheme, nil
}

type epHealthy struct {
	Healthy bool `json:"healthy"`
}

func (ep *Endpoint) keepAlive() error {
	var req *http.Request
	var err error
	if ep.XSRFToken() != "" {
		req, err = http.NewRequest("GET", fmt.Sprintf(healthURL, ep.Scheme(), ep.Host()), nil)
		if err != nil {
			log.Warning("KeepAlive create request error :", err)
			return err
		}
		req.Header.Add("X-Xsrf-Token", ep.xXSRFToken)
		u := &url.URL{Host: ep.Host()}
		ep.RLock()
		for _, cookie := range ep.client.Jar.Cookies(u) {
			if cookie == nil {
				log.Warning("Cookie is nil")
			} else {
				req.Header.Set("Cookie", cookie.String())
			}
		}
		ep.RUnlock()
	} else {
		log.Debug("Token is invalid, using user/password to keep alive")
		req, err = http.NewRequest("GET", fmt.Sprintf(healthURL, ep.Scheme(), ep.Host()), nil)
		req.SetBasicAuth(ep.user, ep.password)
		if err != nil {
			log.Warning("KeepAlive create request error :", err)
			return err
		}
	}
	resp, err := ep.noBalancerClient.Do(req)
	if err != nil {
		log.Warning(fmt.Sprintf("Failed to validate API cluster endpoint %s due to: %v", ep.Host(), err))
		return err
	}
	body, err := ioutil.ReadAll(resp.Body)
	log.Debug(fmt.Sprintf("http request is %v , resp body is %s", req, string(body)))
	defer resp.Body.Close()
	if err = util.InitErrorFromResponse(ep.Host(), resp); err == nil {
		var a epHealthy
		if err = json.Unmarshal(body, &a); err == nil && a.Healthy == true {
			ep.setStatus(UP)
			return nil
		}
		log.Warning(fmt.Sprintf("Failed to validate API cluster endpoint %s due to: %v, %v", ep.Host(), err, a))
		return err
	}

	if util.ShouldRegenerate(err) {
		log.Warning(fmt.Sprintf("Failed to validate API cluster endpoint %s due to an exception that calls for regeneration", ep.Host()))
		// TODO, should we regenerate the token here ?
		ep.setXSRFToken("")
		ep.setStatus(DOWN)
		return err
	} else if util.ShouldRetry(err) {
		log.Info("Error is retriable, endpoint stays up")
		ep.setStatus(UP)
	} else {
		ep.setStatus(DOWN)
	}
	log.Warning(fmt.Sprintf("Failed to validate API cluster endpoint %s due to: %s", ep.Host(), err))
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
			log.Info("KeepAlive stopped by cluster")
			return
		case <-time.After(time.Second * time.Duration(inter)):
		}
	}
}

func (ep *Endpoint) setup() {
	log.Debug("Begin to setup endpoint")
	err := ep.keepAlive()
	if err != nil {
		log.Warning("Fail to setup endpoint: ", err)
	} else {
		log.Debug("successfully to setup endpoint: ")
	}
}

func (ep *Endpoint) setStatus(s EndpointStatus) {
	ep.Lock()
	if ep.status != s {
		log.Info(fmt.Sprintf("Endpoint %s changing from state %s to %s", ep.Host(), ep.status, s))
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
		log.Debug("Skipping session create with client certificate auth")
		return nil
	}
	u := &url.URL{Host: ep.Host(), Scheme: ep.Scheme()}
	var req *http.Request
	var err error
	if tokenProvider != nil {
		token, err := tokenProvider.GetToken(true)
		if err != nil {
			log.Error(fmt.Sprintf("Session create failed for endpoint %s due to error in retrieving JSON Web Token: %s", ep.Host(), err))
			return err
		}
		req, err = http.NewRequest("POST", fmt.Sprintf("%s://%s/api/session/create", u.Scheme, u.Host), nil)
		if err != nil {
			log.Error(fmt.Sprintf("Session create for %s failed due to creating request error : %s", ep.Host(), err))
			return err
		}
		bearerToken := tokenProvider.HeaderValue(token)
		req.Header.Add("Authorization", bearerToken)
	} else {
		postValues := url.Values{}
		postValues.Add("j_username", username)
		postValues.Add("j_password", password)
		req, err = http.NewRequest("POST", fmt.Sprintf("%s://%s/api/session/create", u.Scheme, u.Host), strings.NewReader(postValues.Encode()))
		if err != nil {
			log.Error(fmt.Sprintf("Session create for %s failed due to creating request error : %s", ep.Host(), err))
			return err
		}
	}

	req.Header.Add("Accept", "application/json")
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	log.Debug(fmt.Sprintf("createAuth session: ep is %v, req is %v", ep, req))
	resp, err := ep.noBalancerClient.Do(req)
	if err != nil {
		log.Warning(fmt.Sprintf("Session created failed for endpoint %s with error : %s", u.Host, err))
		return err
	}
	body, err := ioutil.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		log.Warning(fmt.Sprintf("Session created failed for endpoint %s with response %d, error message: %s, local NSX time: %s", u.Host, resp.StatusCode, err, resp.Header["Date"]))
		return err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		log.Warning(fmt.Sprintf("Session created failed for endpoint %s with response %d, resp body: %s, local NSX time: %s", u.Host, resp.StatusCode, body, resp.Header["Date"]))
		return fmt.Errorf("Session create failed for response error %d", resp.StatusCode)
	}
	tokens, ok := resp.Header["X-Xsrf-Token"]
	if !ok {
		log.Warning(fmt.Sprintf("Session created failed for endpoint %s body has no token, body : %s, local NSX time: %s", u.Host, body, resp.Header["Date"]))
		return errors.New("Session create failed for response body no token")
	}
	ep.setXSRFToken(tokens[0])
	jar.SetCookies(u, resp.Cookies())
	ep.Lock()
	ep.noBalancerClient.Jar = jar
	ep.client.Jar = jar
	ep.Unlock()
	ep.setStatus(UP)
	log.Info("Session create succeeded for endpoint ", u.Host)
	return nil
}
