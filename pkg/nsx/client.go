/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"strings"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"
)

type NSXClient struct {
	//TODO Init various client
	QueryClient search.QueryClient
}

func GetClient() *NSXClient{
	cf, _ := config.NewNSXOperatorConfigFromFile()
	c := NewConfig(strings.Join(cf.NsxApiManagers, ","), cf.NsxApiUser, cf.NsxApiPassword, "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil)
	cluster, _ := NewCluster(c)
	connector, _ := cluster.NewRestConnector()
	queryClient := search.NewQueryClient(connector)
	return &NSXClient{QueryClient: queryClient}
}
