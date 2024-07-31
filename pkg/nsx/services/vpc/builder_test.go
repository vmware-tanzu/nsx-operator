package vpc

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/nsx.vmware.com/v1alpha1"
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
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
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
				Id:          common.String("nsuid1"),
				DisplayName: common.String("vpc-cluster1--ns1"),
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
