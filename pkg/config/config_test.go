/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_VCConfig(t *testing.T) {
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

func TestConfig_CoeConfig(t *testing.T) {
	coeConfig := &CoeConfig{}
	expect := errors.New("invalid field " + "Cluster")
	err := coeConfig.validate()
	assert.Equal(t, err, expect)

	coeConfig.Cluster = "10.0.0.1"
	err = coeConfig.validate()
	assert.Equal(t, err, nil)
}

func TestConfig_NsxConfig(t *testing.T) {
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

func TestConfig_NewNSXOperatorConfigFromFile(t *testing.T) {
	// failed to open ini file
	_, err := NewNSXOperatorConfigFromFile()
	_, ok := err.(*fs.PathError)
	assert.Equal(t, ok, true)

	configFilePath = "../mock/nsxop.ini"
	_, err = NewNSXOperatorConfigFromFile()
	assert.Equal(t, err, nil)
}

func TestConfig_GetTokenProvider(t *testing.T) {
	vcConfig := &VCConfig{}
	vcConfig.VCEndPoint = "127.0.0.1"
	vcConfig.SsoDomain = "vsphere@local"
	vcConfig.HttpsPort = 443
	nsxConfig := &NSXOperatorConfig{VCConfig: vcConfig, NsxConfig: &NsxConfig{}}
	tokenProvider := nsxConfig.GetTokenProvider()
	assert.NotNil(t, tokenProvider)
}
