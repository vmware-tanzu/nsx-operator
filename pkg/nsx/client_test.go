/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/agiledragon/gomonkey"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
)

func TestNSXHealthChecker_CheckNSXHealth(t *testing.T) {
	host := "1.1.1.1"
	config := NewConfig(host, "1", "1", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
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
	assert.True(t, client != nil)

	cluster := &Cluster{}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(cluster), "GetVersion", func(_ *Cluster) (*NsxVersion, error) {
		nsxVersion := &NsxVersion{NodeVersion: "3.1.1"}
		return nsxVersion, nil
	})

	client = GetClient(&cf)
	patches.Reset()
	assert.True(t, client != nil)
	securityPolicySupported := client.NSXCheckVersion(SecurityPolicy)
	assert.True(t, securityPolicySupported == false)
	assert.False(t, client.NSXCheckVersion(ServiceAccount))
	assert.False(t, client.NSXCheckVersion(ServiceAccountRestore))
	assert.False(t, client.NSXCheckVersion(ServiceAccountCertRotation))

	patches = gomonkey.ApplyMethod(reflect.TypeOf(cluster), "GetVersion", func(_ *Cluster) (*NsxVersion, error) {
		nsxVersion := &NsxVersion{NodeVersion: "3.2.1"}
		return nsxVersion, nil
	})
	client = GetClient(&cf)
	patches.Reset()
	assert.True(t, client != nil)
	securityPolicySupported = client.NSXCheckVersion(SecurityPolicy)
	assert.True(t, securityPolicySupported == true)
	assert.False(t, client.NSXCheckVersion(ServiceAccount))
	assert.False(t, client.NSXCheckVersion(ServiceAccountRestore))
	assert.False(t, client.NSXCheckVersion(ServiceAccountCertRotation))

	patches = gomonkey.ApplyMethod(reflect.TypeOf(cluster), "GetVersion", func(_ *Cluster) (*NsxVersion, error) {
		nsxVersion := &NsxVersion{NodeVersion: "4.1.0"}
		return nsxVersion, nil
	})
	client = GetClient(&cf)
	patches.Reset()
	assert.True(t, client != nil)
	securityPolicySupported = client.NSXCheckVersion(SecurityPolicy)
	assert.True(t, securityPolicySupported == true)
	assert.True(t, client.NSXCheckVersion(ServiceAccount))
	assert.False(t, client.NSXCheckVersion(ServiceAccountRestore))
	assert.False(t, client.NSXCheckVersion(ServiceAccountCertRotation))

	patches = gomonkey.ApplyMethod(reflect.TypeOf(cluster), "GetVersion", func(_ *Cluster) (*NsxVersion, error) {
		nsxVersion := &NsxVersion{NodeVersion: "4.1.2"}
		return nsxVersion, nil
	})
	client = GetClient(&cf)
	patches.Reset()
	assert.True(t, client != nil)
	securityPolicySupported = client.NSXCheckVersion(SecurityPolicy)
	assert.True(t, securityPolicySupported == true)
	assert.True(t, client.NSXCheckVersion(ServiceAccount))
	assert.True(t, client.NSXCheckVersion(ServiceAccountRestore))
	assert.False(t, client.NSXCheckVersion(ServiceAccountCertRotation))

	patches = gomonkey.ApplyMethod(reflect.TypeOf(cluster), "GetVersion", func(_ *Cluster) (*NsxVersion, error) {
		nsxVersion := &NsxVersion{NodeVersion: "4.1.3"}
		return nsxVersion, nil
	})
	client = GetClient(&cf)
	patches.Reset()
	assert.True(t, client != nil)
	securityPolicySupported = client.NSXCheckVersion(SecurityPolicy)
	assert.True(t, securityPolicySupported == true)
	assert.True(t, client.NSXCheckVersion(ServiceAccount))
	assert.True(t, client.NSXCheckVersion(ServiceAccountRestore))
	assert.True(t, client.NSXCheckVersion(ServiceAccountCertRotation))
}

func IsInstanceOf(objectPtr, typePtr interface{}) bool {
	return reflect.TypeOf(objectPtr) == reflect.TypeOf(typePtr)
}

func TestSRGetClient(t *testing.T) {
	cf := config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{NsxApiUser: "admin", NsxApiPassword: "Admin!23Admin", NsxApiManagers: []string{"10.173.82.128"}}}
	cf.VCConfig = &config.VCConfig{}
	client := GetClient(&cf)
	st, error := client.StaticRouteClient.Get("default", "project-1", "vpc-2", "site1")
	if error == nil {
		fmt.Printf("sr %v\n", *st.ResourceType)
	} else {
		fmt.Printf("error %v\n", error)
	}
	st1 := st
	ip := "10.0.0.2"
	dis := int64(1)
	nexthop := model.RouterNexthop{IpAddress: &ip, AdminDistance: &dis}
	st1.NextHops = append(st1.NextHops, nexthop)
	st, error = client.StaticRouteClient.Update("default", "project-1", "vpc-2", "site1", st1)
	if error == nil {
		fmt.Printf("sr %v\n", *st.ResourceType)
	} else {
		fmt.Printf("error %v\n", error)
	}

	error = client.StaticRouteClient.Delete("default", "project-1", "vpc-2", "site1")
	if error == nil {
		fmt.Printf("delete succ")
	} else {
		fmt.Printf("delete error %v\n", error)
	}
	a := "/orgs/default/projects/project-1/vpcs/vpc-2/static-routes/site1"
	b := strings.Split(a, "/")
	fmt.Printf("b is %v \n", b[2])

}
