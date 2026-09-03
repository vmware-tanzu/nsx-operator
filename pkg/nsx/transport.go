/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"bytes"
	"errors"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

// Transport is used in http.Client to replace default implement.
// It selects the endpoint before sending HTTP reqeust and  it will retry the request based on HTTP response.
type Transport struct {
	Base      http.RoundTripper
	endpoints []*Endpoint
	config    *Config
}

func (t *Transport) getLogger() logger.CustomLogger {
	if t != nil && t.config != nil {
		return t.config.Logger.Fallback()
	}
	return logger.Log
}

// RoundTrip is the core of the transport. It accepts a request,
// replaces host with the URl provided by the endpoint.
// It will block the request if the speed is too fast.
// It will retry the request if nsx-t returns error and error type is ground or regenerate
// It returns the response to the caller.
func (t *Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	var resp *http.Response
	var resul error
	log := t.getLogger()

	reqBodyBytes, hasBody := backupRequestBody(r)

	var retryErr error
	maxAttempts := 10

	for attempt := 0; attempt < maxAttempts; attempt++ {
		retryErr = func() error {
			if hasBody {
				r.Body = io.NopCloser(bytes.NewReader(reqBodyBytes))
			}
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
			util.DumpHttpRequest(r, log)
			waitTime := time.Since(start)
			if resp, resul = t.base().RoundTrip(r); resul != nil {
				ep.setStatus(DOWN)
				return handleRoundTripError(resul, ep)
			}
			transTime := time.Since(start) - waitTime
			ep.adjustRate(waitTime, resp.StatusCode)
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

			if err = util.InitErrorFromResponse(ep.Host(), resp.StatusCode, body, log); err == nil {
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
		}()

		if retryErr == nil {
			break
		}

		if util.ShouldGroundPoint(retryErr) {
			backoffWithJitter(attempt)
			continue
		}
		if util.ShouldRegenerate(retryErr) {
			continue
		}
		log.Debug("Error is configured as not retriable in transport layer", "error", retryErr.Error())
		break
	}

	return resp, resul
}

func backupRequestBody(r *http.Request) ([]byte, bool) {
	if r == nil || r.Body == nil {
		return nil, false
	}
	bodyBytes, _ := io.ReadAll(r.Body)
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	return bodyBytes, true
}

func handleRoundTripError(err error, ep *Endpoint) error {
	ep.logger.Fallback().Error(err, "Failed to request")
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
	small := math.MaxInt32
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
		t.getLogger().Error(errors.New("all endpoints down for cluster"), "select endpoint failed")
		id := strings.Join(eps, ",")
		return nil, util.CreateServiceClusterUnavailable(id)
	}
	return t.endpoints[index], nil
}

func backoffWithJitter(attempt int) {
	// Exponential backoff with jitter
	// Base delay: 100ms, 200ms, 400ms, 800ms...
	baseDelay := 100 * time.Millisecond * time.Duration(1<<attempt)
	if baseDelay > 10*time.Second {
		baseDelay = 10 * time.Second
	}
	// Jitter: 0-50ms
	jitter := time.Duration(rand.Int63n(int64(50 * time.Millisecond))) //nolint:gosec // weak random is acceptable for jitter
	time.Sleep(baseDelay + jitter)
}
