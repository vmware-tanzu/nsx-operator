package subnetport

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/zhengxiexie/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestBuildSubnetPort(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	service := &SubnetPortService{
		Service: common.Service{
			Client:    k8sClient,
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
				CoeConfig: &config.CoeConfig{
					Cluster: "fake_cluster",
				},
			},
		},
	}
	ctx := context.Background()
	namespace := &corev1.Namespace{}
	k8sClient.EXPECT().Get(ctx, gomock.Any(), namespace).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			return nil
		})

	tests := []struct {
		name          string
		obj           interface{}
		nsxSubnet     *model.VpcSubnet
		contextID     string
		labelTags     *map[string]string
		expectedPort  *model.VpcSubnetPort
		expectedError error
	}{
		{
			name: "01",
			obj: &v1alpha1.SubnetPort{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1alpha1",
					Kind:       "SubnetPort",
				},
				ObjectMeta: metav1.ObjectMeta{
					UID:       "2ccec3b9-7546-4fd2-812a-1e3a4afd7acc",
					Name:      "fake_subnetport",
					Namespace: "fake_ns",
				},
			},
			nsxSubnet: &model.VpcSubnet{
				DhcpConfig: &model.VpcSubnetDhcpConfig{
					EnableDhcp: common.Bool(true),
				},
				Path: common.String("fake_path"),
			},
			contextID: "fake_context_id",
			labelTags: nil,
			expectedPort: &model.VpcSubnetPort{
				DisplayName: common.String("port-fake_subnetport"),
				Id:          common.String("2ccec3b9-7546-4fd2-812a-1e3a4afd7acc"),
				Tags: []model.Tag{
					{
						Scope: common.String("nsx-op/cluster"),
						Tag:   common.String("fake_cluster"),
					},
					{
						Scope: common.String("nsx-op/version"),
						Tag:   common.String("1.0.0"),
					},
					{
						Scope: common.String("nsx-op/vm_namespace"),
						Tag:   common.String("fake_ns"),
					},
					{
						Scope: common.String("nsx-op/subnetport_name"),
						Tag:   common.String("fake_subnetport"),
					},
					{
						Scope: common.String("nsx-op/subnetport_uid"),
						Tag:   common.String("2ccec3b9-7546-4fd2-812a-1e3a4afd7acc"),
					},
				},
				Path:       common.String("fake_path/ports/2ccec3b9-7546-4fd2-812a-1e3a4afd7acc"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("DHCP"),
					Type_:             common.String("STATIC"),
					Id:                common.String("32636365-6333-4239-ad37-3534362d3466"),
					TrafficTag:        common.Int64(0),
				},
			},
			expectedError: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observedPort, err := service.buildSubnetPort(tt.obj, tt.nsxSubnet, tt.contextID, tt.labelTags)
			assert.Equal(t, tt.expectedPort, observedPort)
			assert.Equal(t, common.CompareResource(SubnetPortToComparable(tt.expectedPort), SubnetPortToComparable(observedPort)), false)
			assert.Equal(t, tt.expectedError, err)
		})
	}

}
