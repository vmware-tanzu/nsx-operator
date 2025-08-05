// Copyright © 2019-2021 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package clients

import (
	"fmt"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

// NSXClient wraps the nsx.Client to provide NSX API interaction
type NSXClient struct {
	*nsx.Client
}

// NewNSXClient creates a new NSXClient instance
func NewNSXClient(cf *config.NSXOperatorConfig) (*NSXClient, error) {
	// nsxClient is used to interact with NSX API.
	client := nsx.GetClient(cf)
	if client == nil {
		return nil, fmt.Errorf("failed to get nsx client")
	}
	nsxClient := &NSXClient{}
	nsxClient.Client = client
	return nsxClient, nil
}