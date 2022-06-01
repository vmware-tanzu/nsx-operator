/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package jwt

import (
	"errors"
	"fmt"
	"sync"
	"time"

	gojwt "github.com/golang-jwt/jwt"
)

const (
	minFreshInterval = 30 * time.Second
)

// JWTCache ...
type JWTCache struct {
	tesClient     *TESClient
	mutex         sync.Mutex
	jwt           string
	expire        time.Time
	freshInterval time.Duration
}

// NewJWTCache ...
func NewJWTCache(tesClient *TESClient, freshInterval time.Duration) *JWTCache {
	if freshInterval < minFreshInterval {
		freshInterval = minFreshInterval
	}
	return &JWTCache{
		tesClient:     tesClient,
		freshInterval: freshInterval,
	}
}

func (cache *JWTCache) GetJWT(refreshToken bool) (string, error) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	if !refreshToken {
		if cache.jwt != "" && time.Now().Add(cache.freshInterval).Before(cache.expire) {
			return cache.jwt, nil
		}
	}
	jwt, err := cache.refreshJWT()
	if err != nil || jwt == "" {
		log.V(1).Info("get JWT", "Refresh JWT error", err)
		cache.tesClient.signer = nil
		return "", err
	}
	exp, err := cache.getJWTExpire(jwt)
	if err != nil {
		log.Info("get JWT", "get JWT expire", err)
		cache.expire = exp
		return "", err
	}
	if cache.jwt == "" || cache.expire.Before(exp) {
		cache.jwt = jwt
		cache.expire = exp
	}
	return cache.jwt, nil
}

func (cache *JWTCache) refreshJWT() (string, error) {
	if cache.tesClient.signer == nil {
		if err := cache.tesClient.reloadUsernamePass(); err != nil {
			log.Error(err, "JWT cache failed to refresh JWT ")
			return "", err
		}
		if err := cache.tesClient.getorRenewVAPISession(); err != nil {
			log.Error(err, "JWT cache failed to refresh JWT ")
			return "", err
		}
	}
	if jwt, err := cache.tesClient.ExchangeJWT(cache.tesClient.signer.Token, false); err != nil {
		log.Error(err, "JWT cache failed to refresh JWT ")
		return "", err
	} else {
		return jwt, nil
	}
}

func (cache *JWTCache) getJWTExpire(jwt string) (time.Time, error) {
	token, _, err := new(gojwt.Parser).ParseUnverified(jwt, gojwt.MapClaims{})
	if err != nil {
		return time.Now(), err
	}
	claims := token.Claims.(gojwt.MapClaims)
	expirationTime, ok := claims["exp"].(float64)
	if !ok {
		errMsg := fmt.Sprintf("JWT doesn't have claim 'exp' of type 'float64'; claims: %v", claims)
		return time.Now(), errors.New(errMsg)
	}
	return time.Unix(int64(expirationTime), 0), nil
}
