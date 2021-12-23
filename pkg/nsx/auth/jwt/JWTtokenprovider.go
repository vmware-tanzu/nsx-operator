/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package jwt

import (
	"io/ioutil"
	"strings"
	"time"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/auth"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	VC_SVCACCOUNT_USER_PATH = "/etc/nsx-ujo/vc/username"
	VC_SVCACCOUNT_PWD_PATH  = "/etc/nsx-ujo/vc/password"
)

var (
	log = logf.Log.WithName("nsx").WithName("jwt")
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

func NewTokenProvider(vcConfig *config.VcConfig, caCert []byte) (auth.TokenProvider, error) {
	f, err := ioutil.ReadFile(VC_SVCACCOUNT_USER_PATH)
	if err != nil {
		log.Error(err, "failed to read user name")
		return nil, err
	}
	username := strings.TrimRight(string(f), "\n\r")

	f, err = ioutil.ReadFile(VC_SVCACCOUNT_PWD_PATH)
	if err != nil {
		log.Error(err, "failed to read password")
		return nil, err
	}
	password := strings.TrimRight(string(f), "\n\r")

	tesClient, err := NewTESClient(vcConfig.VcEndPoint, vcConfig.HttpsPort, vcConfig.SsoDomain, username, password, nil, true)
	if err != nil {
		log.Error(err, "failed to create tes client")
		return nil, err
	}

	cache := NewJWTCache(tesClient, 60*time.Second)
	return &JWTTokenProvider{cache: cache}, nil
}
