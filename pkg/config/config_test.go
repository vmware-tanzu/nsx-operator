/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNsxVersion_getVersion(t *testing.T) {
	result := `{
				"node_version": "3.1.3.3.0.18844962",
				"product_version": "3.1.3.3.0.18844959"
				}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(result))
	}))
	defer ts.Close()
	nsxVersion := &NsxVersion{}
	err := nsxVersion.getVersion(ts.URL, "test", "test123", nil)
	assert.True(t, err == nil)
	assert.Equal(t, nsxVersion.NodeVersion, "3.1.3.3.0.18844962")

}

func TestNsxVersion_enableFeature(t *testing.T) {
	nsxVersion := &NsxVersion{}
	nsxVersion.NodeVersion = "3.1.3.3.0.18844962"
	assert.False(t, nsxVersion.featureSupported())
	nsxVersion.NodeVersion = "3.2.0.3.0.18844962"
	assert.True(t, nsxVersion.featureSupported())
	nsxVersion.NodeVersion = "3.11.0.3.0.18844962"
	assert.True(t, nsxVersion.featureSupported())
	nsxVersion.NodeVersion = "4.1.0"
	assert.True(t, nsxVersion.featureSupported())
	nsxVersion.NodeVersion = "3.2.0"
	assert.True(t, nsxVersion.featureSupported())
}
