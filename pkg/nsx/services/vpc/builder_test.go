package vpc

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/crd.nsx.vmware.com/v1alpha1"
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
				Id:          common.String("ns1-netinfouid1"),
				DisplayName: common.String("ns1-netinfouid1"),
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
		ExternalIPv4Blocks: []string{"10.10.0.0/16"},
		PrivateIPv4CIDRs:   []string{"192.168.1.0/24"},
		DefaultGatewayPath: "gw1",
		ShortID:            "short1",
		EdgeClusterPath:    "edge1",
	}
	netInfoObj := &v1alpha1.NetworkInfo{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "ns1", UID: "netinfouid1"},
		VPCs:       nil,
	}
	nsObj := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "ns1", UID: "nsuid1"},
	}
	clusterStr := "cluster1"

	for _, tc := range []struct {
		name        string
		existingVPC *model.Vpc
		pathMap     map[string]string
		useAVILB    bool
		expVPC      *model.Vpc
	}{
		{
			name: "existing VPC not change",
			existingVPC: &model.Vpc{
				ExternalIpv4Blocks: []string{"10.10.0.0/16"},
				PrivateIpv4Blocks:  []string{"192.168.1.0/24"},
			},
			useAVILB: true,
		},
		{
			name: "existing VPC changes private IPv4 blocks",
			existingVPC: &model.Vpc{
				ExternalIpv4Blocks: []string{"10.10.0.0/16"},
				PrivateIpv4Blocks:  []string{},
			},
			pathMap:  map[string]string{"vpc1": "192.168.3.0/24"},
			useAVILB: false,
			expVPC: &model.Vpc{
				ExternalIpv4Blocks: []string{"10.10.0.0/16"},
				PrivateIpv4Blocks:  []string{"192.168.3.0/24"},
				ShortId:            common.String("short1"),
			},
		},
		{
			name:     "create new VPC with AVI load balancer enabled",
			pathMap:  map[string]string{"vpc1": "192.168.3.0/24"},
			useAVILB: true,
			expVPC: &model.Vpc{
				Id:                 common.String("ns1-netinfouid1"),
				DisplayName:        common.String("ns1-netinfouid1"),
				DefaultGatewayPath: common.String("gw1"),
				SiteInfos: []model.SiteInfo{
					{
						EdgeClusterPaths: []string{"edge1"},
					},
				},
				LoadBalancerVpcEndpoint: &model.LoadBalancerVPCEndpoint{Enabled: common.Bool(true)},
				ExternalIpv4Blocks:      []string{"10.10.0.0/16"},
				PrivateIpv4Blocks:       []string{"192.168.3.0/24"},
				IpAddressType:           common.String("IPV4"),
				ShortId:                 common.String("short1"),
				Tags: []model.Tag{
					{Scope: common.String("nsx-op/cluster"), Tag: common.String("cluster1")},
					{Scope: common.String("nsx-op/version"), Tag: common.String("1.0.0")},
					{Scope: common.String("nsx-op/namespace"), Tag: common.String("ns1")},
					{Scope: common.String("nsx-op/namespace_uid"), Tag: common.String("nsuid1")},
				},
			},
		},
		{
			name:     "create new VPC with AVI load balancer disabled",
			pathMap:  map[string]string{"vpc1": "192.168.3.0/24"},
			useAVILB: false,
			expVPC: &model.Vpc{
				Id:                 common.String("ns1-netinfouid1"),
				DisplayName:        common.String("ns1-netinfouid1"),
				DefaultGatewayPath: common.String("gw1"),
				SiteInfos: []model.SiteInfo{
					{
						EdgeClusterPaths: []string{"edge1"},
					},
				},
				ExternalIpv4Blocks: []string{"10.10.0.0/16"},
				PrivateIpv4Blocks:  []string{"192.168.3.0/24"},
				IpAddressType:      common.String("IPV4"),
				ShortId:            common.String("short1"),
				Tags: []model.Tag{
					{Scope: common.String("nsx-op/cluster"), Tag: common.String("cluster1")},
					{Scope: common.String("nsx-op/version"), Tag: common.String("1.0.0")},
					{Scope: common.String("nsx-op/namespace"), Tag: common.String("ns1")},
					{Scope: common.String("nsx-op/namespace_uid"), Tag: common.String("nsuid1")},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildNSXVPC(netInfoObj, nsObj, nc, clusterStr, tc.pathMap, tc.existingVPC, tc.useAVILB)
			assert.Nil(t, err)
			assert.Equal(t, tc.expVPC, got)
		})
	}
}
