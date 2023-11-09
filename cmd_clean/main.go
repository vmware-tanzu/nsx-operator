/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package main

import (
	"flag"
	"os"

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/vmware-tanzu/nsx-operator/pkg/clean"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
)

// usage:
// ./bin/clean -cluster=''  -thumbprint="" -log-level=0 -vc-user="" -vc-passwd="" -vc-endpoint="" -vc-sso-domain="" -vc-https-port=443  -mgr-ip=""

var (
	log         = logger.Log
	cf          *config.NSXOperatorConfig
	mgrIp       string
	vcEndpoint  string
	vcUser      string
	vcPasswd    string
	nsxUser     string
	nsxPasswd   string
	vcSsoDomain string
	vcHttpsPort int
	thumbprint  string
	caFile      string
	cluster     string
)

func main() {
	flag.StringVar(&vcEndpoint, "vc-endpoint", "", "nsx manager ip")
	flag.StringVar(&vcSsoDomain, "vc-sso-domain", "", "nsx manager ip")
	flag.StringVar(&mgrIp, "mgr-ip", "", "nsx manager ip")
	flag.StringVar(&vcUser, "vc-user", "", "vc username")
	flag.StringVar(&vcPasswd, "vc-passwd", "", "vc password")
	flag.IntVar(&vcHttpsPort, "vc-https-port", 443, "vc https port")
	flag.StringVar(&thumbprint, "thumbprint", "", "nsx thumbprint")
	flag.StringVar(&nsxUser, "nsx-user", "", "nsx username")
	flag.StringVar(&nsxPasswd, "nsx-passwd", "", "nsx password")
	flag.StringVar(&caFile, "ca-file", "", "ca file")
	flag.StringVar(&cluster, "cluster", "", "cluster name")
	flag.IntVar(&config.LogLevel, "log-level", 0, "Use zap-core log system.")
	flag.Parse()

	cf = config.NewNSXOpertorConfig()
	cf.NsxApiManagers = []string{mgrIp}
	cf.VCUser = vcUser
	cf.VCPassword = vcPasswd
	cf.VCEndPoint = vcEndpoint
	cf.NsxApiUser = nsxUser
	cf.NsxApiPassword = nsxPasswd
	cf.SsoDomain = vcSsoDomain
	cf.HttpsPort = vcHttpsPort
	cf.Thumbprint = []string{thumbprint}
	cf.CaFile = []string{caFile}
	cf.Cluster = cluster
	logf.SetLogger(logger.ZapLogger(cf))

	// the error roughly are:
	// 1. failed to validate config
	// 2. failed to get nsx client
	// 3. failed to initialize cleanup service
	// 4. failed to clean up specific resource
	err := clean.Clean(cf)
	if err != nil {
		log.Error(err, "failed to clean nsx resources")
		os.Exit(1)
	}
	os.Exit(0)
}
