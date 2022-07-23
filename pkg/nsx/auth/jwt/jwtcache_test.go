/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package jwt

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/govmomi/sts"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
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
	token, _ := createToken("sectoid")
	result := fmt.Sprintf(`{"value": {"access_token": "%s"}}`, token)
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
	assert.Nil(t, err)
}

func TestJwtcache_GetJWTFailed(t *testing.T) {
	opts := zap.Options{
		Development: true,
	}
	logf.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	tesClient := &TESClient{}
	freshInterval := 10 * time.Second
	token, _ := createToken("sectoid")
	result := fmt.Sprintf(`{"value": {"access_token": "%s"}}`, token)
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
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
	assert.NotNil(t, err)
}

type TestClaims struct {
	*jwt.StandardClaims
	aud      string
	username string
	domain   string
}

func createToken(user string) (string, error) {
	t := jwt.New(jwt.GetSigningMethod("RS256"))
	t.Claims = &TestClaims{
		&jwt.StandardClaims{
			ExpiresAt: time.Now().Add(time.Minute * 1).Unix(),
		},
		"vmware-tes:vc:nsxd-v2:nsx",
		user, "vsphere.local",
	}
	signKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	return t.SignedString(signKey)
}

func TestJwtcache_GetJWTExpire(t *testing.T) {
	token1, _ := createToken("sectoid")
	cache := &JWTCache{}
	_, err := cache.getJWTExpire(token1)
	assert.Nil(t, err)
}
