/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/auth"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/auth/jwt"
	ini "gopkg.in/ini.v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

//TODO replace to yaml
const (
	nsxOperatorDefaultConf = "/etc/nsx-operator/nsxop.ini"
)

var (
	configFilePath = ""
	log            = logf.Log.WithName("config")
	minVersion     = [3]int64{3, 2, 0}
	tokenProvider  auth.TokenProvider
)

//TODO delete unnecessary config

type NSXOperatorConfig struct {
	*CoeConfig
	*NsxConfig
	*K8sConfig
	*VCConfig
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
		log.Error(err, "failed to load default NSX Operator configuration")
		return nil, err
	}
	cfg, err = ini.Load(configFilePath)
	if err != nil {
		log.Error(err, "failed to load NSX Operator configuration from file")
		return nil, err
	}
	err = cfg.Section("coe").MapTo(nsxOperatorConfig.CoeConfig)
	if err != nil {
		log.Error(err, "failed to parse coe section from NSX Operator config, please check the configuration")
		return nil, err
	}
	err = cfg.Section("nsx_v3").MapTo(nsxOperatorConfig.NsxConfig)
	if err != nil {
		log.Error(err, "failed to parse nsx section from NSX Operator config, please check the configuration")
		return nil, err
	}
	err = cfg.Section("k8s").MapTo(nsxOperatorConfig.K8sConfig)
	if err != nil {
		log.Error(err, "failed to parse k8s section from NSX Operator config, please check the configuration")
		return nil, err
	}
	err = cfg.Section("vc").MapTo(nsxOperatorConfig.VCConfig)
	if err != nil {
		log.Error(err, "failed to parse vc section from NSX Operator config, please check the configuration")
		return nil, err
	}

	if err := nsxOperatorConfig.validate(); err != nil {
		log.Error(err, "failed to validate NSX Operator config, please check the configuration")
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

	return operatorConfig.validateVersion()
}

func (operatorConfig *NSXOperatorConfig) GetTokenProvider() auth.TokenProvider {
	return tokenProvider
}

func (operatorConfig *NSXOperatorConfig) validateVersion() error {
	nsxVersion := &NsxVersion{}
	host := operatorConfig.NsxApiManagers[0]
	if err := operatorConfig.VCConfig.validate(); err == nil {
		tokenProvider, _ = jwt.NewTokenProvider(operatorConfig.VCEndPoint, operatorConfig.HttpsPort, operatorConfig.SsoDomain, nil)
	} else {
		tokenProvider = nil
	}
	if err := nsxVersion.getVersion(host, operatorConfig.NsxApiUser, operatorConfig.NsxApiPassword, tokenProvider); err != nil {
		return err
	}
	if err := nsxVersion.validate(); err != nil {
		return err
	}
	return nil
}

func (vcConfig *VCConfig) validate() error {
	if len(vcConfig.VCEndPoint) == 0 {
		err := errors.New("invalid field " + "VcEndPoint")
		log.Error(err, "validate VcConfig failed", "VcEndPoint", vcConfig.VCEndPoint)
		return err
	}

	if len(vcConfig.SsoDomain) == 0 {
		err := errors.New("invalid field " + "SsoDomain")
		log.Error(err, "validate VcConfig failed", "SsoDomain", vcConfig.SsoDomain)
		return err
	}

	if vcConfig.HttpsPort == 0 {
		err := errors.New("invalid field " + "HttpsPort")
		log.Error(err, "validate VcConfig failed", "HttpsPort", vcConfig.HttpsPort)
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

func (nsxVersion *NsxVersion) validate() error {
	if !nsxVersion.featureSupported() {
		version := fmt.Sprintf("%d:%d:%d", minVersion[0], minVersion[1], minVersion[2])
		err := errors.New("nsxt version " + nsxVersion.NodeVersion + " is old this feature needs version " + version)
		log.Error(err, "validate NsxVersion failed")
		return err
	}
	return nil
}

func (nsxVersion *NsxVersion) featureSupported() bool {
	// only compared major.minor.patch
	// NodeVersion should have at least three sections
	// each section only have digital value
	buff := strings.Split(nsxVersion.NodeVersion, ".")
	sections := make([]int64, len(buff))
	for i, str := range buff {
		val, err := strconv.ParseInt(str, 10, 64)
		if err != nil {
			log.Error(err, "parse version error")
			return false
		}
		sections[i] = val
	}

	for i := 0; i < 3; i++ {
		if sections[i] > minVersion[i] {
			return true
		}
		if sections[i] < minVersion[i] {
			return false
		}
	}
	return true
}

func (nsxVersion *NsxVersion) getVersion(host string, userName string, password string, tokenProvider auth.TokenProvider) error {
	tlsConfig := tls.Config{InsecureSkipVerify: true}
	tr := &http.Transport{
		TLSClientConfig: &tlsConfig,
		IdleConnTimeout: 60 * time.Second,
	}
	client := http.Client{
		Transport: tr,
		Timeout:   60 * time.Second,
	}
	if !strings.HasPrefix(host, "http") {
		host = "https://" + host
	}
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/node/version", host), nil)
	if err != nil {
		log.Error(err, "failed to create http request")
		return err
	}
	if tokenProvider != nil {
		token, err := tokenProvider.GetToken(false)
		if err != nil {
			log.Error(err, "retrieving JSON Web Token eror")
			return err
		}
		bearerToken := tokenProvider.HeaderValue(token)
		req.Header.Add("Authorization", bearerToken)
		req.Header.Add("Accept", "application/json")
		req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req.SetBasicAuth(userName, password)
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Error(err, "failed to get nsx version")
		return err
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil || body == nil {
		log.Error(err, "failed to read response body")
		return err
	}

	if err := json.Unmarshal(body, nsxVersion); err != nil {
		log.Error(err, "failed to convert HTTP response to NsxVersion")
		return err
	}
	return nil
}
