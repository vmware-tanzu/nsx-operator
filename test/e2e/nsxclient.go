// Copyright © 2019-2021 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package e2e

import (
	"fmt"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

type NSXClient struct {
	*nsx.Client
}

func NewNSXClient(configFile string) (*NSXClient, error) {
	// nsxClient is used to interact with NSX API.
	config.UpdateConfigFilePath(configFile)
	cf, err := config.NewNSXOperatorConfigFromFile()
	if err != nil {
		return nil, err
	}
	client := nsx.GetClient(cf)
	if client == nil {
		return nil, fmt.Errorf("failed to get nsx client")
	}
	nsxClient := &NSXClient{}
	nsxClient.Client = client
	return nsxClient, nil
}
