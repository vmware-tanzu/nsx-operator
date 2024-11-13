package common

import (
	"context"
	"errors"
	"fmt"
	"testing"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	pkg_mock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestGetVirtualMachineNameForSubnetPort(t *testing.T) {
	type args struct {
		subnetPort *v1alpha1.SubnetPort
	}
	type want struct {
		vm   string
		port string
		err  error
	}
	tests := []struct {
		name string
		args args
		want want
	}{
		{
			"port_with_annotation",
			args{&v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"nsx.vmware.com/attachment_ref": "virtualmachine/abc/port1",
					},
				}}},
			want{vm: "abc", port: "port1", err: nil},
		},
		{
			"port_without_annotation",
			args{&v1alpha1.SubnetPort{}},
			want{vm: "", port: "", err: nil},
		},
		{
			"port_with_invalid_annotation",
			args{&v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"nsx.vmware.com/attachment_ref": "invalid/abc",
					},
				}}},
			want{vm: "", port: "", err: fmt.Errorf("invalid annotation value of 'nsx.vmware.com/attachment_ref': invalid/abc")},
		},
	}
	for _, tt := range tests {
		got1, got2, err := GetVirtualMachineNameForSubnetPort(tt.args.subnetPort)
		assert.Equal(t, err, tt.want.err)
		if got1 != tt.want.vm {
			t.Errorf("%s failed: got %s, want %s", tt.name, got1, tt.want.vm)
		}
		if got2 != tt.want.port {
			t.Errorf("%s failed: got %s, want %s", tt.name, got2, tt.want.port)
		}
	}
}

func TestAllocateSubnetFromSubnetSet(t *testing.T) {
	expectedSubnetPath := "subnet-path-1"
	subnetSize := int64(32)
	tests := []struct {
		name           string
		prepareFunc    func(*testing.T, servicecommon.VPCServiceProvider, servicecommon.SubnetServiceProvider, servicecommon.SubnetPortServiceProvider)
		expectedErr    string
		expectedResult string
	}{
		{
			name: "AvailableSubnet",
			prepareFunc: func(t *testing.T, vsp servicecommon.VPCServiceProvider, ssp servicecommon.SubnetServiceProvider, spsp servicecommon.SubnetPortServiceProvider) {
				ssp.(*pkg_mock.MockSubnetServiceProvider).On("GetSubnetsByIndex", mock.Anything, mock.Anything).
					Return([]*model.VpcSubnet{
						{
							Id:             servicecommon.String("id-1"),
							Path:           &expectedSubnetPath,
							Ipv4SubnetSize: &subnetSize,
							IpAddresses:    []string{"10.0.0.1/28"},
						},
					})
				spsp.(*pkg_mock.MockSubnetPortServiceProvider).On("GetPortsOfSubnet", mock.Anything).Return([]*model.VpcSubnetPort{})
			},
			expectedResult: expectedSubnetPath,
		},
		{
			name: "ListVPCFailure",
			prepareFunc: func(t *testing.T, vsp servicecommon.VPCServiceProvider, ssp servicecommon.SubnetServiceProvider, spsp servicecommon.SubnetPortServiceProvider) {
				ssp.(*pkg_mock.MockSubnetServiceProvider).On("GetSubnetsByIndex", mock.Anything, mock.Anything).
					Return([]*model.VpcSubnet{})
				ssp.(*pkg_mock.MockSubnetServiceProvider).On("GenerateSubnetNSTags", mock.Anything)
				vsp.(*pkg_mock.MockVPCServiceProvider).On("ListVPCInfo", mock.Anything).Return([]servicecommon.VPCResourceInfo{})
			},
			expectedErr: "no VPC found",
		},
		{
			name: "CreateSubnet",
			prepareFunc: func(t *testing.T, vsp servicecommon.VPCServiceProvider, ssp servicecommon.SubnetServiceProvider, spsp servicecommon.SubnetPortServiceProvider) {
				ssp.(*pkg_mock.MockSubnetServiceProvider).On("GetSubnetsByIndex", mock.Anything, mock.Anything).
					Return([]*model.VpcSubnet{})
				ssp.(*pkg_mock.MockSubnetServiceProvider).On("GenerateSubnetNSTags", mock.Anything)
				vsp.(*pkg_mock.MockVPCServiceProvider).On("ListVPCInfo", mock.Anything).Return([]servicecommon.VPCResourceInfo{{}})
				ssp.(*pkg_mock.MockSubnetServiceProvider).On("CreateOrUpdateSubnet", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(expectedSubnetPath, nil)
			},
			expectedResult: expectedSubnetPath,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vps := &pkg_mock.MockVPCServiceProvider{}
			ssp := &pkg_mock.MockSubnetServiceProvider{}
			spsp := &pkg_mock.MockSubnetPortServiceProvider{}
			tt.prepareFunc(t, vps, ssp, spsp)
			subnetPath, err := AllocateSubnetFromSubnetSet(&v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnetset-1",
					Namespace: "ns-1",
				},
			}, vps, ssp, spsp)
			if tt.expectedErr != "" {
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, expectedSubnetPath, subnetPath)
			}
		})
	}
}

