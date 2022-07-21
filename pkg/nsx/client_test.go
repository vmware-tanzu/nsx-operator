/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/agiledragon/gomonkey"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
)

func TestNSXHealthChecker_CheckNSXHealth(t *testing.T) {
	host := "1.1.1.1"
	config := NewConfig(host, "1", "1", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := NewCluster(config)
	req := &http.Request{}

	res := []ClusterHealth{GREEN, RED, ORANGE}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(cluster), "Health", func(_ *Cluster) ClusterHealth {
		return RED
	})
	patches.Reset()
	type fields struct {
		cluster *Cluster
	}
	type args struct {
		req *http.Request
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{"1", fields{cluster: cluster}, args{req}, false},
		{"2", fields{cluster: cluster}, args{req}, true},
		{"3", fields{cluster: cluster}, args{req}, false},
	}
	for idx, tt := range tests {
		patches.ApplyMethod(reflect.TypeOf(cluster), "Health", func(_ *Cluster) ClusterHealth {
			return res[idx]
		})
		t.Run(tt.name, func(t *testing.T) {
			ck := &NSXHealthChecker{
				cluster: tt.fields.cluster,
			}
			if err := ck.CheckNSXHealth(tt.args.req); (err != nil) != tt.wantErr {
				t.Errorf("NSXHealthChecker.CheckNSXHealth() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
		patches.Reset()
	}
}

func TestGetClient(t *testing.T) {
	cf := config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{NsxApiUser: "1", NsxApiPassword: "1"}}
	cf.VCConfig = &config.VCConfig{}
	client := GetClient(&cf)
	assert.True(t, client == nil)

	cluster := &Cluster{}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(cluster), "GetVersion", func(_ *Cluster) (*NsxVersion, error) {
		nsxVersion := &NsxVersion{NodeVersion: "3.1.1"}
		return nsxVersion, nil
	})

	client = GetClient(&cf)
	patches.Reset()
	assert.True(t, client == nil)

	patches = gomonkey.ApplyMethod(reflect.TypeOf(cluster), "GetVersion", func(_ *Cluster) (*NsxVersion, error) {
		nsxVersion := &NsxVersion{NodeVersion: "3.2.1"}
		return nsxVersion, nil
	})
	client = GetClient(&cf)
	patches.Reset()
	assert.True(t, client != nil)
}

func IsInstanceOf(objectPtr, typePtr interface{}) bool {
	return reflect.TypeOf(objectPtr) == reflect.TypeOf(typePtr)
}
