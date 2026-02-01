package common

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	pkg_mock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
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
				spsp.(*pkg_mock.MockSubnetPortServiceProvider).On("AllocatePortFromSubnet", mock.Anything).Return(true, nil)
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
				spsp.(*pkg_mock.MockSubnetPortServiceProvider).On("AllocatePortFromSubnet", mock.Anything).Return(true, nil)
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
			subnetPath, _, _, err := AllocateSubnetFromSubnetSet(fake.NewClientBuilder().Build(), &v1alpha1.SubnetSet{
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
			name:         "TepLess is true, Other resource, AccessMode L2only - Success",
			tepLess:      true,
			accessMode:   string(v1alpha1.AccessModeL2Only),
			resourceType: "VPCSubnet",
			wantErr:      false,
		},
		{
			name:         "TepLess is true, Other resource, AccessMode Private - Failure",
			tepLess:      true,
			accessMode:   string(v1alpha1.AccessModePrivate),
			resourceType: "SubnetSet",
			wantErr:      true,
			expectedErr:  "AccessMode other than Public/L2Only is not supported for VLANBackedVPC",
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
		{
			name:       "IsTepLessMode, AccessMode None, - Success",
			tepLess:    true,
			wantErr:    false,
			accessMode: "",
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

func TestIsSharedSubnetPath(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	namespace := "ns-1"
	validPath := "/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1"
	expectedResource := "default:ns-1:subnet-1"

	tests := []struct {
		name            string
		path            string
		existingSubnets []v1alpha1.Subnet
		expectedResult  bool
		expectedError   bool
	}{
		{
			name: "shared-subnet",
			path: validPath,
			existingSubnets: []v1alpha1.Subnet{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "shared-subnet",
						Namespace: namespace,
					},
				},
			},
			expectedResult: true,
			expectedError:  false,
		},
		{
			name:            "not-shared-subnet",
			path:            validPath,
			existingSubnets: []v1alpha1.Subnet{},
			expectedResult:  false,
			expectedError:   false,
		},
		{
			name:           "invalid-path",
			path:           "invalid-path",
			expectedResult: false,
			expectedError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := []client.Object{}
			for i := range tt.existingSubnets {
				objs = append(objs, &tt.existingSubnets[i])
			}

			cb := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.Subnet{})
			// Mock the Field Indexer for MatchingFields
			cb.WithIndex(&v1alpha1.Subnet{}, util.SubnetAssociatedResource, func(o client.Object) []string {
				return []string{expectedResource}
			})
			fakeClient := cb.WithObjects(objs...).Build()

			result, err := IsSharedSubnetPath(context.TODO(), fakeClient, tt.path, namespace)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result)
			}
		})
	}
}

func TestGetNSXSubnetsForSubnetSet(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	namespace := "default"
	subnetSetUID := "uid-123"
	nsxSubnet := &model.VpcSubnet{Id: servicecommon.String("subnet-1")}

	tests := []struct {
		name            string
		subnetSet       *v1alpha1.SubnetSet
		existingObjects []runtime.Object
		mockSetup       func(m *pkg_mock.MockSubnetServiceProvider)
		wantErr         bool
		expectedLen     int
	}{
		{
			name: "Return subnets by Index when SubnetNames is empty",
			subnetSet: &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{Name: "subnetset-1", Namespace: namespace, UID: types.UID(subnetSetUID)},
				Spec:       v1alpha1.SubnetSetSpec{},
			},
			mockSetup: func(m *pkg_mock.MockSubnetServiceProvider) {
				m.On("GetSubnetsByIndex", servicecommon.TagScopeSubnetSetCRUID, subnetSetUID).
					Return([]*model.VpcSubnet{nsxSubnet})
			},
			wantErr:     false,
			expectedLen: 1,
		},
		{
			name: "Successfully fetch specific subnets by name",
			subnetSet: &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{Name: "subnetset-2", Namespace: namespace},
				Spec:       v1alpha1.SubnetSetSpec{SubnetNames: &[]string{"subnet-1"}},
			},
			existingObjects: []runtime.Object{
				&v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "subnet-1", Namespace: namespace}},
			},
			mockSetup: func(m *pkg_mock.MockSubnetServiceProvider) {
				m.On("GetSubnetByCR", mock.AnythingOfType("*v1alpha1.Subnet")).Return(nsxSubnet, nil)
			},
			wantErr:     false,
			expectedLen: 1,
		},
		{
			name: "Error when Subnet CR does not exist",
			subnetSet: &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{Name: "subnetset-3", Namespace: namespace},
				Spec:       v1alpha1.SubnetSetSpec{SubnetNames: &[]string{"missing-sub"}},
			},
			existingObjects: []runtime.Object{},
			mockSetup:       func(m *pkg_mock.MockSubnetServiceProvider) {},
			wantErr:         true,
			expectedLen:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tt.existingObjects...).Build()
			mockSvc := new(pkg_mock.MockSubnetServiceProvider)
			tt.mockSetup(mockSvc)

			result, err := GetNSXSubnetsForSubnetSet(k8sClient, tt.subnetSet, mockSvc)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedLen, len(result))
			}
			mockSvc.AssertExpectations(t)
		})
	}
}

