// Copyright © 2019-2021 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package clients

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	sessionURLPath = "/api/session"
)

// createHTTPClient creates an HTTP client with TLS configuration
func createHTTPClient() *http.Client {
	tlsConfig := &tls.Config{InsecureSkipVerify: true} // #nosec G402: ignore insecure options
	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}
	return &http.Client{Transport: transport, Timeout: time.Minute}
}

// StartSession starts a new session with vCenter
func (c *VCClient) StartSession() error {
	c.sessionMutex.Lock()
	defer c.sessionMutex.Unlock()
	if c.sessionKey == "" {
		url := fmt.Sprintf("%s://%s%s", c.url.Scheme, c.url.Host, sessionURLPath)
		request, err := http.NewRequest(http.MethodPost, url, nil)
		if err != nil {
			return err
		}
		username := c.url.User.Username()
		password, _ := c.url.User.Password()
		request.SetBasicAuth(username, password)

		var sessionData string
		if _, err = c.handleRequest(request, &sessionData); err != nil {
			return err
		}

		c.sessionKey = sessionData
	}
	return nil
}

// CloseSession closes the current session with vCenter
func (c *VCClient) CloseSession() error {
	c.sessionMutex.Lock()
	defer c.sessionMutex.Unlock()
	if c.sessionKey == "" {
		return nil
	}
	request, err := c.prepareRequest(http.MethodDelete, sessionURLPath, nil)
	if err != nil {
		return err
	}

	if _, err = c.handleRequest(request, nil); err != nil {
		return err
	}

	c.sessionKey = ""
	return nil
}

// prepareRequest prepares an HTTP request with the appropriate headers
func (c *VCClient) prepareRequest(method string, urlPath string, data []byte) (*http.Request, error) {
	url := fmt.Sprintf("%s://%s%s", c.url.Scheme, c.url.Host, urlPath)
	log.Info("Requesting", "url", url)
	req, err := http.NewRequest(method, url, bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("vmware-api-session-id", c.sessionKey)
	return req, nil
}

// handleRequest handles an HTTP request and processes the response
func (c *VCClient) handleRequest(request *http.Request, responseData interface{}) (int, error) {
	response, err := c.httpClient.Do(request)
	if err != nil {
		log.Error(err, "Failed to do HTTP request")
		return 0, err
	}
	return handleHTTPResponse(response, responseData)
}

// handleHTTPResponse processes an HTTP response
func handleHTTPResponse(response *http.Response, result interface{}) (int, error) {
	statusCode := response.StatusCode
	if statusCode == http.StatusNoContent {
		return statusCode, nil
	}

	if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
		if result == nil {
			return statusCode, nil
		}
		body, err := io.ReadAll(response.Body)
		defer response.Body.Close()

		if err != nil {
			return statusCode, err
		}
		if err = json.Unmarshal(body, result); err != nil {
			return statusCode, err
		}
		return statusCode, nil
	}

	var err error
	if statusCode == http.StatusNotFound {
		err = util.HttpNotFoundError
	} else if statusCode == http.StatusBadRequest {
		err = util.HttpBadRequest
	} else {
		err = fmt.Errorf("HTTP response with errorcode %d", response.StatusCode)
	}
	return statusCode, err
}