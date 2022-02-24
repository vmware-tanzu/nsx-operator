/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package jwt

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/govmomi/sts"
)

func TestJwtcache_NewJWTCache(t *testing.T) {
	tesClient := &TESClient{}
	freshInterval := time.Second
	cache := NewJWTCache(tesClient, freshInterval)
	assert.Equal(t, cache.freshInterval, 30*time.Second)

	freshInterval = 60 * time.Second
	cache = NewJWTCache(tesClient, freshInterval)
	assert.Equal(t, cache.freshInterval, freshInterval)
}

func TestJwtcache_GetJWT(t *testing.T) {
	tesClient := &TESClient{}
	freshInterval := 10 * time.Second
	result := `{"value": {"access_token": "eyJraWQiOiI3RDVDQTMwNzYxNEM2MTE5NDkxNTNBRTc1QjY2NTFCODJDMzZDRjkxIiwiYWxnIjoiUlMyNTYifQ.eyJzdWIiOiJBZG1pbmlzdHJhdG9yQHZzcGhlcmUubG9jYWwiLCJhdWQiOiJ2bXdhcmUtdGVzOnZjOm5zeGQtdjI6bnN4IiwibnN4LXJvbGUiOlsiZW50ZXJwcmlzZV9hZG1pbiIsInZzcGhlcmVfcmVhZF9vbmx5IiwidnNwaGVyZV9hZG1pbiJdLCJkb21haW4iOiJ2c3BoZXJlLmxvY2FsIiwiaXNzIjoiaHR0cHM6XC9cL3NjMi0xMC0xODYtNjItMTkxLmVuZy52bXdhcmUuY29tXC9vcGVuaWRjb25uZWN0XC92c3BoZXJlLmxvY2FsIiwiZXhwIjoxNjQ1MTI1Mjk1LCJpYXQiOjE2NDUwODkyOTUsImp0aSI6ImI3MDBjNmY1LTFkNzQtNDY3OC04Y2U5LWYyMTA0MGM1MjVmMSIsInVzZXJuYW1lIjoiQWRtaW5pc3RyYXRvciJ9.ZOCflteorXRfaXwzPqsxQAzvOoISgJVzSTRhlNE-ZT87UYdvvnelCbBK7fRlgT7FldFzUN0kJo3evdRZe6_hKAucyTQdfLrSSjAEEeGnj9wvOTut57T2rlv6t_BfYI3mp08vciw-fPkXwbabfCtuyK43HLoiMbzF_VJV-yzOROxYICs6RVdInVPL9NjOCQvaEp0hf14SfHcknYcmQMMODFvySyO9d5w_ReIHYPf5KA_6KMj_AgNLlQjHJ_7T7wTA_ag-LFyjkC34zBUHoyr24vAK48kLB2Sunrj0knanCxrd55xF7mBBakqpO2k-SdGpyeF9eRO807ztgA7jF9-8HA"}}`
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(result))
	}))
	defer ts.Close()
	index := strings.Index(ts.URL, "//")
	a := ts.URL[index+2:]

	cache := NewJWTCache(tesClient, freshInterval)
	cache.tesClient = tesClient
	config := &tls.Config{InsecureSkipVerify: true}
	tr := &http.Transport{
		TLSClientConfig: config,
	}
	httpClient := &http.Client{Transport: tr}
	tesClient.VCClient = &VCClient{url: &url.URL{Scheme: "https", Host: a}}
	tesClient.VCClient.httpClient = httpClient

	tesClient.VCClient.signer = &sts.Signer{}
	tesClient.VCClient.signer.Token = "this is a saml token"
	_, err := cache.GetJWT(false)
	assert.Equal(t, err, nil)
}

func TestJwtcache_GetJWTExpire(t *testing.T) {
	token1 := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IlRhbyBUYW8iLCJpYXQiOjE1MTYyMzkwMjIsImV4cCI6MTk3ODAwMDAwfQ.mmGvAbZYBoByhnhmazlhvXYqhG2-fcwt5_bKbAf-B9U"
	cache := &JWTCache{}
	_, err := cache.getJWTExpire(token1)
	assert.Equal(t, err, nil)
}
