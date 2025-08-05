// Copyright © 2019-2021 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package framework

import (
	"fmt"

	"github.com/vmware-tanzu/nsx-operator/test/e2e/providers"
)

// Provider is the interface for interacting with the test environment
var Provider providers.ProviderInterface

// InitProvider initializes the provider based on the test options
func InitProvider(options *TestOptions) error {
	providerFactory := map[string]func(string) (providers.ProviderInterface, error){
		"remote": providers.NewRemoteProvider,
	}
	if fn, ok := providerFactory[options.ProviderName]; ok {
		if newProvider, err := fn(options.ProviderConfigPath); err != nil {
			return err
		} else {
			Provider = newProvider
		}
	} else {
		return fmt.Errorf("unknown provider '%s'", options.ProviderName)
	}
	return nil
}

// RunCommandOnNode is a convenience wrapper around the Provider interface RunCommandOnNode method.
func RunCommandOnNode(nodeName string, cmd string) (code int, stdout string, stderr string, err error) {
	return Provider.RunCommandOnNode(nodeName, cmd)
}
