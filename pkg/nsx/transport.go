/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"bytes"
	"errors"
	"io"
	"math"
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

// RoundTrip implements the http.RoundTripper interface for the NSX client.
//
// Lifecycle and Control Flow:
//  1. Request Body Buffering: Backs up request body bytes for replay across retry attempts.
//  2. Attempt Budget: Max attempts is dynamic (len(endpoints) + 1) to cover failover
//     across all active NSX Manager endpoints plus one credential refresh attempt.
//  3. Endpoint Selection: Chooses the healthiest endpoint with the lowest connection count.
//     If all endpoints are down, clears any stale response and aborts immediately.
//  4. Request Execution Scope:
//     - Wraps single-attempt execution in a closure where `ep.increaseConnNumber()` is paired
//     with `defer ep.decreaseConnNumber()` to prevent connection count leaks under any error or panic.
//     - Updates authentication headers (JWT bearer or session/basic auth). If authentication
//     preparation fails, stops immediately without dispatching unauthenticated requests.
//     - Rewrites request URL to target endpoint host/thumbprint, applies rate limiting and logs.
//  5. Network Error Handling: Marks failing endpoints as DOWN upon network/timeout errors.
//     If the error satisfies `ShouldGroundPoint` (e.g. connection refused or timeout), continues
//     to fail over to the next healthy endpoint; otherwise breaks.
//  6. Response Processing:
//     - Drains and restores `resp.Body` with `io.NopCloser` to allow downstream consumption.
//     - Decodes NSX policy error models via `InitErrorFromResponse`.
//  7. Credential Regeneration (403 / InvalidCredentials / XSRF):
//     - If maximum attempts are exhausted, returns the final HTTP response per RoundTripper contract.
//     - If TokenProvider is configured and `GetToken(true)` fails, immediately aborts and surfaces
//     the root-cause token error rather than masking it with NSX 403.
//     - For password/session auth, attempts session re-creation and falls back to basic auth on error.
//  8. RoundTripper Contract Compliance:
//     - Non-retriable HTTP statuses (e.g., 200, 400, 404, 500) always return `(resp, nil)`.
//     - Stale responses from prior attempts are explicitly cleared (`lastResp = nil`) upon network
//     or endpoint selection failures, guaranteeing no zombie 403s are leaked on disconnects.
//     - Non-nil errors are strictly returned only when failing to obtain a valid HTTP response.
func (t *Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	log := t.getLogger()
	reqBodyBytes, hasBody := backupRequestBody(r, log)

	maxAttempts := len(t.endpoints) + 1

	var lastErr error
	var lastResp *http.Response

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if hasBody {
			r.Body = io.NopCloser(bytes.NewReader(reqBodyBytes))
		}

		ep, err := t.selectEndpoint()
		if err != nil {
			log.Error(err, "Endpoint is unavailable")
			lastErr, lastResp = err, nil
			break
		}

		if err := ep.UpdateHttpRequestAuth(r); err != nil {
			log.Error(err, "Failed to update request authentication", "endpoint", ep.Host())
			lastErr, lastResp = err, nil
			break
		}
		util.UpdateRequestURL(r.URL, ep.Host(), ep.Thumbprint)
		ep.UpdateCAforEnvoy(r)

		resp, waitTime, transTime, reqErr := t.sendRequest(ep, r, log)
		if reqErr != nil {
			lastErr, lastResp = handleRoundTripError(reqErr, ep), nil
			if util.ShouldGroundPoint(lastErr) {
				ep.setStatus(DOWN)
				log.Debug("Grounding endpoint and retrying on next available endpoint", "endpoint", ep.Host(), "attempt", attempt, "maxAttempts", maxAttempts, "error", lastErr)
				continue
			}
			log.Debug("RoundTrip network error is not groundable", "endpoint", ep.Host(), "error", lastErr)
			break
		}
		if resp == nil {
			log.Error(errors.New("nil response from round trip"), "Failed to obtain response from endpoint", "endpoint", ep.Host())
			lastErr, lastResp = errors.New("empty response received from transport"), nil
			break
		}

		ep.adjustRate(waitTime, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Error(err, "Failed to extract HTTP body")
			lastErr, lastResp = util.CreateGeneralManagerError(ep.Host(), "extract http", err.Error()), nil
			break
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
		lastResp = resp

		nsxErr := util.InitErrorFromResponse(ep.Host(), resp.StatusCode, body, log)
		if nsxErr == nil {
			ep.setAliveTime(time.Now().Add(transTime))
			return resp, nil
		}

		if util.ShouldRegenerate(nsxErr) {
			if attempt == maxAttempts-1 {
				log.Debug("Max attempts reached for credential regeneration, returning response to caller", "endpoint", ep.Host(), "statusCode", resp.StatusCode)
				// Reached max attempts, return final response per RoundTripper contract.
				return resp, nil
			}
			log.Debug("Authentication error received, regenerating credentials", "endpoint", ep.Host(), "attempt", attempt, "error", nsxErr)
			if err := t.regenerateAuth(ep); err != nil {
				log.Error(err, "Failed to regenerate authentication credentials", "endpoint", ep.Host())
				// Return the original HTTP response (e.g., 403 Forbidden) to the caller
				// so the business layer can properly interpret the authentication failure.
				return resp, nil
			}
			lastErr = nsxErr
			continue
		}

		// Non-retriable HTTP response (e.g., 400, 404, 500) must return (resp, nil)
		// per http.RoundTripper contract so caller can process the status and body.
		return resp, nil
	}

	if lastResp != nil {
		return lastResp, nil
	}

	if lastErr == nil {
		lastErr = errors.New("request failed without response")
	}
	log.Debug("RoundTrip failed without response", "error", lastErr)
	return nil, lastErr
}

