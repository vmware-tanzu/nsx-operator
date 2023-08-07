/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package main

import (
	"context"
	"flag"
	"os"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/vmware-tanzu/nsx-operator/pkg/clean"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
)

// usage:
// normal mode:
//
// ./bin/clean -cluster=”  -thumbprint="" -log-level=0 -vc-user="" -vc-passwd="" -vc-endpoint="" -vc-sso-domain="" -vc-https-port=443  -mgr-ip=""
//
// envoy ca file mode:
//
//	./clean -cluster=domain-c9:d75735a3-2847-45d2-a652-ef2d146afd54 -nsx-user=admin -nsx-passwd='xxx'  -mgr-ip=nsxmanager-ob-22386469-1-dev-integ-nsxt-8791 -envoyhost=localhost -envoyport=1080 -log-level=1 -ca-file=./ca.cert
//
// envoy thumbprint mode:
//
//	./clean -cluster=domain-c9:d75735a3-2847-45d2-a652-ef2d146afd54 -nsx-user=admin -nsx-passwd='xxx'  -mgr-ip=nsxmanager-ob-22386469-1-dev-integ-nsxt-8791 -envoyhost=localhost -envoyport=1080 -log-level=1 -thumbprint=8bc2fa2b5879c27b1180fa44e5f747832f2ded6be483e3c3d2c4816a38870868
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
	envoyHost   string
	envoyPort   int
)

func main() {
	flag.StringVar(&vcEndpoint, "vc-endpoint", "", "vc endpoint")
	flag.StringVar(&vcSsoDomain, "vc-sso-domain", "", "vc sso domain")
	flag.StringVar(&mgrIp, "mgr-ip", "", "nsx manager ip, it should be host name if want to verify cert")
	flag.StringVar(&vcUser, "vc-user", "", "vc username")
	flag.StringVar(&vcPasswd, "vc-passwd", "", "vc password")
	flag.IntVar(&vcHttpsPort, "vc-https-port", 443, "vc https port")
	flag.StringVar(&thumbprint, "thumbprint", "", "nsx thumbprint")
	flag.StringVar(&nsxUser, "nsx-user", "", "nsx username")
	flag.StringVar(&nsxPasswd, "nsx-passwd", "", "nsx password")
	flag.StringVar(&caFile, "ca-file", "", "ca file")
	flag.StringVar(&cluster, "cluster", "", "cluster name")
	flag.StringVar(&envoyHost, "envoyhost", "", "envoy host")
	flag.IntVar(&envoyPort, "envoyport", 0, "envoy port")
	flag.IntVar(&config.LogLevel, "log-level", 0, "Use zap-core log system.")
	flag.Parse()

	logf.SetLogger(logger.ZapLogger())
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
	cf.EnvoyHost = envoyHost
	cf.EnvoyPort = envoyPort

	logf.SetLogger(logger.ZapLogger(cf.DefaultConfig.Debug, config.LogLevel))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
	defer cancel()

	err := clean.Clean(ctx, cf)
	if err != nil {
		log.Error(err, "failed to clean nsx resources")
		os.Exit(1)
	}
	os.Exit(0)
}
