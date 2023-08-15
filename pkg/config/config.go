/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"

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
	LogLevel               int
	ProbeAddr, MetricsAddr string
	configFilePath         = ""
	log                    = logf.Log.WithName("config")
	tokenProvider          auth.TokenProvider
)

// TODO delete unnecessary config
type NSXOperatorConfig struct {
	*DefaultConfig
	*CoeConfig
	*NsxConfig
	*K8sConfig
	*VCConfig
	*HAConfig
}

func (operatorConfig *NSXOperatorConfig) HAEnabled() bool {
	if operatorConfig.EnableHA == nil || *operatorConfig.EnableHA == true {
		return true
	}
	return false
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
	// Controlled by FSS
	EnableAntreaNSXInterworking bool `ini:"enable_antrea_nsx_interworking"`
}

type VCConfig struct {
	VCEndPoint string `ini:"vc_endpoint"`
	SsoDomain  string `ini:"sso_domain"`
	HttpsPort  int    `ini:"https_port"`
}

type HAConfig struct {
	EnableHA *bool `ini:"enable"`
}

type Validate interface {
	validate() error
}

type NsxVersion struct {
	NodeVersion string `json:"node_version"`
}

func AddFlags() {
	flag.StringVar(&configFilePath, "nsxconfig", nsxOperatorDefaultConf, "NSX Operator configuration file path")
	flag.StringVar(&ProbeAddr, "health-probe-bind-address", ":8384", "The address the probe endpoint binds to.")
	flag.StringVar(&MetricsAddr, "metrics-bind-address", ":8093", "The address the metrics endpoint binds to.")
	flag.IntVar(&LogLevel, "log-level", 0, "Use zap-core log system.")
	flag.Parse()
}

func UpdateConfigFilePath(configFile string) {
	configFilePath = configFile
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
	err = cfg.Section("ha").MapTo(nsxOperatorConfig.HAConfig)
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
		&HAConfig{},
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
	log.V(1).Info("try to load VC host CA")
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

func removeEmptyItem(source []string) []string {
	target := make([]string, 0)
	for _, value := range source {
		if len(value) == 0 {
			continue
		}
		target = append(target, value)
	}
	return target
}

func (nsxConfig *NsxConfig) validateCert() error {
	if nsxConfig.Insecure == true {
		return nil
	}
	nsxConfig.Thumbprint = removeEmptyItem(nsxConfig.Thumbprint)
	nsxConfig.CaFile = removeEmptyItem(nsxConfig.CaFile)
	mCount := len(nsxConfig.NsxApiManagers)
	tpCount := len(nsxConfig.Thumbprint)
	caCount := len(nsxConfig.CaFile)
	// ca file has high priority than thumbprint
	// ca file(thumbprint) == 1 or equal to manager count
	if caCount == 0 && tpCount == 0 {
		err := errors.New("no ca file or thumbprint provided")
		log.Error(err, "validate NsxConfig failed")
		return err
	}
	if caCount > 0 {
		log.V(1).Info("validate CA file", "CA file number", caCount)
		if caCount > 1 && caCount != mCount {
			err := errors.New("ca file count not match manager count")
			log.Error(err, "validate NsxConfig failed", "ca file count", caCount, "manager count", mCount)
			return err
		}
		for _, file := range nsxConfig.CaFile {
			if _, err := os.Stat(file); os.IsNotExist(err) {
				err = fmt.Errorf("ca file does not exist %s", file)
				log.Error(err, "validate NsxConfig failed")
				return err
			}
		}
	} else {
		log.V(1).Info("validate thumbprint", "thumbprint number", tpCount)
		if tpCount > 1 && tpCount != mCount {
			err := errors.New("thumbprint count not match manager count")
			log.Error(err, "validate NsxConfig failed", "thumbprint count", tpCount, "manager count", mCount)
			return err
		}
	}
	return nil
}

func (nsxConfig *NsxConfig) validate() error {
	nsxConfig.NsxApiManagers = removeEmptyItem(nsxConfig.NsxApiManagers)
	mCount := len(nsxConfig.NsxApiManagers)
	if mCount == 0 {
		err := errors.New("invalid field " + "NsxApiManagers")
		log.Error(err, "validate NsxConfig failed", "NsxApiManagers", nsxConfig.NsxApiManagers)
		return err
	}
	if err := nsxConfig.validateCert(); err != nil {
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
