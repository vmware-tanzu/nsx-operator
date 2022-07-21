/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package jwt

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJWTTokenprovider_NewTokenProvider(t *testing.T) {
	_, err := NewTokenProvider("127.0.0.1", 443, "vsphere.local", []byte{}, false)
	_, ok := err.(*url.Error)
	assert.Equal(t, ok, false)
}

func TestJWTTokenprovider_HeaderValue(t *testing.T) {
	provider := &JWTTokenProvider{}
	value := provider.HeaderValue("hello")
	assert.Equal(t, value, "Bearer hello")
}
