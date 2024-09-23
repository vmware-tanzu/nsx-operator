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
			got, err := buildNSXVPC(netInfoObj, nsObj, nc, clusterStr, tc.existingVPC, tc.useAVILB, tc.lbProviderChanged)
			assert.Nil(t, err)
			assert.Equal(t, tc.expVPC, got)
		})
	}
}
