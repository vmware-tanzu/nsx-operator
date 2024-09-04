/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/zap"
	ini "gopkg.in/ini.v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/auth"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/auth/jwt"
)

// TODO replace to yaml
const (
	nsxOperatorDefaultConf = "/etc/nsx-operator/nsxop.ini"
	vcHostCACertPath       = "/etc/vmware/wcp/tls/vmca.pem"
	// LicenseInterval is the timeout for checking license status
	LicenseInterval = 86400
	// LicenseIntervalForDFW is the timeout for checking license status while no DFW license enabled
	LicenseIntervalForDFW = 1800
	defaultWebhookPort    = 9981
	WebhookCertDir        = "/tmp/k8s-webhook-server/serving-certs"
)

var (
	LogLevel               int
	ProbeAddr, MetricsAddr string
	WebhookServerPort      int
	configFilePath         = ""
	configLog              *zap.SugaredLogger
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
	configCache configCache
}

func init() {
	zapLogger, _ := zap.NewProduction()
	configLog = zapLogger.Sugar()
}

func (operatorConfig *NSXOperatorConfig) HAEnabled() bool {
	if operatorConfig.EnableHA == nil || *operatorConfig.EnableHA == true {
		return true
	}
	return false
}

func (operatorConfig *NSXOperatorConfig) GetCACert() []byte {
	ca := operatorConfig.configCache.nsxCA
	if ca == nil {
		ca = []byte{}
		caFiles := operatorConfig.CaFile
		if len(operatorConfig.LeafCertFile) > 0 {
			caFiles = operatorConfig.LeafCertFile
		}
		for _, caFile := range caFiles {
			caCert, err := os.ReadFile(caFile)
			if err != nil || len(caCert) == 0 {
				configLog.Errorf("Failed to read CA file %s, err=%v, skip", caFile, err)
				continue
			}
			ca = append(ca, caCert...)
			ca = append(ca, []byte("\n")...)
		}
		operatorConfig.configCache.nsxCA = ca
	}
	return ca
}

type configCache struct {
	// nsxCA stores all file contents of NsxConfig.CaFile in a byte slice
	nsxCA []byte
}

type DefaultConfig struct {
	Debug bool `ini:"debug"`
}

type CoeConfig struct {
	Cluster          string `ini:"cluster"`
	EnableVPCNetwork bool   `ini:"enable_vpc_network"`
}

type NsxConfig struct {
	NsxApiUser                string   `ini:"nsx_api_user"`
	NsxApiPassword            string   `ini:"nsx_api_password"`
	NsxApiCertFile            string   `ini:"nsx_api_cert_file"`
	NsxApiPrivateKeyFile      string   `ini:"nsx_api_private_key_file"`
	NsxApiManagers            []string `ini:"nsx_api_managers"`
	CaFile                    []string `ini:"ca_file"`
	LeafCertFile              []string `ini:"nsx_leaf_cert_file"`
	Thumbprint                []string `ini:"thumbprint"`
	Insecure                  bool     `ini:"insecure"`
	SingleTierSrTopology      bool     `ini:"single_tier_sr_topology"`
	EnforcementPoint          string   `ini:"enforcement_point"`
	DefaultProject            string   `ini:"default_project"`
	DefaultSubnetSize         int      `ini:"default_subnet_size"`
	DefaultTimeout            int      `ini:"default_timeout"`
	EnvoyHost                 string   `ini:"envoy_host"`
	EnvoyPort                 int      `ini:"envoy_port"`
	LicenseValidationInterval int      `ini:"license_validation_interval"`
	UseAVILoadBalancer        bool     `ini:"use_avi_lb"`
	UseNSXLoadBalancer        *bool    `ini:"use_native_loadbalancer"`
	RelaxNSXLBScaleValication bool     `ini:"relax_scale_validation"`
	NSXLBSize                 string   `ini:"service_size"`
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
	VCUser     string `ini:"user"`
	VCPassword string `ini:"password"`
	VCCAFile   string `ini:"ca_file"`
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
	flag.IntVar(&WebhookServerPort, "webhook-server-port", defaultWebhookPort, "Port number to expose the controller webhook server")
	flag.Parse()
}

func UpdateConfigFilePath(configFile string) {
	configFilePath = configFile
}

