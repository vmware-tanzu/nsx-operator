/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"net/http"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/protocol/client"
)

// HeaderConfig updates http request header.
type HeaderConfig struct {
	xAllowOverwrite       bool
	nsxEnablePartialPatch bool
	// configXallowOverwrite comes from config, it's a global parameter.
	configXallowOverwrite bool
}

// CreateHeaderConfig creates HeaderConfig.
func CreateHeaderConfig(xAllowOverwrite bool, nsxEnablePartialPatch bool, configXallowOverwrite bool) *HeaderConfig {
	header := &HeaderConfig{
		xAllowOverwrite:       xAllowOverwrite,
		nsxEnablePartialPatch: nsxEnablePartialPatch,
		configXallowOverwrite: configXallowOverwrite,
	}
	return header
}

// Process adds header to http.Request depending on configuration.
func (headerConfig *HeaderConfig) Process(req *http.Request) error {
	if headerConfig.configXallowOverwrite {
		if headerConfig.xAllowOverwrite {
			req.Header["X-Allow-Overwrite"] = []string{"true"}
		}
	}
	if headerConfig.nsxEnablePartialPatch {
		req.Header.Set("nsx-enable-partial-patch", "true")
	}
	return nil
}

// SetXAllowOverrite sets XAllowoverrite.
func (headerConfig *HeaderConfig) SetXAllowOverrite(value bool) *HeaderConfig {
	headerConfig.xAllowOverwrite = value
	return headerConfig
}

// SetNSXEnablePartialPatch sets NSXEnablePartialPatch.
func (headerConfig *HeaderConfig) SetNSXEnablePartialPatch(value bool) *HeaderConfig {
	headerConfig.nsxEnablePartialPatch = value
	return headerConfig
}

// SetConfigXallowOverwrite sets configXallowOverwrite.
func (headerConfig *HeaderConfig) SetConfigXallowOverwrite(value bool) *HeaderConfig {
	headerConfig.configXallowOverwrite = value
	return headerConfig
}

// Done updates request process of RestConnector.
func (headerConfig *HeaderConfig) Done(connector *client.RestConnector) {
	connector.AddRequestProcessor(headerConfig)
}
