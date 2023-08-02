/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"strings"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/auth"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
)

// Config holds all the configuration parameters used by the nsx code.
type Config struct {
	// List of IP addresses of the NSX managers. Each address should be of the form:[<scheme>://]<ip_address>[:<port>]
	// If scheme is not provided https is used. If port is not provided port 80 is used for http and port 443 for
	// https.
	APIManagers []string
	// User name for the NSX manager.
	Username string
	// Password for the NSX manager.
	Password string
	// Specify a CA bundle file to use in verifying the NSX Manager server certificate. This option is ignored if
	// "Insecure" is set to True. If "Insecure" is set to False and "CAFile" is unset, the "Thumbprint" will be used.
	// If "Thumbprint" is unset, the system root CAs will be used to verify the server certificate.
	CAFile []string
	// Specify a Thumbprint string to use in verifying the NSX Manager server certificate. This option is ignored
	// if "Insecure" is set to True or "CAFile" is defined.
	Thumbprint []string
	// Maximum concurrent connections to each NSX manager.
	ConcurrentConnections int
	// If True, the client will retry requests failed on "Too many requests" error.
	Retries int
	// The time in seconds before aborting a HTTP connection to a NSX manager.
	HTTPTimeout int
	// The amount of time in seconds to wait before ensuring connectivity to the NSX manager if no manager connection
	// has been used.
	ConnIdleTimeout int
	// If true, the NSX Manager server certificate is not verified. If false the CA bundle specified via "CAFile"
	// will be used or if unset the "Thumbprint" will be used. If "Thumbprint" is unset, the default system root CAs
	// will be used.
	Insecure bool
	// If True, a default header of X-Allow-Overwrite:true will be added to all the requests, to allow admin user to
	// update/delete all entries.
	AllowOverwriteHeader bool
	// If True, use nsx manager api for cases which are not supported by the policy manager api.
	AllowPassThrough bool
	// Algorithm used to adaptively adjust max API rate limit. If not set, the max rate will not be automatically
	// changed. If set to 'AIMD', max API rate will be increase by 1 after successful calls that was blocked before
	// sent, and will be decreased by half after 429/503 error for each period. The rate has hard max limit of
	// min(100/s, param api_rate_limit_per_endpoint).
	APIRateMode ratelimiter.Type
	// None, or instance of implemented AbstractJWTProvider which will return the JSON Web Token used in the requests
	// in NSX for authorization.
	TokenProvider auth.TokenProvider
	// None, or ClientCertProvider object. If specified, client cert will be used instead of basic authentication.
	ClientCertProvider auth.ClientCertProvider
}

// NewConfig creates a nsx configuration. It provides default values for those items not in function parameters.
func NewConfig(apiManagers, username, password string, caFile []string, concurrentConnections, retries, httpTimeout, connIdleTimeout int, insecure, allowOverwriteHeader, allowPassThrough bool, apiRateMode ratelimiter.Type, tokenProvider auth.TokenProvider, clientCertProvider auth.ClientCertProvider, thumbprint []string) *Config {
	apis := strings.Split(apiManagers, ",")
	for i, v := range apis {
		apis[i] = strings.TrimSpace(v)
	}
	return &Config{
		APIManagers:           apis,
		Username:              username,
		Password:              password,
		Insecure:              insecure,
		ConcurrentConnections: concurrentConnections,
		Retries:               retries,
		HTTPTimeout:           httpTimeout,
		ConnIdleTimeout:       connIdleTimeout,
		APIRateMode:           apiRateMode,
		AllowOverwriteHeader:  allowOverwriteHeader,
		AllowPassThrough:      allowPassThrough,
		TokenProvider:         tokenProvider,
		ClientCertProvider:    clientCertProvider,
		CAFile:                caFile,
		Thumbprint:            thumbprint,
	}
}