func LoadConfigFromFile() (*NSXOperatorConfig, error) {
	configLog.Infof("loading NSX Operator configuration file: %s", configFilePath)
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

func NewNSXOperatorConfigFromFile() (*NSXOperatorConfig, error) {
	nsxOperatorConfig, err := LoadConfigFromFile()
	if err != nil {
		configLog.Error("failed to load NSX Operator configuration file: %v", err)
		return nil, err
	}
	return nsxOperatorConfig, nil
}

func NewNSXOpertorConfig() *NSXOperatorConfig {
	defaultNSXOperatorConfig := &NSXOperatorConfig{
		&DefaultConfig{},
		&CoeConfig{},
		&NsxConfig{},
		&K8sConfig{},
		&VCConfig{},
		&HAConfig{},
		configCache{},
	}
	return defaultNSXOperatorConfig
}

func (operatorConfig *NSXOperatorConfig) validate() error {
	if err := operatorConfig.CoeConfig.validate(); err != nil {
		return err
	}
	if err := operatorConfig.NsxConfig.validate(operatorConfig.CoeConfig.EnableVPCNetwork); err != nil {
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
	configLog.Info("try to load VC host CA")
	var vcCaCert []byte
	var err error
	if !operatorConfig.Insecure {
		if operatorConfig.VCCAFile != "" {
			vcCaCert, err = os.ReadFile(operatorConfig.VCCAFile)
		} else {
			vcCaCert, err = os.ReadFile(vcHostCACertPath)
		}
		// If operatorConfig.VCInsecure is false, tls will use the CA to verify the server
		// certificate. If loading CA failed, tls will use the CA on local host
		if err != nil {
			configLog.Info("fail to load CA cert from file.", " error: ", err)
		}
	}

	if err := operatorConfig.VCConfig.validate(); err == nil {
		if operatorConfig.EnvoyPort != 0 {
			tokenProvider, _ = jwt.NewTokenProvider(operatorConfig.EnvoyHost, operatorConfig.EnvoyPort, operatorConfig.SsoDomain, operatorConfig.VCUser, operatorConfig.VCPassword, vcCaCert, operatorConfig.Insecure, "http")
		} else {
			tokenProvider, _ = jwt.NewTokenProvider(operatorConfig.VCEndPoint, operatorConfig.HttpsPort, operatorConfig.SsoDomain, operatorConfig.VCUser, operatorConfig.VCPassword, vcCaCert, operatorConfig.Insecure, "https")
		}
	}
	return nil
}

func (vcConfig *VCConfig) validate() error {
	if len(vcConfig.VCEndPoint) == 0 {
		err := errors.New("invalid field " + "VcEndPoint")
		configLog.Info("validate VcConfig failed", "VcEndPoint", vcConfig.VCEndPoint)
		return err
	}

	if len(vcConfig.SsoDomain) == 0 {
		err := errors.New("invalid field " + "SsoDomain")
		configLog.Info("validate VcConfig failed", "SsoDomain", vcConfig.SsoDomain)
		return err
	}

	if vcConfig.HttpsPort == 0 {
		err := errors.New("invalid field " + "HttpsPort")
		configLog.Info("validate VcConfig failed", "HttpsPort", vcConfig.HttpsPort)
		return err
	}
	// VCPassword, VCUser should be both empty or valid
	if !((len(vcConfig.VCPassword) > 0) == (len(vcConfig.VCUser) > 0)) {
		err := errors.New("invalid field " + "VCUser, VCPassword")
		configLog.Info("validate VcConfig failed VCUser %s VCPassword %s", vcConfig.VCUser, vcConfig.VCPassword)
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
	nsxConfig.LeafCertFile = removeEmptyItem(nsxConfig.LeafCertFile)
	mCount := len(nsxConfig.NsxApiManagers)
	tpCount := len(nsxConfig.Thumbprint)
	// Prefer LeafCertFile, otherwise fallback to CaFile
	caCount := len(nsxConfig.CaFile)
	ca := nsxConfig.CaFile
	if len(nsxConfig.LeafCertFile) > 0 {
		caCount = len(nsxConfig.LeafCertFile)
		ca = nsxConfig.LeafCertFile
	}

	// ca file has high priority than thumbprint
	// ca file(thumbprint) == 1 or equal to manager count
	if caCount == 0 && tpCount == 0 && nsxConfig.NsxApiUser == "" && nsxConfig.NsxApiPassword == "" {
		err := errors.New("no ca file or thumbprint or nsx username/password provided")
		configLog.Error(err, "validate NsxConfig failed")
		return err
	}
	if nsxConfig.EnvoyPort != 0 && caCount == 0 && tpCount == 0 {
		err := errors.New("no ca file or thumbprint while using envoy mode")
		configLog.Error(err, "validate NsxConfig failed")
		return err
	}
	if caCount > 0 {
		configLog.Infof("validate CA file: %s", caCount)
		if caCount > 1 && caCount != mCount {
			err := errors.New("ca or cert file count not match manager count")
			configLog.Error(err, "validate NsxConfig failed", "cert count", caCount, "manager count", mCount)
			return err
		}
		for _, file := range ca {
			// caFile should be a existed cert filename or raw content of a cert
			if _, err := os.Stat(file); !os.IsNotExist(err) {
				continue
			}
			block, _ := pem.Decode([]byte(file))
			if block == nil || block.Type != "CERTIFICATE" {
				err := fmt.Errorf("ca or cert file does not exist or not a valid cert %s", file)
				configLog.Error(err, "validate NsxConfig failed")
				return err
			}
			_, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				err := fmt.Errorf("ca or cert file does not exist or not a valid cert %s", file)
				configLog.Error(err, "validate NsxConfig failed")
				return err
			}
		}
	} else {
		configLog.Infof("validate thumbprint: %s", tpCount)
		if tpCount > 1 && tpCount != mCount {
			err := errors.New("thumbprint count not match manager count")
			configLog.Error(err, "validate NsxConfig failed", "thumbprint count", tpCount, "manager count", mCount)
			return err
		}
	}
	return nil
}

func (nsxConfig *NsxConfig) validate(enableVPC bool) error {
	nsxConfig.NsxApiManagers = removeEmptyItem(nsxConfig.NsxApiManagers)
	mCount := len(nsxConfig.NsxApiManagers)
	if mCount == 0 {
		err := errors.New("invalid field " + "NsxApiManagers")
		configLog.Error(err, "validate NsxConfig failed", "NsxApiManagers", nsxConfig.NsxApiManagers)
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
		configLog.Error(err, "validate coeConfig failed")
		return err
	}
	return nil
}

func (nsxConfig *NsxConfig) ValidateConfigFromCmd() error {
	return nsxConfig.validate(true)
}

func (nsxConfig *NsxConfig) GetNSXLBSize() string {
	lbsSize := nsxConfig.NSXLBSize
	if lbsSize == "" {
		lbsSize = model.LBService_SIZE_SMALL
	}
	return lbsSize
}
