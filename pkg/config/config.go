/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"flag"
	ini "gopkg.in/ini.v1"
	"k8s.io/klog"
)

//TODO replace to yaml
const (
	nsxOperatorDefaultConf = "/etc/nsx-operator/nsxop.ini"
)

var (
	configFilePath = ""
)

//TODO delete unnecessary config

type NSXOperatorConfig struct {
	*CoeConfig
	*NsxConfig
	*K8sConfig
	*VcConfig
}

type CoeConfig struct {
	Cluster string `ini:"cluster"`
}

type NsxConfig struct {
	NsxApiUser           string   `ini:"nsx_api_user"`
	NsxApiPassword       string   `ini:"nsx_api_password"`
	NsxApiCertFile       string   `ini:"nsx_api_cert_file"`
	NsxApiPrivateKeyFile string   `ini:"nsx_api_private_key_file"`
	NsxApiManagers       []string `ini:"nsx_api_managers"`
	CaFile               []string `ini:"ca_file"`
	Thumbprint           []string `ini:"thumbprint"`
	Insecure             bool     `ini:"inseure"`
	SingleTierSrTopology bool     `ini:"single_tier_sr_topology"`
}

type K8sConfig struct {
	BaseLinePolicyType string `ini:"baseline_policy_type"`
	EnableNCPEvent     bool   `ini:"enable_ncp_event"`
	EnableVNetCRD      bool   `ini:"enable_vnet_crd"`
	EnableRestore      bool   `ini:"enable_restore"`
	KubeConfigFile     string `ini:"kubeconfig"`
}

type VcConfig struct {
	VcEndPoint string `ini:"vc_endpoint"`
	SsoDomain  string `ini:"sso_domain"`
	HttpsPort  int    `ini:"https_port"`
}

func AddFlags() {
	flag.StringVar(&configFilePath, "nsxconfig", nsxOperatorDefaultConf, "NSX Operator configuration file path")
}

func NewNSXOperatorConfigFromFile() (*NSXOperatorConfig, error) {
	nsxOperatorConfig := NewNSXOpertorConfig()

	cfg := ini.Empty()
	err := ini.ReflectFrom(cfg, nsxOperatorConfig)
	if err != nil {
		klog.Errorf("Failed to load default NSX Operator configuration")
		return nil, err
	}
	cfg, err = ini.Load(configFilePath)
	if err != nil {
		klog.Errorf("Failed to load NSX Operator configuration from file")
		return nil, err
	}
	err = cfg.Section("coe").MapTo(nsxOperatorConfig.CoeConfig)
	if err != nil {
		klog.Errorf("Failed to parse coe section from NSX Operator config, please check the configuration")
		return nil, err
	}
	err = cfg.Section("nsx_v3").MapTo(nsxOperatorConfig.NsxConfig)
	if err != nil {
		klog.Errorf("Failed to parse nsx section from NSX Operator config, please check the configuration")
		return nil, err
	}
	err = cfg.Section("k8s").MapTo(nsxOperatorConfig.K8sConfig)
	if err != nil {
		klog.Errorf("Failed to parse k8s section from NSX Operator config, please check the configuration")
		return nil, err
	}
	err = cfg.Section("vc").MapTo(nsxOperatorConfig.VcConfig)
	if err != nil {
		klog.Errorf("Failed to parse vc section from NSX Operator config, please check the configuration")
		return nil, err
	}

	return nsxOperatorConfig, nil
}

func NewNSXOpertorConfig() *NSXOperatorConfig {
	defaultNSXOperatorConfig := &NSXOperatorConfig{
		&CoeConfig{
			"",
		},
		&NsxConfig{},
		&K8sConfig{},
		&VcConfig{},
	}
	return defaultNSXOperatorConfig
}

func Validate(*NSXOperatorConfig) error {
	return nil
}
