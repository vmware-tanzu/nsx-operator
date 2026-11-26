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
	"github.com/stretchr/testify/require"
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
	config := NewConfig(host, "1", "1", []string{}, false, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
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
	cf := config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{NsxApiUser: "1", NsxApiPassword: "1", NsxApiManagers: []string{"10.0.0.1"}, HttpTimeout: 20}}
	cf.VCConfig = &config.VCConfig{}
	client := GetClient(&cf)
	assert.True(t, client != nil)

	cluster := &Cluster{}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(cluster), "GetVersion", func(_ *Cluster) (*NsxVersion, error) {
		nsxVersion := &NsxVersion{ProductVersion: "3.1.1"}
		return nsxVersion, nil
	})

	client = GetClient(&cf)
	patches.Reset()
	assert.True(t, client != nil)
	securityPolicySupported := client.NSXCheckVersion(SecurityPolicy)
	assert.False(t, securityPolicySupported)
	assert.False(t, client.NSXCheckVersion(ServiceAccount))
	assert.False(t, client.NSXCheckVersion(ServiceAccountRestore))
	assert.False(t, client.NSXCheckVersion(ServiceAccountCertRotation))

	patches = gomonkey.ApplyMethod(reflect.TypeOf(cluster), "GetVersion", func(_ *Cluster) (*NsxVersion, error) {
		nsxVersion := &NsxVersion{ProductVersion: "3.2.1"}
		return nsxVersion, nil
	})
	client = GetClient(&cf)
	patches.Reset()
	assert.True(t, client != nil)
	securityPolicySupported = client.NSXCheckVersion(SecurityPolicy)
	assert.True(t, securityPolicySupported)
	assert.False(t, client.NSXCheckVersion(ServiceAccount))
	assert.False(t, client.NSXCheckVersion(ServiceAccountRestore))
	assert.False(t, client.NSXCheckVersion(ServiceAccountCertRotation))

	patches = gomonkey.ApplyMethod(reflect.TypeOf(cluster), "GetVersion", func(_ *Cluster) (*NsxVersion, error) {
		nsxVersion := &NsxVersion{ProductVersion: "4.1.0"}
		return nsxVersion, nil
	})
	client = GetClient(&cf)
	patches.Reset()
	assert.True(t, client != nil)
	securityPolicySupported = client.NSXCheckVersion(SecurityPolicy)
	assert.True(t, securityPolicySupported)
	assert.True(t, client.NSXCheckVersion(ServiceAccount))
	assert.False(t, client.NSXCheckVersion(ServiceAccountRestore))
	assert.False(t, client.NSXCheckVersion(ServiceAccountCertRotation))

	patches = gomonkey.ApplyMethod(reflect.TypeOf(cluster), "GetVersion", func(_ *Cluster) (*NsxVersion, error) {
		nsxVersion := &NsxVersion{ProductVersion: "4.1.2"}
		return nsxVersion, nil
	})
	client = GetClient(&cf)
	patches.Reset()
	assert.True(t, client != nil)
	securityPolicySupported = client.NSXCheckVersion(SecurityPolicy)
	assert.True(t, securityPolicySupported)
	assert.True(t, client.NSXCheckVersion(ServiceAccount))
	assert.True(t, client.NSXCheckVersion(ServiceAccountRestore))
	assert.False(t, client.NSXCheckVersion(ServiceAccountCertRotation))

	patches = gomonkey.ApplyMethod(reflect.TypeOf(cluster), "GetVersion", func(_ *Cluster) (*NsxVersion, error) {
		nsxVersion := &NsxVersion{ProductVersion: "4.1.3"}
		return nsxVersion, nil
	})
	client = GetClient(&cf)
	patches.Reset()
	assert.True(t, client != nil)
	securityPolicySupported = client.NSXCheckVersion(SecurityPolicy)
	assert.True(t, securityPolicySupported)
	assert.True(t, client.NSXCheckVersion(ServiceAccount))
	assert.True(t, client.NSXCheckVersion(ServiceAccountRestore))
	assert.True(t, client.NSXCheckVersion(ServiceAccountCertRotation))
}

func newLocalNSXClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/session/create":
			w.Header().Set("X-XSRF-TOKEN", "test-session-token")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{}`)
		case "/api/v1/reverse-proxy/node/health":
			fmt.Fprint(w, `{"healthy":true,"components_health":"MANAGER:UP, SEARCH:UP, UI:UP, NODE_MGMT:UP"}`)
		case "/api/v1/node/version":
			fmt.Fprint(w, `{"node_version":"9.2.0","product_version":"9.2.0"}`)
		default:
			handler(w, r)
		}
	}))
	t.Cleanup(server.Close)
	cf := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			NsxApiManagers: []string{strings.TrimPrefix(server.URL, "https://")},
			NsxApiUser:     "test-user",
			NsxApiPassword: "test-password",
			Insecure:       true,
			HttpTimeout:    5,
		},
		VCConfig: &config.VCConfig{},
	}
	client, err := NewClient(cf)
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, endpoint := range client.Cluster.endpoints {
			close(endpoint.stop)
		}
		client.Cluster.noBalancerClient.CloseIdleConnections()
		if transport, ok := client.Cluster.transport.Base.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
	})
	return client
}

func TestClientVPCIPBlockAllocationState(t *testing.T) {
	const allocationStatePath = "/policy/api/v1/orgs/default/projects/project/vpcs/vpc/ip-blocks/allocation-state"
	const blockPath = "/orgs/default/projects/project/infra/ip-blocks/block"
	requests := make(chan string, 1)
	client := newLocalNSXClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != allocationStatePath {
			http.Error(w, "unexpected request path", http.StatusNotFound)
			return
		}
		requests <- r.Method + " " + r.URL.RequestURI()
		fmt.Fprintf(w, `{"results":[{"ip_block_path":%q,"visibility":"PRIVATE","ip_address_type":"IPV4","allocation_states":[{"cidr":"10.0.0.0/28"}]}]}`, blockPath)
	})
	require.NotNil(t, client.VPCIPBlockAllocationStateClient)
	states, err := client.VPCIPBlockAllocationStateClient.List("default", "project", "vpc", nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, states.Results, 1)
	require.NotNil(t, states.Results[0].IpBlockPath)
	assert.Equal(t, blockPath, *states.Results[0].IpBlockPath)
	require.Len(t, states.Results[0].AllocationStates, 1)
	require.NotNil(t, states.Results[0].AllocationStates[0].Cidr)
	assert.Equal(t, "10.0.0.0/28", *states.Results[0].AllocationStates[0].Cidr)
	select {
	case request := <-requests:
		assert.Equal(t, "GET "+allocationStatePath, request, "nil filters must not add query parameters")
	default:
		t.Fatal("NSX allocation-state endpoint was not requested")
	}
}

func TestClientSubnetDHCPServerLeases(t *testing.T) {
	const leasesPath = "/policy/api/v1/orgs/default/projects/project/vpcs/vpc/subnets/subnet/state/dhcp-server-leases"
	requests := make(chan string, 1)
	client := newLocalNSXClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != leasesPath {
			http.Error(w, "unexpected request path", http.StatusNotFound)
			return
		}
		requests <- r.Method + " " + r.URL.RequestURI()
		fmt.Fprint(w, `{
			"leases":[{
				"mac_address":"00:50:56:aa:bb:cc",
				"ip_address":"10.0.0.10",
				"subnet":"10.0.0.0/24",
				"lease_time":"3600",
				"start_time":"2026-11-18T08:00:00Z",
				"expire_time":"2026-11-18T09:00:00Z"
			}],
			"ipv6_leases":[{
				"duid":"00:01:00:01:12:34:56:78:00:50:56:aa:bb:cc",
				"ia_type":"IA_NA",
				"iaid":65537,
				"ip_addresses":["2001:db8::10","2001:db8::11"],
				"lease_time":"7200",
				"start_time":"2026-11-18T08:00:00Z",
				"expire_time":"2026-11-18T10:00:00Z"
			}]
		}`)
	})
	require.NotNil(t, client.SubnetDHCPServerLeasesClient)
	leases, err := client.SubnetDHCPServerLeasesClient.List("default", "project", "vpc", "subnet", nil, nil, nil, nil, nil, nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, leases.Leases, 1)
	require.Len(t, leases.Ipv6Leases, 1)
	stringPtr := func(value string) *string { return &value }
	assert.Equal(t, model.DhcpLeasePerIP{
		MacAddress: stringPtr("00:50:56:aa:bb:cc"),
		IpAddress:  stringPtr("10.0.0.10"),
		Subnet:     stringPtr("10.0.0.0/24"),
		LeaseTime:  stringPtr("3600"),
		StartTime:  stringPtr("2026-11-18T08:00:00Z"),
		ExpireTime: stringPtr("2026-11-18T09:00:00Z"),
	}, leases.Leases[0])
	iaid := int64(65537)
	assert.Equal(t, model.DhcpV6Lease{
		Duid:        stringPtr("00:01:00:01:12:34:56:78:00:50:56:aa:bb:cc"),
		IaType:      stringPtr(model.DhcpV6Lease_IA_TYPE_NA),
		Iaid:        &iaid,
		IpAddresses: []string{"2001:db8::10", "2001:db8::11"},
		LeaseTime:   stringPtr("7200"),
		StartTime:   stringPtr("2026-11-18T08:00:00Z"),
		ExpireTime:  stringPtr("2026-11-18T10:00:00Z"),
	}, leases.Ipv6Leases[0])
	select {
	case request := <-requests:
		assert.Equal(t, "GET "+leasesPath, request, "nil filters must not add query parameters")
	default:
		t.Fatal("NSX DHCP server leases endpoint was not requested")
	}
}

func TestClient_resetNSXVersionFeatureCache(t *testing.T) {
	client := &Client{}
	for i := range client.NSXVerChecker.featureSupported {
		client.NSXVerChecker.featureSupported[i] = true
	}
	client.resetNSXVersionFeatureCache()
	for i := range client.NSXVerChecker.featureSupported {
		assert.False(t, client.NSXVerChecker.featureSupported[i], "featureSupported[%d] should be false after reset", i)
	}
}

func IsInstanceOf(objectPtr, typePtr interface{}) bool {
	return reflect.TypeOf(objectPtr) == reflect.TypeOf(typePtr)
}

func TestSRGetClient(t *testing.T) {
	pkg_log.SetLogger(zap.New(zap.UseDevMode(true)))
	cf := config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{NsxApiUser: "admin", NsxApiPassword: "Admin!23Admin", NsxApiManagers: []string{"10.0.0.1"}, HttpTimeout: 20}}
	cf.VCConfig = &config.VCConfig{}

	cluster := &Cluster{}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(cluster), "GetVersion", func(_ *Cluster) (*NsxVersion, error) {
		nsxVersion := &NsxVersion{ProductVersion: "4.1.3"}
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
			allowOverwrite := r.Header["X-Allow-Overwrite"]
			assert.Equal(t, "True", allowOverwrite[0])
		}
		if strings.Contains(r.URL.Path, "reverse-proxy/node/health") {
			w.Write([]byte(resHealth))
		} else {
			w.Write([]byte(resVersion))
		}
	}))
	defer ts.Close()
	thumbprint := []string{"123"}
	index := strings.Index(ts.URL, "//")
	a := ts.URL[index+2:]
	config := NewConfig(a, "admin", "passw0rd", []string{}, false, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, thumbprint)
	cluster, _ := NewCluster(config)
	nsxVersion, err := cluster.GetVersion()
	assert.Equal(t, err, nil)
	assert.Equal(t, nsxVersion.ProductVersion, "3.1.3.3.0.18844959")

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