func TestGetDefaultSubnetSet(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	subnetSet := v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			UID:  "uuid-1",
			Name: "subnetset-1",
		},
	}
	tests := []struct {
		name           string
		prepareFunc    func(*testing.T)
		expectedErr    string
		expectedResult *v1alpha1.SubnetSet
	}{
		{
			name: "NamespaceNotFound",
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("failed to get Namespace"))
			},
			expectedErr: "failed to get Namespace",
		},
		{
			name: "SharedNamespace",
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					namespaceCR := obj.(*v1.Namespace)
					namespaceCR.Annotations = make(map[string]string)
					namespaceCR.Annotations[servicecommon.AnnotationSharedVPCNamespace] = "sharedNamespace"
					return nil
				})
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.SubnetSetList)
					a.Items = append(a.Items, subnetSet)
					return nil
				})
			},
			expectedResult: &subnetSet,
		},
		{
			name: "ListSubnetSetFailure",
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("failed to list SubnetSet"))
			},
			expectedErr: "failed to list SubnetSet",
		},
		{
			name: "NoDefaultSubnetSet",
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			},
			expectedErr: "default subnetset not found",
		},
		{
			name: "MultipleDefaultSubnetSet",
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.SubnetSetList)
					a.Items = append(a.Items, subnetSet)
					a.Items = append(a.Items, subnetSet)
					return nil
				})
			},
			expectedErr: "multiple default subnetsets found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.prepareFunc(t)
			result, err := GetDefaultSubnetSet(k8sClient, context.TODO(), "ns-1", "")
			if tt.expectedErr != "" {
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, tt.expectedResult, result)
			}
		})
	}

}

type fakeRecorder struct {
}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
}

func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}

func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}

func createStatusUpdater(t *testing.T) StatusUpdater {
	statusUpdater := StatusUpdater{
		Client: fake.NewClientBuilder().Build(),
		NSXConfig: &config.NSXOperatorConfig{
			NsxConfig: &config.NsxConfig{
				EnforcementPoint: "vmc-enforcementpoint",
			},
		},
		Recorder:        fakeRecorder{},
		MetricResType:   MetricResTypeSubnet,
		NSXResourceType: "Subnet",
		ResourceType:    "Subnet",
	}
	return statusUpdater
}
func TestStatusUpdater_UpdateSuccess(t *testing.T) {
	statusUpdater := createStatusUpdater(t)
	statusUpdater.UpdateSuccess(context.TODO(), &v1alpha1.Subnet{}, func(client.Client, context.Context, client.Object, metav1.Time, ...interface{}) {})
}

func TestStatusUpdater_UpdateFail(t *testing.T) {
	statusUpdater := createStatusUpdater(t)
	statusUpdater.UpdateFail(context.TODO(), &v1alpha1.Subnet{}, fmt.Errorf("mock error"), "log message", func(_ client.Client, _ context.Context, _ client.Object, _ metav1.Time, e error, _ ...interface{}) {
		assert.Contains(t, e.Error(), "mock error")
	})
}

func TestStatusUpdater_DeleteSuccess(t *testing.T) {
	statusUpdater := createStatusUpdater(t)

	patchesRecordEvent := gomonkey.ApplyFunc((record.EventRecorder).Event,
		func(r record.EventRecorder, object runtime.Object, eventtype string, reason string, message string) {
			assert.Equal(t, &v1alpha1.Subnet{}, object)
			assert.Equal(t, v1.EventTypeNormal, eventtype)
			assert.Equal(t, ReasonSuccessfulDelete, reason)
			assert.Equal(t, "Subnet CR has been successfully deleted", message)
			return
		})
	defer patchesRecordEvent.Reset()

	statusUpdater.DeleteSuccess(types.NamespacedName{Name: "name", Namespace: "ns"}, &v1alpha1.Subnet{})
}

func TestStatusUpdater_DeleteFail(t *testing.T) {
	statusUpdater := createStatusUpdater(t)

	patchesRecordEvent := gomonkey.ApplyFunc((record.EventRecorder).Event,
		func(r record.EventRecorder, object runtime.Object, eventtype string, reason string, message string) {
			assert.Equal(t, &v1alpha1.Subnet{}, object)
			assert.Equal(t, v1.EventTypeWarning, eventtype)
			assert.Equal(t, ReasonFailDelete, reason)
			assert.Equal(t, "mock error", message)
			return
		})
	defer patchesRecordEvent.Reset()

	statusUpdater.DeleteFail(types.NamespacedName{Name: "name", Namespace: "ns"}, &v1alpha1.Subnet{}, fmt.Errorf("mock error"))
}

func TestNodeIsMaster(t *testing.T) {
	tests := []struct {
		name     string
		node     *v1.Node
		expected bool
	}{
		{
			name: "Node with master role label",
			node: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelK8sMasterRole: "",
					},
				},
			},
			expected: true,
		},
		{
			name: "Node with control role label",
			node: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelK8sControlRole: "",
					},
				},
			},
			expected: true,
		},
		{
			name: "Node without master or control role label",
			node: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"some-other-label": "",
					},
				},
			},
			expected: false,
		},
		{
			name: "Node with no labels",
			node: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NodeIsMaster(tt.node)
			assert.Equal(t, tt.expected, result)
		})
	}
}