func TestGetSubnetFromSubnetSet(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	ns := "ns-1"
	path := "/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1"
	nsxSubnet := &model.VpcSubnet{Id: servicecommon.String("subnet-1"), Path: &path}

	tests := []struct {
		name            string
		subnetSet       *v1alpha1.SubnetSet
		existingObjects []runtime.Object
		mockSetup       func(ms *pkg_mock.MockSubnetServiceProvider, mp *pkg_mock.MockSubnetPortServiceProvider)
		expectedPath    string
		wantErr         bool
		errContains     string
	}{
		{
			name: "Success - First subnet has capacity",
			subnetSet: &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{Name: "subnetset-1", Namespace: ns},
				Spec:       v1alpha1.SubnetSetSpec{SubnetNames: &[]string{"subnet-1"}},
			},
			existingObjects: []runtime.Object{
				&v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "subnet-1", Namespace: ns}},
			},
			mockSetup: func(ms *pkg_mock.MockSubnetServiceProvider, mp *pkg_mock.MockSubnetPortServiceProvider) {
				// We use mock.Anything here to avoid pointer address issues
				ms.On("GetSubnetByCR", mock.Anything).Return(nsxSubnet, nil)
				mp.On("AllocatePortFromSubnet", mock.Anything).Return(true, nil)
			},
			expectedPath: path,
			wantErr:      false,
		},
		{
			name: "Success - Second subnet works after first is full",
			subnetSet: &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{Name: "subnetset-2", Namespace: ns},
				Spec:       v1alpha1.SubnetSetSpec{SubnetNames: &[]string{"full-subnet", "ok-subnet"}},
			},
			existingObjects: []runtime.Object{
				&v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "full-subnet", Namespace: ns}},
				&v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "ok-subnet", Namespace: ns}},
			},
			mockSetup: func(ms *pkg_mock.MockSubnetServiceProvider, mp *pkg_mock.MockSubnetPortServiceProvider) {
				ms.On("GetSubnetByCR", mock.Anything).Return(nsxSubnet, nil)
				// First call returns false (full), second call returns true
				mp.On("AllocatePortFromSubnet", mock.Anything).Return(false, nil).Once()
				mp.On("AllocatePortFromSubnet", mock.Anything).Return(true, nil).Once()
			},
			expectedPath: path,
			wantErr:      false,
		},
		{
			name: "Failure - All subnets exhausted",
			subnetSet: &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{Name: "subnetset-3", Namespace: ns},
				Spec:       v1alpha1.SubnetSetSpec{SubnetNames: &[]string{"subnet-1"}},
			},
			existingObjects: []runtime.Object{
				&v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "subnet-1", Namespace: ns}},
			},
			mockSetup: func(ms *pkg_mock.MockSubnetServiceProvider, mp *pkg_mock.MockSubnetPortServiceProvider) {
				ms.On("GetSubnetByCR", mock.Anything).Return(nsxSubnet, nil)
				mp.On("AllocatePortFromSubnet", mock.Anything).Return(false, nil)
			},
			expectedPath: "",
			wantErr:      true,
		},
		{
			name: "Failure - K8s Client Get fails",
			subnetSet: &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{Name: "subnetset-4", Namespace: ns},
				Spec:       v1alpha1.SubnetSetSpec{SubnetNames: &[]string{"missing-subnet"}},
			},
			existingObjects: []runtime.Object{},
			mockSetup: func(ms *pkg_mock.MockSubnetServiceProvider, mp *pkg_mock.MockSubnetPortServiceProvider) {
			},
			wantErr:     true,
			errContains: "not found",
		},
		{
			name: "Failure - GetSubnetByCR fails",
			subnetSet: &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{Name: "subnetset-5", Namespace: ns},
				Spec:       v1alpha1.SubnetSetSpec{SubnetNames: &[]string{"subnet-1"}},
			},
			existingObjects: []runtime.Object{
				&v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "subnet-1", Namespace: ns}},
			},
			mockSetup: func(ms *pkg_mock.MockSubnetServiceProvider, mp *pkg_mock.MockSubnetPortServiceProvider) {
				ms.On("GetSubnetByCR", mock.Anything).Return(nil, errors.New("nsx-service-down"))
			},
			wantErr:     true,
			errContains: "nsx-service-down",
		},
		{
			name: "Failure - AllocatePortFromSubnet fails",
			subnetSet: &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{Name: "subnetset-6", Namespace: ns},
				Spec:       v1alpha1.SubnetSetSpec{SubnetNames: &[]string{"subnet-1"}},
			},
			existingObjects: []runtime.Object{
				&v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "subnet-1", Namespace: ns}},
			},
			mockSetup: func(ms *pkg_mock.MockSubnetServiceProvider, mp *pkg_mock.MockSubnetPortServiceProvider) {
				ms.On("GetSubnetByCR", mock.Anything).Return(nsxSubnet, nil)
				mp.On("AllocatePortFromSubnet", mock.Anything).Return(false, errors.New("capacity-check-failed"))
			},
			wantErr:     true,
			errContains: "capacity-check-failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tt.existingObjects...).Build()
			mockSubnetSvc := new(pkg_mock.MockSubnetServiceProvider)
			mockPortSvc := new(pkg_mock.MockSubnetPortServiceProvider)
			tt.mockSetup(mockSubnetSvc, mockPortSvc)

			// Execute
			result, err := GetSubnetFromSubnetSet(client, tt.subnetSet, mockSubnetSvc, mockPortSvc)

			// Assert
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedPath, result)
			}

			mockSubnetSvc.AssertExpectations(t)
			mockPortSvc.AssertExpectations(t)
		})
	}
}

