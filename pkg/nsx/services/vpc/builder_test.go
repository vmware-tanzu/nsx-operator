package vpc

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func Test_buildNSXLBS(t *testing.T) {
	type args struct {
		obj                  *v1alpha1.NetworkInfo
		nsObj                *v1.Namespace
		cluster              string
		lbsSize              string
		vpcPath              string
		relaxScaleValidation *bool
	}
	tests := []struct {
		name    string
		args    args
		want    *model.LBService
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "1",
			args: args{
				obj: &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "ns1", UID: "netinfouid1"},
					VPCs:       nil,
				},
				nsObj: &v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: "ns1", UID: "nsuid1"},
				},
				cluster:              "cluster1",
				lbsSize:              model.LBService_SIZE_SMALL,
				vpcPath:              "/vpc1",
				relaxScaleValidation: nil,
			},
			want: &model.LBService{
				Id:          common.String(defaultLBSName),
				DisplayName: common.String(defaultLBSName),
				Tags: []model.Tag{
					{
						Scope: common.String(common.TagScopeCluster),
						Tag:   common.String("cluster1"),
					},
					{
						Scope: common.String(common.TagScopeVersion),
						Tag:   common.String(strings.Join(common.TagValueVersion, ".")),
					},
					{Scope: common.String(common.TagScopeNamespace), Tag: common.String("ns1")},
					{Scope: common.String(common.TagScopeNamespaceUID), Tag: common.String("nsuid1")},
					{Scope: common.String(common.TagScopeCreatedFor), Tag: common.String(common.TagValueSLB)},
				},
				Size:                 common.String(model.LBService_SIZE_SMALL),
				ConnectivityPath:     common.String("/vpc1"),
				RelaxScaleValidation: nil,
			},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildNSXLBS(tt.args.obj, tt.args.nsObj, tt.args.cluster, tt.args.lbsSize, tt.args.vpcPath, tt.args.relaxScaleValidation)
			if !tt.wantErr(t, err, fmt.Sprintf("buildNSXLBS(%v, %v, %v, %v, %v, %v)", tt.args.obj, tt.args.nsObj, tt.args.cluster, tt.args.lbsSize, tt.args.vpcPath, tt.args.relaxScaleValidation)) {
				return
			}
			assert.Equalf(t, tt.want, got, "buildNSXLBS(%v, %v, %v, %v, %v, %v)", tt.args.obj, tt.args.nsObj, tt.args.cluster, tt.args.lbsSize, tt.args.vpcPath, tt.args.relaxScaleValidation)
		})
	}
}