// sendRequest executes the HTTP request against the chosen endpoint with connection tracking and rate limiting.
func (t *Transport) sendRequest(ep *Endpoint, r *http.Request, log logger.CustomLogger) (*http.Response, time.Duration, time.Duration, error) {
	ep.increaseConnNumber()
	defer ep.decreaseConnNumber()

	start := time.Now()
	ep.wait()
	util.DumpHttpRequest(r, log)
	waitTime := time.Since(start)

	resp, err := t.base().RoundTrip(r)
	transTime := time.Since(start) - waitTime
	return resp, waitTime, transTime, err
}

// regenerateAuth handles token refresh or session recreation upon credential invalidation.
func (t *Transport) regenerateAuth(ep *Endpoint) error {
	if t.config == nil {
		return errors.New("cannot regenerate credentials: configuration is nil")
	}
	if t.config.TokenProvider != nil {
		_, err := t.config.TokenProvider.GetToken(true)
		return err
	}
	if t.config.Username != "" {
		return ep.createAuthSession(t.config.ClientCertProvider, t.config.TokenProvider, t.config.Username, t.config.Password, jarCache)
	}
	return nil
}

func backupRequestBody(r *http.Request, log logger.CustomLogger) ([]byte, bool) {
	if r == nil || r.Body == nil {
		return nil, false
	}
	bodyBytes, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		log.Error(err, "Failed to read request body for backup")
		return nil, false
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	return bodyBytes, true
}

func handleRoundTripError(err error, ep *Endpoint) error {
	host := ""
	if ep != nil {
		host = ep.Host()
	}
	errString := err.Error()
	if strings.HasSuffix(errString, "connection refused") {
		return util.CreateConnectionError(host)
	} else if strings.HasSuffix(errString, "i/o timeout") {
		return util.CreateTimeout(host)
	} else {
		return util.CreateGeneralManagerError(host, "RoundTrip", err.Error())
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
		if ep == nil || ep.Status() == DOWN {
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
			if i != nil {
				eps = append(eps, i.Host())
			}
		}
		id := strings.Join(eps, ",")
		return nil, util.CreateServiceClusterUnavailable(id)
	}
	return t.endpoints[index], nil
}
