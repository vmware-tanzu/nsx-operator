/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package jwt

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTESClient_NewTESClient(t *testing.T) {
	_, err := NewTESClient("10.0.0.1", 433, "vsphere.local", "admin", "admin", []byte{}, true)
	assert.Equal(t, err, nil)
}

func TestTESClient_ExchangeJWT(t *testing.T) {
	tesClient := &TESClient{}
	tesClient.VCClient = &VCClient{}
	tesClient.VCClient.url = &url.URL{Scheme: "https", Host: "127.0.0.1"}

	_, err := tesClient.ExchangeJWT("hello", false)
	assert.True(t, strings.Contains(err.Error(), "failed to exchange JWT due to"))
}
