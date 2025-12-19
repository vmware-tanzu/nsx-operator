/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"
	pkg_log "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
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
	cf := config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{NsxApiUser: "1", NsxApiPassword: "1", NsxApiManagers: []string{"10.0.0.1"}}}
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
	pkg_log.SetLogger(zap.New(zap.UseDevMode(true)))
	cf := config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{NsxApiUser: "admin", NsxApiPassword: "Admin!23Admin", NsxApiManagers: []string{"10.0.0.1"}}}
	cf.VCConfig = &config.VCConfig{}

	cluster := &Cluster{}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(cluster), "GetVersion", func(_ *Cluster) (*NsxVersion, error) {
		nsxVersion := &NsxVersion{NodeVersion: "4.1.3"}
		return nsxVersion, nil
	})
	defer patches.Reset()
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
func TestRestConnectorAllowOverwrite(t *testing.T) {
	resVersion := `{
                    "node_version": "3.1.3.3.0.18844962",
                    "product_version": "3.1.3.3.0.18844959"
                    }`
	resHealth := `{
					"healthy" : true,
					"components_health" : "MANAGER:UP, SEARCH:UP, UI:UP, NODE_MGMT:UP"
					}`
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("header %v", r.Header)
		w.WriteHeader(http.StatusOK)
		if strings.Contains(r.URL.Path, "search") {
			allowOverwrite, _ := r.Header["X-Allow-Overwrite"]
			assert.Equal(t, "True", allowOverwrite[0])
		}
		if strings.Contains(r.URL.Path, "reverse-proxy/node/health") {
			w.Write(([]byte(resHealth)))
		} else {
			w.Write([]byte(resVersion))
		}
	}))
	defer ts.Close()
	thumbprint := []string{"123"}
	index := strings.Index(ts.URL, "//")
	a := ts.URL[index+2:]
	config := NewConfig(a, "admin", "passw0rd", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, thumbprint)
	cluster, _ := NewCluster(config)
	nsxVersion, err := cluster.GetVersion()
	assert.Equal(t, err, nil)
	assert.Equal(t, nsxVersion.NodeVersion, "3.1.3.3.0.18844962")

	client := search.NewQueryClient(cluster.NewRestConnectorAllowOverwrite())
	client.List("search", nil, nil, nil, nil, nil)
}
func TestValidateLicense(t *testing.T) {
	pkg_log.SetLogger(zap.New(zap.UseDevMode(true)))
	cf := config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{NsxApiUser: "admin", NsxApiPassword: "Admin!23Admin", NsxApiManagers: []string{"10.0.0.1"}}}
	cf.VCConfig = &config.VCConfig{}
	cf.NsxConfig = &config.NsxConfig{}
	cf.CoeConfig = &config.CoeConfig{EnableVPCNetwork: true}

	cluster := &Cluster{}

	tests := []struct {
		name             string
		init             bool
		fetchLicenseErr  error
		isLicensedBefore bool
		isLicensedAfter  bool
		dfwLicenseBefore bool
		dfwLicenseAfter  bool
		wantErr          bool
		expectedErrMsg   string
	}{
		{
			name:             "init mode, container license supported",
			init:             true,
			fetchLicenseErr:  nil,
			isLicensedBefore: true,
			isLicensedAfter:  true,
			dfwLicenseBefore: true,
			dfwLicenseAfter:  true,
			wantErr:          false,
		},
		{
			name:             "init mode, container license not supported",
			init:             true,
			fetchLicenseErr:  nil,
			isLicensedBefore: false,
			isLicensedAfter:  false,
			dfwLicenseBefore: false,
			dfwLicenseAfter:  false,
			wantErr:          true,
			expectedErrMsg:   "NSX license check failed",
		},
		{
			name:             "init mode, VPC license not supported",
			init:             true,
			fetchLicenseErr:  nil,
			isLicensedBefore: true,
			isLicensedAfter:  false,
			dfwLicenseBefore: false,
			dfwLicenseAfter:  false,
			wantErr:          true,
			expectedErrMsg:   "NSX license check failed",
		},
		{
			name:            "fetch license error",
			init:            true,
			fetchLicenseErr: fmt.Errorf("license fetch failed"),
			wantErr:         true,
			expectedErrMsg:  "license fetch failed",
		},
		{
			name:             "non-init mode, license unchanged",
			init:             false,
			fetchLicenseErr:  nil,
			isLicensedBefore: true,
			isLicensedAfter:  true,
			dfwLicenseBefore: true,
			dfwLicenseAfter:  true,
			wantErr:          false,
		},
		{
			name:             "non-init mode, container license changed",
			init:             false,
			fetchLicenseErr:  nil,
			isLicensedBefore: true,
			isLicensedAfter:  true,
			dfwLicenseBefore: true,
			dfwLicenseAfter:  false,
			wantErr:          true,
			expectedErrMsg:   "license updated",
		},
		{
			name:             "non-init mode, dfw license changed",
			init:             false,
			fetchLicenseErr:  nil,
			isLicensedBefore: true,
			isLicensedAfter:  true,
			dfwLicenseBefore: true,
			dfwLicenseAfter:  false,
			wantErr:          true,
			expectedErrMsg:   "license updated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patchFetch := gomonkey.ApplyMethod(reflect.TypeOf(cluster), "FetchLicense", func(_ *Cluster) error {
				return tt.fetchLicenseErr
			})
			defer patchFetch.Reset()

			callCount := 0
			patchIsLicensed := gomonkey.ApplyFunc(util.IsLicensed, func(feature string) bool {
				callCount++
				if callCount == 1 {
					return tt.isLicensedBefore
				}
				return tt.isLicensedAfter
			})
			defer patchIsLicensed.Reset()

			dfwCallCount := 0
			patchDfw := gomonkey.ApplyFunc(util.GetDFWLicense, func() bool {
				dfwCallCount++
				if dfwCallCount == 1 {
					return tt.dfwLicenseBefore
				}
				return tt.dfwLicenseAfter
			})
			defer patchDfw.Reset()

			patchSetVpc := gomonkey.ApplyFunc(util.SetEnableVpcNetwork, func(enable bool) {})
			defer patchSetVpc.Reset()

			client := &Client{
				NsxConfig: &cf,
				NSXChecker: NSXHealthChecker{
					cluster: cluster,
				},
			}

			err := client.ValidateLicense(tt.init)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateLicense() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil && err.Error() != tt.expectedErrMsg {
				t.Errorf("ValidateLicense() error message = %v, expected %v", err.Error(), tt.expectedErrMsg)
			}
		})
	}
}