func TestIsNamespaceVLANBacked(t *testing.T) {
	tests := []struct {
		name        string
		namespace   string
		setupFunc   func(client.Client)
		expected    bool
		expectedErr string
	}{
		{
			name:      "VLANBackedVPC",
			namespace: "ns-1",
			setupFunc: func(client client.Client) {
				networkInfo := &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "network-info-1",
						Namespace: "ns-1",
					},
					VPCs: []v1alpha1.VPCState{
						{
							NetworkStack: v1alpha1.VLANBackedVPC,
						},
					},
				}
				assert.NoError(t, client.Create(context.TODO(), networkInfo))
			},
			expected: true,
		},
		{
			name:      "FullStackVPC",
			namespace: "ns-1",
			setupFunc: func(client client.Client) {
				networkInfo := &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "network-info-1",
						Namespace: "ns-1",
					},
					VPCs: []v1alpha1.VPCState{
						{
							NetworkStack: v1alpha1.FullStackVPC,
						},
					},
				}
				assert.NoError(t, client.Create(context.TODO(), networkInfo))
			},
			expected: false,
		},
		{
			name:      "NetworkInfoExistsButNoNetworkStack",
			namespace: "ns-1",
			setupFunc: func(client client.Client) {
				networkInfo := &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "network-info-1",
						Namespace: "ns-1",
					},
					VPCs: []v1alpha1.VPCState{
						{
							LoadBalancerIPAddresses: "",
						},
					},
				}
				assert.NoError(t, client.Create(context.TODO(), networkInfo))
			},
			expectedErr: "NetworkStack is not set in NetworkInfo CRD",
		},
		{
			name:      "NetworkInfoExistsButNoVPCs",
			namespace: "ns-1",
			setupFunc: func(client client.Client) {
				networkInfo := &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "network-info-1",
						Namespace: "ns-1",
					},
					VPCs: []v1alpha1.VPCState{}, // Empty VPCs
				}
				assert.NoError(t, client.Create(context.TODO(), networkInfo))
			},
			expectedErr: "no VPC found in NetworkInfo",
		},
		{
			name:        "NoNetworkInfo",
			namespace:   "ns-1",
			setupFunc:   func(client client.Client) {},
			expectedErr: "no NetworkInfo found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newScheme := runtime.NewScheme()
			utilruntime.Must(v1alpha1.AddToScheme(newScheme))
			fakeClient := fake.NewClientBuilder().WithScheme(newScheme).Build()

			tt.setupFunc(fakeClient)

			got, err := IsNamespaceInTepLessMode(fakeClient, tt.namespace)
			if tt.expectedErr != "" {
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, got)
			}
		})
	}
}

