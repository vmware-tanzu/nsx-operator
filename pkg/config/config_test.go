/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
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
	vcConfig.VCUser = "admin"
	vcConfig.VCPassword = "password"
	nsxConfig := &NSXOperatorConfig{VCConfig: vcConfig, NsxConfig: &NsxConfig{}}
	tokenProvider := nsxConfig.GetTokenProvider()
	assert.NotNil(t, tokenProvider)

	newTokenProvider := nsxConfig.GetTokenProvider()
	assert.Equal(t, tokenProvider, newTokenProvider)

	nsxConfig.LibMode = true
	newTokenProvider = nsxConfig.GetTokenProvider()
	assert.True(t, tokenProvider != newTokenProvider)
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

func TestNsxConfig_VpcWcpEnhanceEnabled(t *testing.T) {
	f := false
	tr := true
	assert.False(t, (*NsxConfig)(nil).VpcWcpEnhanceEnabled())
	assert.False(t, (&NsxConfig{}).VpcWcpEnhanceEnabled())
	assert.False(t, (&NsxConfig{VpcWcpEnhance: nil}).VpcWcpEnhanceEnabled())
	assert.True(t, (&NsxConfig{VpcWcpEnhance: &tr}).VpcWcpEnhanceEnabled())
	assert.False(t, (&NsxConfig{VpcWcpEnhance: &f}).VpcWcpEnhanceEnabled())
}

func TestK8sConfig_PodV2Enabled(t *testing.T) {
	f := false
	tr := true
	assert.False(t, (*K8sConfig)(nil).PodV2Enabled())
	assert.False(t, (&K8sConfig{}).PodV2Enabled())
	assert.False(t, (&K8sConfig{PodV2: nil}).PodV2Enabled())
	assert.True(t, (&K8sConfig{PodV2: &tr}).PodV2Enabled())
	assert.False(t, (&K8sConfig{PodV2: &f}).PodV2Enabled())
}