func TestBuildNSXVPC(t *testing.T) {
	nc := common.VPCNetworkConfigInfo{
		PrivateIPs: []string{"192.168.1.0/24"},
	}
	netInfoObj := &v1alpha1.NetworkInfo{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "netinfo1", UID: "netinfouid1"},
		VPCs:       nil,
	}
	nsObj := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "ns1", UID: "nsuid1"},
	}
	clusterStr := "cluster1"

	for _, tc := range []struct {
		name              string
		existingVPC       *model.Vpc
		ncPrivateIps      []string
		useAVILB          bool
		netInfoObj        *v1alpha1.NetworkInfo
		expVPC            *model.Vpc
		lbProviderChanged bool
	}{
		{
			name:         "existing VPC not change",
			ncPrivateIps: []string{"192.168.1.0/24"},
			existingVPC: &model.Vpc{
				PrivateIps: []string{"192.168.1.0/24"},
			},
			useAVILB:          true,
			lbProviderChanged: false,
		},
		{
			name: "existing VPC changes private IPv4 blocks",
			existingVPC: &model.Vpc{
				PrivateIps: []string{},
			},
			ncPrivateIps: []string{"192.168.3.0/24"},
			useAVILB:     false,
			expVPC: &model.Vpc{
				PrivateIps: []string{"192.168.3.0/24"},
			},
			lbProviderChanged: false,
		},
		{
			name:              "create new VPC with AVI load balancer enabled",
			ncPrivateIps:      []string{"192.168.3.0/24"},
			useAVILB:          true,
			lbProviderChanged: false,
			expVPC: &model.Vpc{
				Id:                      common.String("netinfo1_netinfouid1"),
				DisplayName:             common.String("netinfo1_netinfouid1"),
				LoadBalancerVpcEndpoint: &model.LoadBalancerVPCEndpoint{Enabled: common.Bool(true)},
				PrivateIps:              []string{"192.168.3.0/24"},
				IpAddressType:           common.String("IPV4"),
				Tags: []model.Tag{
					{Scope: common.String("nsx-op/cluster"), Tag: common.String("cluster1")},
					{Scope: common.String("nsx-op/version"), Tag: common.String("1.0.0")},
					{Scope: common.String("nsx-op/namespace"), Tag: common.String("ns1")},
					{Scope: common.String("nsx-op/namespace_uid"), Tag: common.String("nsuid1")},
					{Scope: common.String("nsx/managed-by"), Tag: common.String("nsx-op")},
				},
			},
		},
		{
			name:              "create new VPC with AVI load balancer disabled",
			ncPrivateIps:      []string{"192.168.3.0/24"},
			useAVILB:          false,
			lbProviderChanged: false,
			expVPC: &model.Vpc{
				Id:            common.String("netinfo1_netinfouid1"),
				DisplayName:   common.String("netinfo1_netinfouid1"),
				PrivateIps:    []string{"192.168.3.0/24"},
				IpAddressType: common.String("IPV4"),
				Tags: []model.Tag{
					{Scope: common.String("nsx-op/cluster"), Tag: common.String("cluster1")},
					{Scope: common.String("nsx-op/version"), Tag: common.String("1.0.0")},
					{Scope: common.String("nsx-op/namespace"), Tag: common.String("ns1")},
					{Scope: common.String("nsx-op/namespace_uid"), Tag: common.String("nsuid1")},
					{Scope: common.String("nsx/managed-by"), Tag: common.String("nsx-op")},
				},
			},
		},
		{
			name:              "create new VPC with 81 chars networkinfo name",
			ncPrivateIps:      []string{"192.168.3.0/24"},
			useAVILB:          false,
			lbProviderChanged: false,
			netInfoObj: &v1alpha1.NetworkInfo{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "test-ns-03a2def3-0087-4077-904e-23e4dd788fb7", UID: "ecc6eb9f-92b5-4893-b809-e3ebc1fcf59e"},
				VPCs:       nil,
			},
			expVPC: &model.Vpc{
				Id:            common.String("test-ns-03a2def3-0087-4077-904e-23e4dd788fb7_ecc6eb9f-92b5-4893-b809-e3ebc1fcf59e"),
				DisplayName:   common.String("test-ns-03a2def3-0087-4077-904e-23e4dd788fb7_yWOLBB"),
				PrivateIps:    []string{"192.168.3.0/24"},
				IpAddressType: common.String("IPV4"),
				Tags: []model.Tag{
					{Scope: common.String("nsx-op/cluster"), Tag: common.String("cluster1")},
					{Scope: common.String("nsx-op/version"), Tag: common.String("1.0.0")},
					{Scope: common.String("nsx-op/namespace"), Tag: common.String("ns1")},
					{Scope: common.String("nsx-op/namespace_uid"), Tag: common.String("nsuid1")},
					{Scope: common.String("nsx/managed-by"), Tag: common.String("nsx-op")},
				},
			},
		},
		{
			name:         "update VPC with AVI load balancer disabled -> enabled",
			ncPrivateIps: []string{"192.168.3.0/24"},
			existingVPC: &model.Vpc{
				Id:            common.String("netinfo1_netinfouid1"),
				DisplayName:   common.String("netinfo1_netinfouid1"),
				PrivateIps:    []string{"192.168.3.0/24"},
				IpAddressType: common.String("IPV4"),
			},
			useAVILB:          true,
			lbProviderChanged: true,
			expVPC: &model.Vpc{
				Id:                      common.String("netinfo1_netinfouid1"),
				DisplayName:             common.String("netinfo1_netinfouid1"),
				LoadBalancerVpcEndpoint: &model.LoadBalancerVPCEndpoint{Enabled: common.Bool(true)},
				PrivateIps:              []string{"192.168.3.0/24"},
				IpAddressType:           common.String("IPV4"),
			},
		},
		{
			name:         "update VPC with NSX load balancer disabled -> enabled",
			ncPrivateIps: []string{"192.168.3.0/24"},
			existingVPC: &model.Vpc{
				Id:            common.String("netinfo1_netinfouid1"),
				DisplayName:   common.String("netinfo1_netinfouid1"),
				PrivateIps:    []string{"192.168.3.0/24"},
				IpAddressType: common.String("IPV4"),
			},
			useAVILB:          false,
			lbProviderChanged: true,
			expVPC: &model.Vpc{
				Id:            common.String("netinfo1_netinfouid1"),
				DisplayName:   common.String("netinfo1_netinfouid1"),
				PrivateIps:    []string{"192.168.3.0/24"},
				IpAddressType: common.String("IPV4"),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nc.PrivateIPs = tc.ncPrivateIps
			if tc.netInfoObj != nil {
				netInfoObj = tc.netInfoObj
			}
			got, err := buildNSXVPC(netInfoObj, nsObj, nc, clusterStr, tc.existingVPC, tc.useAVILB, tc.lbProviderChanged)
			assert.Nil(t, err)
			assert.Equal(t, tc.expVPC, got)
		})
	}
}