func TestGetVpcNetworkConfig(t *testing.T) {
	testCases := []struct {
		name      string
		patches   func(r *vpc.VPCService) *gomonkey.Patches
		expectErr bool
	}{
		{
			name: "Success",
			patches: func(r *vpc.VPCService) *gomonkey.Patches {
				vpcnetworkConfig := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DefaultSubnetSize: 32}}
				return gomonkey.ApplyMethod(reflect.TypeOf(r), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return vpcnetworkConfig, nil
				})
			},
			expectErr: false,
		},
		{
			name: "GetVPCNetworkConfigByNamespace error",
			patches: func(r *vpc.VPCService) *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(r), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return nil, errors.New("get error")
				})
			},
			expectErr: true,
		},
		{
			name: "VPCNetworkConfig nil",
			patches: func(r *vpc.VPCService) *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(r), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return nil, nil
				})
			},
			expectErr: false,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			subnetsetCR := &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-subnetset",
					Namespace: "test-namespace",
				},
			}
			service := createVPCService([]client.Object{subnetsetCR})
			if testCase.patches != nil {
				patches := testCase.patches(service)
				defer patches.Reset()
			}
			_, err := GetVpcNetworkConfig(service, subnetsetCR.Namespace)
			if testCase.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func createVPCService(objects []client.Object) *vpc.VPCService {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(objects...).Build()
	service := &vpc.VPCService{
		Service: servicecommon.Service{
			Client:    fakeClient,
			NSXClient: &nsx.Client{},
		},
	}
	return service
}
func TestGetDefaultAccessMode(t *testing.T) {
	testCases := []struct {
		name               string
		patches            func(r *vpc.VPCService) *gomonkey.Patches
		expectErr          bool
		expectedAccessMode v1alpha1.AccessMode
	}{
		{
			name: "Success with FullStackVPC",
			patches: func(r *vpc.VPCService) *gomonkey.Patches {
				vpcnetworkConfig := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DefaultSubnetSize: 16}}
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return vpcnetworkConfig, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r), "GetNetworkStackFromNC", func(_ *vpc.VPCService, config *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
				})
				return patches
			},
			expectErr:          false,
			expectedAccessMode: v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
		},
		{
			name: "Success with other stack",
			patches: func(r *vpc.VPCService) *gomonkey.Patches {
				vpcnetworkConfig := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DefaultSubnetSize: 16}}
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return vpcnetworkConfig, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r), "GetNetworkStackFromNC", func(_ *vpc.VPCService, config *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.NetworkStackType("Other"), nil
				})
				return patches
			},
			expectErr:          false,
			expectedAccessMode: v1alpha1.AccessMode(v1alpha1.AccessModePublic),
		},
		{
			name: "GetVPCNetworkConfigByNamespace error",
			patches: func(r *vpc.VPCService) *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(r), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return nil, errors.New("get error")
				})
			},
			expectErr: true,
		},
		{
			name: "GetNetworkStackFromNC error",
			patches: func(r *vpc.VPCService) *gomonkey.Patches {
				vpcnetworkConfig := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DefaultSubnetSize: 16}}
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return vpcnetworkConfig, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r), "GetNetworkStackFromNC", func(_ *vpc.VPCService, config *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return "", errors.New("stack error")
				})
				return patches
			},
			expectErr: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			subnetsetCR := &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-subnetset",
					Namespace: "test-namespace",
				},
			}
			service := createVPCService([]client.Object{subnetsetCR})
			if testCase.patches != nil {
				patches := testCase.patches(service)
				defer patches.Reset()
			}
			accessMode, _, err := GetDefaultAccessMode(service, subnetsetCR.Namespace)
			if testCase.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, testCase.expectedAccessMode, accessMode)
			}
		})
	}
}
