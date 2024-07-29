/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
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

	vcConfig.VCUser = ""
	vcConfig.VCPassword = "test"
	err = vcConfig.validate()
	assert.NotEqual(t, err, nil)

	vcConfig.VCUser = "test1"
	vcConfig.VCPassword = "test"
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
	err := nsxConfig.validate(false)
	assert.Equal(t, err, expect)

	nsxConfig.NsxApiManagers = []string{"10.0.0.1"}
	expect = errors.New("no ca file or thumbprint or nsx username/password provided")
	err = nsxConfig.validate(false)
	assert.Equal(t, err, expect)

	nsxConfig.Thumbprint = []string{"0a:fc"}
	err = nsxConfig.validate(false)
	assert.Equal(t, err, nil)

	nsxConfig.CaFile = []string{"0a:fc", "ob:fd"}
	expect = errors.New("ca or cert file count not match manager count")
	err = nsxConfig.validate(false)
	assert.Equal(t, err, expect)

	// Insecure == true
	nsxConfig.CaFile = []string{"0a:fc", "ob:fd"}
	nsxConfig.Insecure = true
	err = nsxConfig.validate(false)
	assert.Equal(t, err, nil)

	nsxConfig.CaFile = []string{}
	nsxConfig.Insecure = false
	nsxConfig.Thumbprint = []string{"0a:fc", "ob:fd"}
	expect = errors.New("thumbprint count not match manager count")
	err = nsxConfig.validate(false)
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

	configFilePath = "../mock/nsxop_err.ini"
	_, err = NewNSXOperatorConfigFromFile()
	assert.NotNil(t, err)
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

func TestConfig_GetHA(t *testing.T) {
	configFilePath = "../mock/nsxop.ini"
	cf, err := NewNSXOperatorConfigFromFile()
	assert.Equal(t, err, nil)
	assert.Equal(t, cf.HAEnabled(), true)
}

func TestNSXOperatorConfig_GetCACert(t *testing.T) {
	caFile, _ := os.CreateTemp("", "config_test")
	caFile.Write([]byte("dummy file"))
	caFile.Close()
	defer os.Remove(caFile.Name())
	caFile2, _ := os.CreateTemp("", "config_test")
	caFile2.Write([]byte("dummy file2"))
	caFile2.Close()
	defer os.Remove(caFile2.Name())
	type fields struct {
		nsxCA  []byte
		caFile []string
	}
	tests := []struct {
		name   string
		fields fields
		want   []byte
	}{
		{
			name: "no",
			fields: fields{
				nsxCA:  nil,
				caFile: nil,
			},
			want: []byte{},
		},
		{
			name: "cached",
			fields: fields{
				nsxCA:  []byte("dummy\n"),
				caFile: nil,
			},
			want: []byte("dummy\n"),
		},
		{
			name: "readFile",
			fields: fields{
				nsxCA:  nil,
				caFile: []string{caFile.Name(), caFile2.Name()},
			},
			want: []byte("dummy file\ndummy file2\n"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			operatorConfig := &NSXOperatorConfig{
				NsxConfig:   &NsxConfig{CaFile: tt.fields.caFile},
				configCache: configCache{nsxCA: tt.fields.nsxCA},
			}
			assert.Equalf(t, tt.want, operatorConfig.GetCACert(), "GetCACert()")
			assert.Equalf(t, tt.want, operatorConfig.configCache.nsxCA, "GetCACert()")
		})
	}
}

func TestNsxConfig_GetServiceSize(t *testing.T) {
	type fields struct {
		ServiceSize string
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{{
		name: "default",
		fields: fields{
			ServiceSize: "",
		},
		want: model.LBService_SIZE_SMALL,
	}, {
		name: "large",
		fields: fields{
			ServiceSize: model.LBService_SIZE_LARGE,
		},
		want: model.LBService_SIZE_LARGE,
	},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nsxConfig := &NsxConfig{
				NSXLBSize: tt.fields.ServiceSize,
			}
			assert.Equalf(t, tt.want, nsxConfig.GetNSXLBSize(), "GetNSXLBSize()")
		})
	}
}
