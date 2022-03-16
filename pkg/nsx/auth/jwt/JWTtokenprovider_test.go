/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package jwt

import (
	"io/fs"
	"net/url"
	"os"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
)

func TestJWTTokenprovider_NewTokenProvider(t *testing.T) {
	_, err := NewTokenProvider("127.0.0.1", 443, "vsphere.local", []byte{}, false)
	_, ok := err.(*fs.PathError)
	assert.Equal(t, ok, true)

	patch := gomonkey.ApplyFunc(os.ReadFile, func(filename string) ([]byte, error) {
		return []byte{}, nil
	})
	defer patch.Reset()
	_, err = NewTokenProvider("127.0.0.1", 443, "vsphere.local", []byte{}, false)
	_, ok = err.(*url.Error)
	assert.Equal(t, ok, true)
}

func TestJWTTokenprovider_HeaderValue(t *testing.T) {
	provider := &JWTTokenProvider{}
	value := provider.HeaderValue("hello")
	assert.Equal(t, value, "Bearer hello")
}
