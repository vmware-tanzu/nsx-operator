/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/third_party/retry"
)

// Transport is used in http.Client to replace default implement.
// It selects the endpoint before sending HTTP reqeust and  it will retry the request based on HTTP response.
type Transport struct {
	Base      http.RoundTripper
	endpoints []*Endpoint
	config    *Config
}

// RoundTrip is the core of the transport. It accepts a request,
// replaces host with the URl provided by the endpoint.
// It will block the request if the speed is too fast.
// It will retry the request if nsx-t returns error and error type is retriable or ground
// It returns the response to the caller.
func (t *Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	var resp *http.Response
	var resul error

	retry.Do(
		func() error {
			ep, err := t.selectEndpoint()
			if err != nil {
				log.Error(err, "Endpoint is unavailable")
				return err
			}
			ep.increaseConnNumber()
			defer ep.decreaseConnNumber()

			util.UpdateRequestURL(r.URL, ep.Host(), ep.Thumbprint)
			ep.UpdateHttpRequestAuth(r)
			ep.UpdateCAforEnvoy(r)
			start := time.Now()
			ep.wait()
			util.DumpHttpRequest(r)
			waitTime := time.Since(start)
			if resp, resul = t.base().RoundTrip(r); resul != nil {
				ep.setStatus(DOWN)
				return handleRoundTripError(resul, ep)
			}
			transTime := time.Since(start) - waitTime
			ep.adjustRate(waitTime, resp.StatusCode)
			log.V(1).Info("RoundTrip request", "request", r.URL, "method", r.Method, "transTime", transTime)
			if resp == nil {
				return nil
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(body))

			if err != nil {
				log.Error(err, "Failed to extract HTTP body")
				return util.CreateGeneralManagerError(ep.Host(), "extract http", err.Error())
			}

			if err = util.InitErrorFromResponse(ep.Host(), resp.StatusCode, body); err == nil {
				ep.setAliveTime(start.Add(transTime))
				return nil
			}
			if util.ShouldRegenerate(err) {
				if t.config.TokenProvider != nil {
					t.config.TokenProvider.GetToken(true)
				} else {
					ep.createAuthSession(t.config.ClientCertProvider, t.config.TokenProvider, t.config.Username, t.config.Password, jarCache)
				}
			}
			return err
		}, retry.RetryIf(func(err error) bool {
			if util.ShouldGroundPoint(err) {
				return true
			} else if util.ShouldRetry(err) {
				return true
			} else {
				log.V(1).Info("error is configurated as not retriable", "error", err.Error())
				return false
			}
		}), retry.LastErrorOnly(true),
	)

	return resp, resul
}

func handleRoundTripError(err error, ep *Endpoint) error {
	log.Error(err, "request failed")
	errString := err.Error()
	if strings.HasSuffix(errString, "connection refused") {
		ep.setStatus(DOWN)
		return util.CreateConnectionError(ep.Host())
	} else if strings.HasSuffix(errString, "i/o timeout") {
		return util.CreateTimeout(ep.Host())
	} else {
		return util.CreateGeneralManagerError(ep.Host(), "RoundTrip", err.Error())
	}
}

func (t *Transport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

func (t *Transport) selectEndpoint() (*Endpoint, error) {
	small := 100
	index := -1
	for i, ep := range t.endpoints {
		if ep.Status() == DOWN {
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
		log.Error(errors.New("all endpoints down for cluster"), "select endpoint failed")
		id := strings.Join(eps, ",")
		return nil, util.CreateServiceClusterUnavailable(id)
	}
	return t.endpoints[index], nil
}