func Test_combineVPCIDAndLBSID(t *testing.T) {
	type args struct {
		vpcID string
		lbsID string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "pass",
			args: args{
				vpcID: "fakeVpc",
				lbsID: "fakeLbs",
			},
			want: "fakeVpc_fakeLbs",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := combineVPCIDAndLBSID(tt.args.vpcID, tt.args.lbsID); got != tt.want {
				t.Errorf("combineVPCIDAndLBSID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_generateLBSKey(t *testing.T) {
	emptyPath := ""
	emptyVpcPath := "/fake/path/empty/vpc/"
	okPath := "/fake/path/vpc/fake-vpc"
	okId := "fake-id"
	type args struct {
		lbs model.LBService
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "nil connectivity path",
			args: args{
				lbs: model.LBService{ConnectivityPath: nil},
			},
			want:    "",
			wantErr: true,
		},
		{
			name: "empty connectivity path",
			args: args{
				lbs: model.LBService{ConnectivityPath: &emptyPath},
			},
			want:    "",
			wantErr: true,
		},
		{
			name: "empty vpc id",
			args: args{
				lbs: model.LBService{ConnectivityPath: &emptyVpcPath},
			},
			want:    "",
			wantErr: true,
		},
		{
			name: "nil lbs id",
			args: args{
				lbs: model.LBService{ConnectivityPath: &okPath, Id: nil},
			},
			want:    "",
			wantErr: true,
		},
		{
			name: "empty lbs id",
			args: args{
				lbs: model.LBService{ConnectivityPath: &okPath, Id: &emptyPath},
			},
			want:    "",
			wantErr: true,
		},
		{
			name: "empty lbs id",
			args: args{
				lbs: model.LBService{ConnectivityPath: &okPath, Id: &okId},
			},
			want:    "fake-vpc_fake-id",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := generateLBSKey(tt.args.lbs)
			if (err != nil) != tt.wantErr {
				t.Errorf("generateLBSKey() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("generateLBSKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildNetworkConfigInfo(t *testing.T) {
	emptyCRD := &v1alpha1.VPCNetworkConfiguration{}
	emptyCRD2 := &v1alpha1.VPCNetworkConfiguration{
		Spec: v1alpha1.VPCNetworkConfigurationSpec{
			NSXProject: "/invalid/path",
		},
	}
	_, e := buildNetworkConfigInfo(emptyCRD)
	assert.NotNil(t, e)
	_, e = buildNetworkConfigInfo(emptyCRD2)
	assert.NotNil(t, e)

	spec1 := v1alpha1.VPCNetworkConfigurationSpec{
		PrivateIPs:             []string{"private-ipb-1", "private-ipb-2"},
		DefaultSubnetSize:      64,
		VPCConnectivityProfile: "test-VPCConnectivityProfile",
		NSXProject:             "/orgs/default/projects/nsx_operator_e2e_test",
	}
	spec2 := v1alpha1.VPCNetworkConfigurationSpec{
		PrivateIPs:        []string{"private-ipb-1", "private-ipb-2"},
		DefaultSubnetSize: 32,
		NSXProject:        "/orgs/anotherOrg/projects/anotherProject",
	}
	spec3 := v1alpha1.VPCNetworkConfigurationSpec{
		DefaultSubnetSize: 28,
		NSXProject:        "/orgs/anotherOrg/projects/anotherProject",
		VPC:               "vpc33",
	}
	testCRD1 := v1alpha1.VPCNetworkConfiguration{
		Spec: spec1,
	}
	testCRD1.Name = "test-1"
	testCRD2 := v1alpha1.VPCNetworkConfiguration{
		Spec: spec2,
	}
	testCRD2.Name = "test-2"

	testCRD3 := v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				common.AnnotationDefaultNetworkConfig: "true",
			},
		},
		Spec: spec2,
	}
	testCRD3.Name = "test-3"

	testCRD4 := v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				common.AnnotationDefaultNetworkConfig: "false",
			},
		},
		Spec: spec3,
	}
	testCRD4.Name = "test-4"

	testCRD5 := v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				common.AnnotationDefaultNetworkConfig: "failed",
			},
		},
		Spec: spec3,
	}
	testCRD5.Name = "test-5"

	tests := []struct {
		name                   string
		nc                     v1alpha1.VPCNetworkConfiguration
		gw                     string
		edge                   string
		org                    string
		project                string
		subnetSize             int
		accessMode             string
		isDefault              bool
		vpcConnectivityProfile string
		vpcPath                string
	}{
		{"test-nsxProjectPathToId", testCRD1, "test-gw-path-1", "test-edge-path-1", "default", "nsx_operator_e2e_test", 64, "Public", false, "", ""},
		{"with-VPCConnectivityProfile", testCRD2, "test-gw-path-2", "test-edge-path-2", "anotherOrg", "anotherProject", 32, "Private", false, "test-VPCConnectivityProfile", ""},
		{"with-defaultNetworkConfig", testCRD3, "test-gw-path-2", "test-edge-path-2", "anotherOrg", "anotherProject", 32, "Private", true, "", ""},
		{"with-preCreatedVPC", testCRD4, "", "", "anotherOrg", "anotherProject", 28, "Private", false, "", "vpc33"},
		{"annotation-error", testCRD5, "", "", "anotherOrg", "anotherProject", 28, "Private", false, "", "vpc33"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nc, e := buildNetworkConfigInfo(&tt.nc)
			assert.Nil(t, e)
			assert.Equal(t, tt.org, nc.Org)
			assert.Equal(t, tt.project, nc.NSXProject)
			assert.Equal(t, tt.subnetSize, nc.DefaultSubnetSize)
			assert.Equal(t, tt.isDefault, nc.IsDefault)
			assert.Equal(t, tt.vpcPath, nc.VPCPath)
		})
	}
}

func TestNsxProjectPathToId(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		org       string
		project   string
		expectErr string
	}{
		{"Valid project path", "/orgs/default/projects/nsx_operator_e2e_test", "default", "nsx_operator_e2e_test", ""},
		{"Invalid project path", "", "", "", "invalid NSX project path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, p, err := nsxProjectPathToId(tt.path)
			if tt.expectErr != "" {
				assert.ErrorContains(t, err, tt.expectErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.org, o)
			assert.Equal(t, tt.project, p)
		})
	}
}

func TestIsDefaultNetworkConfigCR(t *testing.T) {
	testCRD1 := v1alpha1.VPCNetworkConfiguration{}
	testCRD1.Name = "test-1"
	testCRD2 := v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				common.AnnotationDefaultNetworkConfig: "invalid",
			},
		},
	}
	testCRD2.Name = "test-2"
	testCRD3 := v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				common.AnnotationDefaultNetworkConfig: "true",
			},
		},
	}
	testCRD3.Name = "test-3"
	assert.Equal(t, isDefaultNetworkConfigCR(&testCRD1), false)
	assert.Equal(t, isDefaultNetworkConfigCR(&testCRD2), false)
	assert.Equal(t, isDefaultNetworkConfigCR(&testCRD3), true)
}
