/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestNsxVersion_validate(t *testing.T) {
	nsxVersion := &NsxVersion{}
	nsxVersion.NodeVersion = "12"

	expect := errors.New("error version format")
	err := nsxVersion.validate()
	assert.Equal(t, err, expect)

	nsxVersion.NodeVersion = "12.3"
	err = nsxVersion.validate()
	assert.Equal(t, err, expect)

	nsxVersion.NodeVersion = "3.2.3.3.0.18844962"
	err = nsxVersion.validate()
	assert.Equal(t, err, nil)

	nsxVersion.NodeVersion = "3.2.3"
	err = nsxVersion.validate()
	assert.Equal(t, err, nil)
}

func TestNsxVersion_VCConfig(t *testing.T) {
	vcConfig := &VCConfig{}
	expect := errors.New("invalid field " + "VcEndPoint")
	err := vcConfig.validate()
	assert.Equal(t, err, expect)

	vcConfig.VCEndPoint = "10.0.0.1"
	expect = errors.New("invalid field " + "SsoDomain")
	err = vcConfig.validate()
	assert.Equal(t, err, expect)

	vcConfig.SsoDomain = "vsphere@local"
	expect = errors.New("invalid field " + "HttpsPort")
	err = vcConfig.validate()
	assert.Equal(t, err, expect)

	vcConfig.HttpsPort = 443
	err = vcConfig.validate()
	assert.Equal(t, err, nil)
}

func TestNsxVersion_CoeConfig(t *testing.T) {
	coeConfig := &CoeConfig{}
	expect := errors.New("invalid field " + "Cluster")
	err := coeConfig.validate()
	assert.Equal(t, err, expect)

	coeConfig.Cluster = "10.0.0.1"
	err = coeConfig.validate()
	assert.Equal(t, err, nil)

}

func TestNsxVersion_NsxConfig(t *testing.T) {
	nsxConfig := &NsxConfig{}
	expect := errors.New("invalid field " + "NsxApiManagers")
	err := nsxConfig.validate()
	assert.Equal(t, err, expect)

	nsxConfig.NsxApiManagers = []string{"10.0.0.1"}
	err = nsxConfig.validate()
	assert.Equal(t, err, nil)

	nsxConfig.Thumbprint = []string{"0a:fc"}
	err = nsxConfig.validate()
	assert.Equal(t, err, nil)

	nsxConfig.Thumbprint = []string{"0a:fc", "ob:fd"}
	expect = errors.New("thumbprint count not match manager count")
	err = nsxConfig.validate()
	assert.Equal(t, err, expect)
}

func TestNsxVersion_NewNSXOperatorConfigFromFile(t *testing.T) {
	// failed to open ini file
	_, err := NewNSXOperatorConfigFromFile()
	_, ok := err.(*fs.PathError)
	assert.Equal(t, ok, true)

	// failed to get version
	configFilePath = "../mock/nsxop.ini"
	_, err = NewNSXOperatorConfigFromFile()
	_, ok = err.(*url.Error)
	assert.Equal(t, ok, true)
}
