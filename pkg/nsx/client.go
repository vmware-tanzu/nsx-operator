/* Copyright © 2020 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	policyclient "github.com/vmware/vsphere-automation-sdk-go/runtime/protocol/client"
	"strings"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
)

func NewClient() *policyclient.RestConnector {
	cf, _ := config.NewNSXOperatorConfigFromFile()
	c := NewConfig(strings.Join(cf.NsxApiManagers, ","), cf.NsxApiUser, cf.NsxApiPassword, "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil)
	cluster, _ := NewCluster(c)
	connector, _ := cluster.NewRestConnector()
	return connector
}
