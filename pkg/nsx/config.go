// Copyright (c) 2021 VMware, Inc. All rights reserved. VMware Confidential.

package nsx

import (
	"strings"

	"gitlab.eng.vmware.com/nsx-container-plugin/vmware-nsxlib-go/pkg/nsx/auth"
	"gitlab.eng.vmware.com/nsx-container-plugin/vmware-nsxlib-go/pkg/nsx/ratelimiter"
)

// Config holds all the configuration parameters used by the nsxlib code.
//  apiManagers: List of IP addresses of the NSX managers.
//               Each IP address should be of the form:
//               [<scheme>://]<ip_address>[:<port>]
//               If scheme is not provided https is used.
//               If port is not provided port 80 is used for http
//               and port 443 for https.
//  param username: User name for the NSX manager
//  param password: Password for the NSX manager
//  param clientCertProvider: None, or ClientCertProvider object.
//                            If specified, nsxlib will use client cert auth
//                            instead of basic authentication.
//   param insecure: If true, the NSX Manager server certificate is not
//                   verified. If false the CA bundle specified via "caFile"
//                   will be used or if unset the "thumbprint" will be used.
//                   If "thumbprint" is unset, the default system root CAs
//                   will be used.
//   param caFile: Specify a CA bundle file to use in verifying the NSX
//                 Manager server certificate. This option is ignored if
//                 "insecure" is set to True. If "insecure" is set to False
//                 and "caFile" is unset, the "thumbprint" will be used.
//                 If "thumbprint" is unset, the system root CAs will be
//                 used to verify the server certificate.
//   param thumbprint: Specify a thumbprint string to use in verifying the
//                     NSX Manager server certificate. This option is ignored
//                     if "insecure" is set to True or "caFile" is defined.
//   param tokenProvider: None, or instance of implemented AbstractJWTProvider
//                        which will return the JSON Web Token used in the
//                        requests in NSX for authorization.
//   param concurrentConnections: Maximum concurrent connections to each NSX
//                                manager.
//   param retries: Maximum number of times to retry a HTTP connection.
//   param httpTimeout: The time in seconds before aborting a HTTP connection
//                      to a NSX manager.
//   param connIdleTimeout: The amount of time in seconds to wait before
//                          ensuring connectivity to the NSX manager if no
//                          manager connection has been used.
//   param allowOverwriteHeader: If True, a default header of
//                               X-Allow-Overwrite:true will be added to all
//                               the requests, to allow admin user to update/
//                               delete all entries.
//   param rateLimitRetry: If True, the client will retry requests failed on
//                         "Too many requests" error.
//   param clusterUnavailableRetry: If True, skip fatal errors when no
//                                  endpoint in the NSX management cluster is
//                                  available to serve a request, and retry
//                                  the request instead. This setting can
//                                  not be False if single endpoint is
//                                  configured in the cluster, since there
//                                  will be no keepalive probes in this
//                                  case.
//   param apiRateLimitPerEndpoint: If set to positive integer, API calls
//                                  sent to each endpoint will be limited
//                                  to a max rate of this value per second.
//                                  The rate limit is not enforced on
//                                  connection validations. This option
//                                  defaults to None, which disables rate
//                                  limit.
//   param apiRateMode: Algorithm used to adaptively adjust max API rate
//                      limit. If not set, the max rate will not be
//                      automatically changed. If set to 'AIMD', max API
//                      rate will be increase by 1 after successful calls
//                      that was blocked before sent, and will be decreased
//                      by half after 429/503 error for each period.
//                      The rate has hard max limit of min(100/s, param
//                      api_rate_limit_per_endpoint).
//   param allowPassthrough: If True, use nsx manager api for cases which are
//                           not supported by the policy manager api.
type Config struct {
	apiManagers           []string
	username              string
	password              string
	caFile                string
	thumbprint            string
	concurrentConnections int
	retries               int
	httpTimeout           int
	connIdleTimeout       int
	insecure              bool
	allowOverwriteHeader  bool
	allowPassThrough      bool
	apiRateMode           ratelimiter.RateLimiterType
	tokenProvider         auth.TokenProvider
	clientCertProvider    auth.ClientCertProvider
}

// NewConfig creates a nsxlib configuration. It provides default values for those items not in function parameters.
func NewConfig(apiManagers, username, password, caFile string, concurrentConnections, retries, httpTimeout, connIdleTimeout int, insecure, allowOverwriteHeader, allowPassThrough bool, apiRateMode ratelimiter.RateLimiterType, tokenProvider auth.TokenProvider, clientCertProvider auth.ClientCertProvider) *Config {
	apis := strings.Split(apiManagers, ",")
	for i, v := range apis {
		apis[i] = strings.TrimSpace(v)
	}
	conf := &Config{
		apiManagers:           apis,
		username:              username,
		password:              password,
		insecure:              insecure,
		concurrentConnections: concurrentConnections,
		retries:               retries,
		httpTimeout:           httpTimeout,
		connIdleTimeout:       connIdleTimeout,
		apiRateMode:           apiRateMode,
		allowOverwriteHeader:  allowOverwriteHeader,
		allowPassThrough:      allowPassThrough,
		tokenProvider:         tokenProvider,
		clientCertProvider:    clientCertProvider,
	}
	return conf
}

// HTTPTimeout returns HTTP timeout.
func (conf *Config) HTTPTimeout() int {
	return conf.httpTimeout
}

// APIRateMode returns the rate limiter type.
func (conf *Config) APIRateMode() ratelimiter.RateLimiterType {
	return conf.apiRateMode
}

// TokenProvider returns TokenProvider.
func (conf *Config) TokenProvider() auth.TokenProvider {
	return conf.tokenProvider
}

// CertProvider returns ClientCertProvider.
func (conf *Config) CertProvider() auth.ClientCertProvider {
	return conf.clientCertProvider
}

// AllowOverwriteHeader returns allowOverwriteHeader.
func (conf *Config) AllowOverwriteHeader() bool {
	return conf.allowOverwriteHeader
}

// Username returns username.
func (conf *Config) Username() string {
	return conf.username
}

// Password returns password.
func (conf *Config) Password() string {
	return conf.password
}

// ConnIdleTimeout returns connIdleTimeout.
func (conf *Config) ConnIdleTimeout() int {
	return conf.connIdleTimeout
}

// APIManagers returns apiManagers.
func (conf *Config) APIManagers() []string {
	return conf.apiManagers
}