func TestNsxConfig_RestoreVifEnabled(t *testing.T) {
	f := false
	tr := true
	assert.False(t, (*NsxConfig)(nil).RestoreVifEnabled())
	assert.False(t, (&NsxConfig{}).RestoreVifEnabled())
	assert.False(t, (&NsxConfig{RestoreVif: nil}).RestoreVifEnabled())
	assert.True(t, (&NsxConfig{RestoreVif: &tr}).RestoreVifEnabled())
	assert.False(t, (&NsxConfig{RestoreVif: &f}).RestoreVifEnabled())
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

func TestK8sConfig_PodV2Ini(t *testing.T) {
	iniContent := `
[DEFAULT]
[coe]
cluster = k8scl-one
[ha]
[k8s]
pod_v2 = true
[nsx_v3]
nsx_api_managers = 127.0.0.1
nsx_api_password = admin
nsx_api_user = admin
thumbprint = 81:49:DD:B7:E8:79:55:5D:9E:75:A9:FA:A6:7D:CB:EA:A4:CA:12:C6
[vc]
`
	tmpFile, err := os.CreateTemp("", "nsxop_podv2_*.ini")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	_, err = tmpFile.WriteString(iniContent)
	assert.NoError(t, err)
	tmpFile.Close()

	oldConfigPath := configFilePath
	defer func() { configFilePath = oldConfigPath }()
	configFilePath = tmpFile.Name()

	cfg, err := NewNSXOperatorConfigFromFile()
	assert.NoError(t, err)
	assert.NotNil(t, cfg.K8sConfig.PodV2)
	assert.True(t, cfg.K8sConfig.PodV2Enabled())
}

func TestK8sConfig_SupportReusePortEnabled(t *testing.T) {
	assert.False(t, (*K8sConfig)(nil).SupportReusePortEnabled())
	assert.False(t, (&K8sConfig{}).SupportReusePortEnabled())
	assert.False(t, (&K8sConfig{SupportReusePort: false}).SupportReusePortEnabled())
	assert.True(t, (&K8sConfig{SupportReusePort: true}).SupportReusePortEnabled())

	assert.False(t, (*NSXOperatorConfig)(nil).SupportReusePortEnabled())
	assert.False(t, (&NSXOperatorConfig{}).SupportReusePortEnabled())
	assert.False(t, (&NSXOperatorConfig{K8sConfig: &K8sConfig{SupportReusePort: false}}).SupportReusePortEnabled())
	assert.True(t, (&NSXOperatorConfig{K8sConfig: &K8sConfig{SupportReusePort: true}}).SupportReusePortEnabled())
}

func TestConfig_Mode(t *testing.T) {
	// Nil receiver tests
	var nilCfg *NSXOperatorConfig
	assert.Equal(t, ModeVPC, nilCfg.GetMode())
	assert.False(t, nilCfg.IsAntreaNSXOnly())
	assert.True(t, nilCfg.IsVPCMode())

	// Nil DefaultConfig
	cfgNoDefault := &NSXOperatorConfig{}
	assert.Equal(t, ModeVPC, cfgNoDefault.GetMode())
	assert.False(t, cfgNoDefault.IsAntreaNSXOnly())
	assert.True(t, cfgNoDefault.IsVPCMode())

	// Default mode
	cfg := NewNSXOpertorConfig()
	assert.Equal(t, ModeVPC, cfg.GetMode())
	assert.True(t, cfg.IsVPCMode())
	assert.False(t, cfg.IsAntreaNSXOnly())

	// Empty string defaults to VPC
	cfg.DefaultConfig.Mode = ""
	assert.Equal(t, ModeVPC, cfg.GetMode())
	assert.True(t, cfg.IsVPCMode())
	assert.False(t, cfg.IsAntreaNSXOnly())

	// Explicit VPC mode (case-insensitive)
	cfg.DefaultConfig.Mode = "vpc"
	assert.Nil(t, cfg.validateMode())
	assert.Equal(t, ModeVPC, cfg.GetMode())
	assert.True(t, cfg.IsVPCMode())
	assert.False(t, cfg.IsAntreaNSXOnly())

	// antreaNSXOnly mode (canonical)
	cfg.DefaultConfig.Mode = ModeAntreaNSXOnly
	cfg.CoeConfig.EnableVPCNetwork = true
	cfg.K8sConfig.EnableAntreaNSXInterworking = false
	assert.Nil(t, cfg.validateMode())
	assert.Equal(t, ModeAntreaNSXOnly, cfg.GetMode())
	assert.True(t, cfg.IsAntreaNSXOnly())
	assert.False(t, cfg.IsVPCMode())
	assert.False(t, cfg.CoeConfig.EnableVPCNetwork)
	assert.True(t, cfg.K8sConfig.EnableAntreaNSXInterworking)

	// antreaNSXOnly with nil CoeConfig and nil K8sConfig
	cfgMinimal := &NSXOperatorConfig{
		DefaultConfig: &DefaultConfig{Mode: ModeAntreaNSXOnly},
	}
	assert.Nil(t, cfgMinimal.validateMode())
	assert.NotNil(t, cfgMinimal.K8sConfig)
	assert.True(t, cfgMinimal.K8sConfig.EnableAntreaNSXInterworking)

	// antreaNSXOnly variants
	for _, modeVariant := range []string{"antreaNSXOnly", "Antrea-NSX-Only", "antrea-nsx-only", "antreansxonly"} {
		cfg.DefaultConfig.Mode = modeVariant
		assert.True(t, cfg.IsAntreaNSXOnly(), "expected %s to be antreaNSXOnly", modeVariant)
		assert.False(t, cfg.IsVPCMode(), "expected %s to not be VPCMode", modeVariant)
		assert.Nil(t, cfg.validateMode())
		assert.Equal(t, ModeAntreaNSXOnly, cfg.GetMode())
	}

	// Invalid mode in validateMode
	cfg.DefaultConfig.Mode = "invalid_mode"
	assert.NotNil(t, cfg.validateMode())

	// Invalid mode in validate()
	assert.NotNil(t, cfg.validate())
}

func TestConfig_ModeFromConfigFile(t *testing.T) {
	// 1. Config file with [coe] mode fallback
	f1, err := os.CreateTemp("", "nsxop_coe_mode_*.ini")
	assert.Nil(t, err)
	defer os.Remove(f1.Name())
	_, err = f1.WriteString(`
[DEFAULT]
[coe]
cluster = k8scl-one
mode = antreaNSXOnly
[nsx_v3]
nsx_api_managers = 127.0.0.1
nsx_api_user = admin
nsx_api_password = admin
thumbprint = 81:49:DD:B7:E8:79:55:5D:9E:75:A9:FA:A6:7D:CB:EA:A4:CA:12:C6
[vc]
`)
	assert.Nil(t, err)
	f1.Close()

	oldPath := configFilePath
	defer func() { configFilePath = oldPath }()

	configFilePath = f1.Name()
	loadedCfg, err := NewNSXOperatorConfigFromFile()
	assert.Nil(t, err)
	assert.Equal(t, ModeAntreaNSXOnly, loadedCfg.GetMode())
	assert.True(t, loadedCfg.IsAntreaNSXOnly())
	assert.False(t, loadedCfg.CoeConfig.EnableVPCNetwork)
	assert.True(t, loadedCfg.K8sConfig.EnableAntreaNSXInterworking)

	// 2. Config file with [DEFAULT] mode
	f2, err := os.CreateTemp("", "nsxop_default_mode_*.ini")
	assert.Nil(t, err)
	defer os.Remove(f2.Name())
	_, err = f2.WriteString(`
[DEFAULT]
mode = antreaNSXOnly
[coe]
cluster = k8scl-one
[nsx_v3]
nsx_api_managers = 127.0.0.1
nsx_api_user = admin
nsx_api_password = admin
thumbprint = 81:49:DD:B7:E8:79:55:5D:9E:75:A9:FA:A6:7D:CB:EA:A4:CA:12:C6
[vc]
`)
	assert.Nil(t, err)
	f2.Close()

	configFilePath = f2.Name()
	loadedCfg2, err := NewNSXOperatorConfigFromFile()
	assert.Nil(t, err)
	assert.Equal(t, ModeAntreaNSXOnly, loadedCfg2.GetMode())
	assert.True(t, loadedCfg2.IsAntreaNSXOnly())
}
