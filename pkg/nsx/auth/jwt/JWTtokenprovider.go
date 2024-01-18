/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package jwt

import (
	"time"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/auth"
)

const (
	VC_SVCACCOUNT_USER_PATH = "/etc/nsx-ujo/vc/username"
	// #nosec G101: false positive triggered by variable name which includes "pwd"
	VC_SVCACCOUNT_PWD_PATH = "/etc/nsx-ujo/vc/password"
)

var (
	log = logger.Log
)

type JWTTokenProvider struct {
	cache *JWTCache
}

func (provider *JWTTokenProvider) GetToken(refreshToken bool) (string, error) {
	return provider.cache.GetJWT(refreshToken)
}

func (provider *JWTTokenProvider) HeaderValue(token string) string {
	return "Bearer " + token
}

func NewTokenProvider(vcEndpoint string, port int, ssoDomain, user, password string, caCert []byte, insecure bool) (auth.TokenProvider, error) {
	// not load username/password, not create vapi session, defer them to cache.refreshJWT
	tesClient, err := NewTESClient(vcEndpoint, port, ssoDomain, user, password, caCert, insecure)
	if err != nil {
		log.Error(err, "failed to create tes client")
		return nil, err
	}

	cache := NewJWTCache(tesClient, 60*time.Second)
	return &JWTTokenProvider{cache: cache}, nil
}
