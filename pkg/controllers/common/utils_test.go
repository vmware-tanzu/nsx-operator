package common

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
				ssp.(*pkg_mock.MockSubnetServiceProvider).On("CreateOrUpdateSubnet", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&model.VpcSubnet{Path: &expectedSubnetPath}, nil)
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

func TestGetDefaultSubnetSetByNamespace(t *testing.T) {
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
			name: "ListSubnetSetFailure",
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("failed to list SubnetSet"))
			},
			expectedErr: "failed to list SubnetSet",
		},
		{
			name: "NoDefaultSubnetSet",
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			},
			expectedErr: "default SubnetSet not found",
		},
		{
			name: "MultipleDefaultSubnetSet",
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.SubnetSetList)
					a.Items = append(a.Items, subnetSet)
					a.Items = append(a.Items, subnetSet)
					return nil
				})
			},
			expectedErr: "multiple default SubnetSets found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.prepareFunc(t)
			result, err := GetDefaultSubnetSetByNamespace(k8sClient, "ns-1", "")
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

func TestCheckNetworkStack(t *testing.T) {
	tests := []struct {
		name          string
		namespace     string
		objects       []client.Object
		wantErr       bool
		errMsg        string
		expectedErrIs error
	}{
		{
			name:      "no NetworkInfo found",
			namespace: "default",
			objects:   []client.Object{},
			wantErr:   false,
		},
		{
			name:      "VLANBackedVPC not supported",
			namespace: "default",
			objects: []client.Object{
				&v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
					VPCs: []v1alpha1.VPCState{
						{NetworkStack: "VLANBackedVPC"},
					},
				},
			},
			wantErr: true,
			errMsg:  "StaticRoute is not supported in VLANBackedVPC VPC",
		},
		{
			name:      "valid FullStackVPC VPC",
			namespace: "default",
			objects: []client.Object{
				&v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
					VPCs: []v1alpha1.VPCState{
						{NetworkStack: "FullStackVPC"},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			v1alpha1.AddToScheme(scheme)
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.objects...).Build()

			err := CheckNetworkStack(client, context.Background(), tt.namespace, "StaticRoute")

			if (err != nil) != tt.wantErr {
				t.Errorf("CheckNetworkStack() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if tt.expectedErrIs != nil {
					if !errors.Is(err, tt.expectedErrIs) {
						t.Errorf("expected error to be %v, got %v", tt.expectedErrIs, err)
					}
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("CheckNetworkStack() error = %v, want %v", err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func TestCheckAccessModeOrVisibility(t *testing.T) {
	ctx := context.TODO()
	fakeClient := fake.NewClientBuilder().Build()
	ns := "test-ns"

	tests := []struct {
		name         string
		tepLess      bool
		tepLessErr   error
		accessMode   string
		resourceType string
		wantErr      bool
		expectedErr  string
	}{
		{
			name:         "TepLess is true, IPAddressAllocation, AccessMode External - Success",
			tepLess:      true,
			accessMode:   string(v1alpha1.IPAddressVisibilityExternal),
			resourceType: servicecommon.ResourceTypeIPAddressAllocation,
			wantErr:      false,
		},
		{
			name:         "TepLess is true, IPAddressAllocation, AccessMode Private - Failure",
			tepLess:      true,
			accessMode:   "Private",
			resourceType: servicecommon.ResourceTypeIPAddressAllocation,
			wantErr:      true,
			expectedErr:  "IPAddressVisibility other than External is not supported for VLANBackedVPC",
		},
		{
			name:         "TepLess is true, Other resource, AccessMode Public - Success",
			tepLess:      true,
			accessMode:   string(v1alpha1.AccessModePublic),
			resourceType: "VPCSubnet",
			wantErr:      false,
		},
		{
			name:         "TepLess is true, Other resource, AccessMode Private - Failure",
			tepLess:      true,
			accessMode:   string(v1alpha1.AccessModePrivate),
			resourceType: "SubnetSet",
			wantErr:      true,
			expectedErr:  "AccessMode other than Public is not supported for VLANBackedVPC",
		},
		{
			name:         "TepLess is false, Any AccessMode - Success",
			tepLess:      false,
			accessMode:   "AnyMode",
			resourceType: "AnyResource",
			wantErr:      false,
		},
		{
			name:        "IsTepLessMode returns error",
			tepLessErr:  fmt.Errorf("internal error"),
			wantErr:     true,
			expectedErr: "internal error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Mock the IsTepLessMode function
			// Note: Replace 'IsTepLessMode' with the actual package-qualified name if it's external
			patches := gomonkey.ApplyFunc(IsTepLessMode, func(_ client.Client, _ context.Context, _ string) (bool, error) {
				return tt.tepLess, tt.tepLessErr
			})
			defer patches.Reset()

			err := CheckAccessModeOrVisibility(fakeClient, ctx, ns, tt.accessMode, tt.resourceType)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetNamespaceType(t *testing.T) {
	tests := []struct {
		name     string
		ns       *v1.Namespace
		vnc      *v1alpha1.VPCNetworkConfiguration
		expected NameSpaceType
	}{
		{
			name: "annotation system returns SystemNs",
			ns: &v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "ns-system",
					Annotations: map[string]string{servicecommon.AnnotationVPCNetworkConfig: "system"},
				},
			},
			vnc: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{VPC: "irrelevant"},
			},
			expected: SystemNs,
		},
		{
			name: "lablel present  returns SVServiceNs",
			ns: &v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "ns-anno-nonsystem",
					Labels: map[string]string{"appplatform.vmware.com/serviceId": "custom-nc", "managedBy": "vSphere-AppPlatform"},
				},
			},
			vnc: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{VPC: "vmware-system-supervisor-services"},
			},
			expected: SVServiceNs,
		},
		{
			name: "no special annotation and vnc does not contain supervisor returns NormalNs",
			ns: &v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ns-normal",
				},
			},
			vnc: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{VPC: "some-other-vpc"},
			},
			expected: NormalNs,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := GetNamespaceType(tc.ns, tc.vnc)
			assert.Equal(t, tc.expected, got)
		})
	}
}
