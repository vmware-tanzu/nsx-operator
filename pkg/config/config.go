/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"errors"
	"flag"
	"io/ioutil"

	ini "gopkg.in/ini.v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/auth"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/auth/jwt"
)

// TODO replace to yaml
const (
	nsxOperatorDefaultConf = "/etc/nsx-operator/nsxop.ini"
	vcHostCACertPath       = "/etc/vmware/wcp/tls/vmca.pem"
)

var (
	configFilePath = ""
	log            = logf.Log.WithName("config")
	tokenProvider  auth.TokenProvider
)

// TODO delete unnecessary config

type NSXOperatorConfig struct {
	*DefaultConfig
	*CoeConfig
	*NsxConfig
	*K8sConfig
	*VCConfig
}

type DefaultConfig struct {
	Debug bool `ini:"debug"`
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
	Insecure             bool     `ini:"insecure"`
	SingleTierSrTopology bool     `ini:"single_tier_sr_topology"`
	EnforcementPoint     string   `ini:"enforcement_point"`
}

type K8sConfig struct {
	BaseLinePolicyType string `ini:"baseline_policy_type"`
	EnableNCPEvent     bool   `ini:"enable_ncp_event"`
	EnableVNetCRD      bool   `ini:"enable_vnet_crd"`
	EnableRestore      bool   `ini:"enable_restore"`
	EnablePromMetrics  bool   `ini:"enable_prometheus_metrics"`
	KubeConfigFile     string `ini:"kubeconfig"`
}

type VCConfig struct {
	VCEndPoint string `ini:"vc_endpoint"`
	SsoDomain  string `ini:"sso_domain"`
	HttpsPort  int    `ini:"https_port"`
}

type Validate interface {
	validate() error
}

type NsxVersion struct {
	NodeVersion string `json:"node_version"`
}

func AddFlags() {
	flag.StringVar(&configFilePath, "nsxconfig", nsxOperatorDefaultConf, "NSX Operator configuration file path")
}

func NewNSXOperatorConfigFromFile() (*NSXOperatorConfig, error) {
	nsxOperatorConfig := NewNSXOpertorConfig()

	cfg := ini.Empty()
	err := ini.ReflectFrom(cfg, nsxOperatorConfig)
	if err != nil {
		return nil, err
	}
	cfg, err = ini.Load(configFilePath)
	if err != nil {
		return nil, err
	}
	err = cfg.Section("DEFAULT").MapTo(nsxOperatorConfig.DefaultConfig)
	if err != nil {
		return nil, err
	}
	err = cfg.Section("coe").MapTo(nsxOperatorConfig.CoeConfig)
	if err != nil {
		return nil, err
	}
	err = cfg.Section("nsx_v3").MapTo(nsxOperatorConfig.NsxConfig)
	if err != nil {
		return nil, err
	}
	err = cfg.Section("k8s").MapTo(nsxOperatorConfig.K8sConfig)
	if err != nil {
		return nil, err
	}
	err = cfg.Section("vc").MapTo(nsxOperatorConfig.VCConfig)
	if err != nil {
		return nil, err
	}

	if err := nsxOperatorConfig.validate(); err != nil {
		return nil, err
	}

	return nsxOperatorConfig, nil
}

func NewNSXOpertorConfig() *NSXOperatorConfig {
	defaultNSXOperatorConfig := &NSXOperatorConfig{
		&DefaultConfig{},
		&CoeConfig{
			"",
		},
		&NsxConfig{},
		&K8sConfig{},
		&VCConfig{},
	}
	return defaultNSXOperatorConfig
}

func (operatorConfig *NSXOperatorConfig) validate() error {
	if err := operatorConfig.CoeConfig.validate(); err != nil {
		return err
	}
	if err := operatorConfig.NsxConfig.validate(); err != nil {
		return err
	}
	// TODO, verify if user&pwd, cert, jwt has any of them provided
	return nil
}

// it's not thread safe
func (operatorConfig *NSXOperatorConfig) GetTokenProvider() auth.TokenProvider {
	if tokenProvider == nil {
		operatorConfig.createTokenProvider()
	}
	return tokenProvider
}

func (operatorConfig *NSXOperatorConfig) createTokenProvider() error {
	log.V(2).Info("try to load VC host CA")
	var vcCaCert []byte
	var err error
	if !operatorConfig.Insecure {
		vcCaCert, err = ioutil.ReadFile(vcHostCACertPath)
		// If operatorConfig.VCInsecure is false, tls will the CA to verify the server
		// certificate. If loading CA failed, tls will use the CA on local host
		if err != nil {
			log.Info("fail to load CA cert from file", "error", err)
		}
	}

	if err := operatorConfig.VCConfig.validate(); err == nil {
		tokenProvider, _ = jwt.NewTokenProvider(operatorConfig.VCEndPoint, operatorConfig.HttpsPort, operatorConfig.SsoDomain, vcCaCert, operatorConfig.Insecure)
	}
	return nil
}

func (vcConfig *VCConfig) validate() error {
	if len(vcConfig.VCEndPoint) == 0 {
		err := errors.New("invalid field " + "VcEndPoint")
		log.Info("validate VcConfig failed", "VcEndPoint", vcConfig.VCEndPoint)
		return err
	}

	if len(vcConfig.SsoDomain) == 0 {
		err := errors.New("invalid field " + "SsoDomain")
		log.Info("validate VcConfig failed", "SsoDomain", vcConfig.SsoDomain)
		return err
	}

	if vcConfig.HttpsPort == 0 {
		err := errors.New("invalid field " + "HttpsPort")
		log.Info("validate VcConfig failed", "HttpsPort", vcConfig.HttpsPort)
		return err
	}
	return nil
}

func (nsxConfig *NsxConfig) validate() error {
	mCount := len(nsxConfig.NsxApiManagers)
	if mCount == 0 {
		err := errors.New("invalid field " + "NsxApiManagers")
		log.Error(err, "validate NsxConfig failed", "NsxApiManagers", nsxConfig.NsxApiManagers)
		return err
	}
	tpCount := len(nsxConfig.Thumbprint)
	if tpCount == 0 {
		log.V(1).Info("no thumbprint provided")
		return nil
	}
	if tpCount == 1 {
		log.V(1).Info("all endpoints share one thumbprint")
		return nil
	}
	if tpCount > 1 && tpCount != mCount {
		err := errors.New("thumbprint count not match manager count")
		log.Error(err, "validate NsxConfig failed", "thumbprint count", tpCount, "manager count", mCount)
		return err
	}
	return nil
}

func (coeConfig *CoeConfig) validate() error {
	if len(coeConfig.Cluster) == 0 {
		err := errors.New("invalid field " + "Cluster")
		log.Error(err, "validate coeConfig failed")
		return err
	}
	return nil
}
