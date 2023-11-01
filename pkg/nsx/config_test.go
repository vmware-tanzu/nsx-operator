/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"reflect"
	"testing"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/auth"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
)

func TestNewConfig(t *testing.T) {
	type args struct {
		apiManagers           string
		username              string
		password              string
		caFile                []string
		concurrentConnections int
		retries               int
		httpTimeout           int
		connIdleTimeout       int
		insecure              bool
		allowOverwriteHeader  bool
		allowPassThrough      bool
		apiRateMode           ratelimiter.Type
		tokenProvider         auth.TokenProvider
		clientCertProvider    auth.ClientCertProvider
		thumbprint            []string
	}
	tests := []struct {
		name string
		args args
		want *Config
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewConfig(tt.args.apiManagers, tt.args.username, tt.args.password, tt.args.caFile, tt.args.concurrentConnections, tt.args.retries, tt.args.httpTimeout, tt.args.connIdleTimeout, tt.args.insecure, tt.args.allowOverwriteHeader, tt.args.allowPassThrough, tt.args.apiRateMode, tt.args.tokenProvider, tt.args.clientCertProvider, tt.args.thumbprint); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}
