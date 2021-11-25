/* Copyright © 2020 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/auth"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/third_party/retry"
)

// Transport is used in http.Client to replace default implement.
// It selects the endpoint before sending HTTP reqeust and  it will retry the request based on HTTP response.
type Transport struct {
	Base          http.RoundTripper
	endpoints     []*Endpoint
	tokenProvider auth.TokenProvider
	cluster       *Cluster
}

// RoundTrip is the core of the transport. It accepts a request,
// replaces host with the URl provided by the endpoint.
// It will block the request if the speed is too fast.
// It will retry the request if nsx-t returns error and error type is retriable or ground
// It returns the response to the caller.
func (t *Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	var resp *http.Response
	var resul error

	err1 := retry.Do(
		func() error {
			ep, err := t.selectEndpoint()
			if err != nil {
				log.Error()
				return err
			}
			ep.increaseConnNumber()
			defer ep.decreaseConnNumber()

			r.URL.Host = ep.Host()
			t.updateAuthInfo(r, ep)
			start := time.Now()
			ep.wait()
			waitTime := time.Since(start)
			if resp, resul = t.base().RoundTrip(r); resul != nil {
				ep.setStatus(DOWN)
				return handleRoundTripError(resul, ep)
			}
			transTime := time.Since(start) - waitTime
			ep.adjustRate(waitTime, resp.StatusCode)
			log.Debug(fmt.Sprintf("HTTP request: %v, response: %v, Took: %s", r, resp, transTime))
			if err = util.InitErrorFromResponse(ep.Host(), resp); err == nil {
				ep.setAliveTime(start.Add(transTime))
				return nil
			}
			log.Debug(fmt.Sprintf("Request failed due to: %v", err))

			// refresh token here
			if util.ShouldRegenerate(err) {
				t.cluster.createAuthSession(ep)
			}
			return err
		}, retry.RetryIf(func(err error) bool {
			if util.ShouldGroundPoint(err) {
				return true
			} else if util.ShouldRetry(err) {
				return true
			} else {
				log.Debug(fmt.Sprintf("Error [%v] is configrated as not retriable", err))
				return false
			}
		}), retry.LastErrorOnly(true),
	)

	return resp, err1
}

func handleRoundTripError(err error, ep *Endpoint) error {
	log.Warning(fmt.Sprintf("Request failed due to: %v", err))
	errString := err.Error()
	if strings.HasSuffix(errString, "connection refused") {
		ep.setStatus(DOWN)
		return util.CreateConnectionError(ep.Host())
	} else if strings.HasSuffix(errString, "i/o timeout") {
		return util.CreateTimeout(ep.Host())
	} else {
		return util.CreateManagerError(ep.Host(), "RoundTrip", err.Error())
	}
}
func (t *Transport) updateAuthInfo(r *http.Request, ep *Endpoint) {
	if ep.XSRFToken() != "" {
		if r.Header.Get("Authorization") != "" {
			r.Header.Del("Authorization")
		}
		r.Header.Add("X-Xsrf-Token", ep.XSRFToken())
		url := &url.URL{Host: ep.Host()}
		ep.Lock()
		cookies := ep.client.Jar.Cookies(url)
		ep.Unlock()
		for _, cookie := range cookies {
			if cookie == nil {
				log.Warning("Cookie is nil.")
			}
			r.Header.Set("Cookie", cookie.String())
		}
	} else {
		if t.tokenProvider != nil {
			token, err := t.tokenProvider.GetToken(false)
			if err != nil {
				log.Error(fmt.Sprintf("Update authentication info failed for endpoint %s due to error in retrieving JSON Web Token: %s", ep.Host(), err))
				return
			}
			bearerToken := t.tokenProvider.HeaderValue(token)
			r.Header.Add("Authorization", bearerToken)
		}
	}
}

func (t *Transport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

func (t *Transport) setEndpoints(eps []*Endpoint) {
	t.endpoints = eps
}

func (t *Transport) setCluster(cluster *Cluster) {
	t.cluster = cluster
}

func (t *Transport) selectEndpoint() (*Endpoint, error) {
	small := 100
	index := -1
	for i, ep := range t.endpoints {
		status := ep.Status()
		if status == DOWN {
			continue
		}
		conn := ep.ConnNumber()
		if conn < small {
			small = conn
			index = i
		}
	}
	if index == -1 {
		var eps []string
		for _, i := range t.endpoints {
			eps = append(eps, i.Host())
		}
		log.Warning(fmt.Sprintf("All endpoints down for cluster: %v", eps))
		id := strings.Join(eps, ",")
		return nil, util.CreateServiceClusterUnavailable(id)
	}
	return t.endpoints[index], nil
}
