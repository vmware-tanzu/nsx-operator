/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package namespace

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	ctlcommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicetypes "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

func createNameSpaceReconciler(objs []client.Object) *NamespaceReconciler {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))

	// Create a fake client builder
	clientBuilder := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(objs...)

	// Create a separate client builder for APIReader
	apiReaderBuilder := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(objs...)

	fakeClient := clientBuilder.Build()
	fakeAPIReader := apiReaderBuilder.Build()

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint:   "vmc-enforcementpoint",
			UseAVILoadBalancer: false,
		},
	}

	service := &vpc.VPCService{
		Service: servicetypes.Service{
			Client:    fakeClient,
			NSXClient: &nsx.Client{},
			NSXConfig: nsxConfig,
		},
	}

	subnetService := &subnet.SubnetService{
		Service: servicetypes.Service{
			Client:    fakeClient,
			NSXClient: &nsx.Client{},
			NSXConfig: nsxConfig,
		},
	}

	nsReconciler := &NamespaceReconciler{
		Client:        fakeClient,
		Scheme:        newScheme,
		APIReader:     fakeAPIReader,
		VPCService:    service,
		SubnetService: subnetService,
		NSXConfig:     nsxConfig,
	}
	nsReconciler.SubnetStatusUpdater = ctlcommon.NewStatusUpdater(nsReconciler.Client, nsReconciler.SubnetService.NSXConfig, nil, "Subnet", "Subnet", "Subnet")
	return nsReconciler
}

func TestGetDefaultNetworkConfigName(t *testing.T) {
	fakeNC := &v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "fake-name",
			Annotations: map[string]string{servicetypes.AnnotationDefaultNetworkConfig: "true"},
		},
	}
	tests := []struct {
		name       string
		exist      bool
		nc         *v1alpha1.VPCNetworkConfiguration
		expectName string
	}{
		{"1", false, nil, ""},
		{"2", true, fakeNC, "fake-name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := createNameSpaceReconciler(nil)
			patch := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetDefaultNetworkConfig", func(_ *vpc.VPCService) (*v1alpha1.VPCNetworkConfiguration, error) {
				if !tt.exist {
					return tt.nc, fmt.Errorf("not found")
				}
				return tt.nc, nil
			})
			defer patch.Reset()
			name, err := r.getDefaultNetworkConfigName()
			assert.Equal(t, tt.expectName, name)
			if name == "" {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
			patch.Reset()
		})
	}
}

func TestNamespaceReconciler_Reconcile(t *testing.T) {
	nc := v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "fake-VPCNetworkConfig",
			Annotations: map[string]string{servicetypes.AnnotationDefaultNetworkConfig: "true"},
		},
		Spec: v1alpha1.VPCNetworkConfigurationSpec{
			VPCConnectivityProfile: "",
			NSXProject:             "/orgs/org/projects/project",
			PrivateIPs:             nil,
			DefaultSubnetSize:      0,
			VPC:                    "fake-patch",
		},
	}
	testCases := []struct {
		name                string
		req                 ctrl.Request
		expectRes           ctrl.Result
		expectErrStr        string
		patches             func(r *NamespaceReconciler) *gomonkey.Patches
		existingNamespaceCR *v1.Namespace
		expectNamespaceCR   *v1.Namespace
	}{
		{
			name: "Namespace CR not found",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-ns"}},
			patches: func(r *NamespaceReconciler) *gomonkey.Patches {
				return nil
			},
			expectRes:           ctrl.Result{},
			existingNamespaceCR: nil,
		},
		{
			name: "Namespace with Finalizer",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-ns"}},
			patches: func(r *NamespaceReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "deleteDefaultSubnetSet", func(_ *NamespaceReconciler, _ string) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "deleteAllSharedSubnets", func(_ *NamespaceReconciler, _ string) error {
					return nil
				})
				return patches
			},
			expectRes: ctrl.Result{},
			existingNamespaceCR: &v1.Namespace{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "test-ns", DeletionTimestamp: &metav1.Time{Time: time.Now()}, Finalizers: []string{"test-Finalizers"}},
				Spec:       v1.NamespaceSpec{},
				Status:     v1.NamespaceStatus{},
			},
		},
		{
			name: "Namespace update using default NetworkConfig",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-ns"}},
			patches: func(r *NamespaceReconciler) *gomonkey.Patches {
				// GetDefaultNetworkConfig
				return nil
			},
			expectRes: ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second},
			existingNamespaceCR: &v1.Namespace{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "test-ns"},
				Spec:       v1.NamespaceSpec{},
				Status:     v1.NamespaceStatus{},
			},
		},
		{
			name: "Namespace update with error missing NetworkConfig",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-ns"}},
			patches: func(r *NamespaceReconciler) *gomonkey.Patches {
				// GetDefaultNetworkConfig
				patches := gomonkey.ApplyMethod(reflect.TypeOf(&vpc.VPCService{}), "GetDefaultNetworkConfig", func(_ *vpc.VPCService) (*v1alpha1.VPCNetworkConfiguration, error) {
					return &nc, nil
				})
				return patches
			},
			expectErrStr: "missing NetworkConfig",
			expectRes:    ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second},
			existingNamespaceCR: &v1.Namespace{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "test-ns"},
				Spec:       v1.NamespaceSpec{},
				Status:     v1.NamespaceStatus{},
			},
		},
		{
			name: "Namespace update",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-ns"}},
			patches: func(r *NamespaceReconciler) *gomonkey.Patches {
				// GetDefaultNetworkConfig
				patches := gomonkey.ApplyMethod(reflect.TypeOf(&vpc.VPCService{}), "GetDefaultNetworkConfig", func(_ *vpc.VPCService) (*v1alpha1.VPCNetworkConfiguration, error) {
					return &nc, nil
				})
				patches.ApplyMethod(reflect.TypeOf(&vpc.VPCService{}), "GetVPCNetworkConfig", func(_ *vpc.VPCService, ncCRName string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &nc, true, nil
				})
				// Mock syncSharedSubnets to return nil (no error)
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "syncSharedSubnets", func(_ *NamespaceReconciler, _ context.Context, _ string,
					_ *v1alpha1.VPCNetworkConfiguration) error {
					return nil
				})
				// Mock createNetworkInfoCR to return nil (no error)
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "createNetworkInfoCR", func(_ *NamespaceReconciler, _ context.Context,
					_ client.Object, _ string) (*v1alpha1.NetworkInfo, error) {
					return nil, nil
				})
				// Mock createDefaultSubnetSet to return nil (no error)
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "createDefaultSubnetSet", func(_ *NamespaceReconciler, _ context.Context, _ string,
					_ int) error {
					return nil
				})
				return patches
			},
			expectRes: ctrl.Result{},
			existingNamespaceCR: &v1.Namespace{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "test-ns"},
				Spec:       v1.NamespaceSpec{},
				Status:     v1.NamespaceStatus{},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var objs []client.Object
			if tc.existingNamespaceCR != nil {
				objs = append(objs, tc.existingNamespaceCR)
			}
			r := createNameSpaceReconciler(objs)

			if tc.patches(r) != nil {
				patches := tc.patches(r)
				defer patches.Reset()
			}

			res, err := r.Reconcile(context.Background(), tc.req)

			assert.Equal(t, tc.expectRes, res)

			if tc.expectErrStr != "" {
				assert.ErrorContains(t, err, tc.expectErrStr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNamespaceReconciler_StartController(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects().Build()
	fakeAPIReader := fake.NewClientBuilder().WithObjects().Build()
	vpcService := &vpc.VPCService{
		Service: servicetypes.Service{
			Client: fakeClient,
		},
	}
	subnetService := &subnet.SubnetService{
		Service: servicetypes.Service{
			Client: fakeClient,
		},
	}
	mockMgr := &MockManager{scheme: runtime.NewScheme(), client: fakeClient, apiReader: fakeAPIReader}
	patches := gomonkey.ApplyFunc((*NamespaceReconciler).setupWithManager, func(r *NamespaceReconciler, mgr manager.Manager) error {
		return nil
	})
	patches.ApplyFunc(ctlcommon.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context) error) {
		return
	})
	defer patches.Reset()
	r := NewNamespaceReconciler(mockMgr, nil, vpcService, subnetService)
	err := r.StartController(mockMgr, nil)
	assert.Nil(t, err)
}

func TestGetAccessMode(t *testing.T) {
	tests := []struct {
		name           string
		subnetSetName  string
		expectedResult v1alpha1.AccessMode
	}{
		{
			name:           "VM Subnet Set",
			subnetSetName:  servicetypes.DefaultVMSubnetSet,
			expectedResult: v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
		},
		{
			name:           "Pod Subnet Set",
			subnetSetName:  servicetypes.DefaultPodSubnetSet,
			expectedResult: v1alpha1.AccessMode(v1alpha1.AccessModeProject),
		},
		{
			name:           "Unknown Subnet Set",
			subnetSetName:  "unknown-subnet-set",
			expectedResult: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getAccessMode(tt.subnetSetName)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestCreateDefaultSubnetSet(t *testing.T) {
	tests := []struct {
		name               string
		namespace          string
		defaultSubnetSize  int
		existingResources  []client.Object
		expectedError      bool
		expectedSubnetSets int
		setupMocks         func(r *NamespaceReconciler) *gomonkey.Patches
	}{
		{
			name:               "Success case - create new SubnetSets",
			namespace:          "test-ns",
			defaultSubnetSize:  24,
			existingResources:  []client.Object{},
			expectedError:      false,
			expectedSubnetSets: 0, // VM and Pod subnet sets
			setupMocks:         func(r *NamespaceReconciler) *gomonkey.Patches { return nil },
		},
		{
			name:              "Success case - SubnetSets already exist",
			namespace:         "test-ns",
			defaultSubnetSize: 24,
			existingResources: []client.Object{
				&v1alpha1.SubnetSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      servicetypes.DefaultVMSubnetSet,
						Namespace: "test-ns",
						Labels: map[string]string{
							servicetypes.LabelDefaultSubnetSet: servicetypes.LabelDefaultVMSubnetSet,
						},
					},
					Spec: v1alpha1.SubnetSetSpec{
						AccessMode:     v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
						IPv4SubnetSize: 24,
					},
				},
				&v1alpha1.SubnetSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      servicetypes.DefaultPodSubnetSet,
						Namespace: "test-ns",
						Labels: map[string]string{
							servicetypes.LabelDefaultSubnetSet: servicetypes.LabelDefaultPodSubnetSet,
						},
					},
					Spec: v1alpha1.SubnetSetSpec{
						AccessMode:     v1alpha1.AccessMode(v1alpha1.AccessModeProject),
						IPv4SubnetSize: 24,
					},
				},
			},
			expectedError:      false,
			expectedSubnetSets: 2, // VM and Pod subnet sets
			setupMocks:         func(r *NamespaceReconciler) *gomonkey.Patches { return nil },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := createNameSpaceReconciler(tt.existingResources)

			if tt.setupMocks != nil {
				patches := tt.setupMocks(r)
				if patches != nil {
					defer patches.Reset()
				}
			}

			// Call the function being tested
			err := r.createDefaultSubnetSet(context.Background(), tt.namespace, tt.defaultSubnetSize)

			// Check the result
			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)

				// Verify that the SubnetSets were created
				subnetSetList := &v1alpha1.SubnetSetList{}
				err = r.Client.List(context.Background(), subnetSetList, client.InNamespace(tt.namespace))
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedSubnetSets, len(subnetSetList.Items))

				// Check that the SubnetSets have the correct properties
				if len(subnetSetList.Items) > 0 {
					for _, subnetSet := range subnetSetList.Items {
						if subnetSet.Name == servicetypes.DefaultVMSubnetSet {
							assert.Equal(t, v1alpha1.AccessMode(v1alpha1.AccessModePrivate), subnetSet.Spec.AccessMode)
						} else if subnetSet.Name == servicetypes.DefaultPodSubnetSet {
							assert.Equal(t, v1alpha1.AccessMode(v1alpha1.AccessModeProject), subnetSet.Spec.AccessMode)
						}
						assert.Equal(t, tt.defaultSubnetSize, subnetSet.Spec.IPv4SubnetSize)
					}
				}
			}
		})
	}
}

func TestDeleteDefaultSubnetSet(t *testing.T) {
	tests := []struct {
		name              string
		namespace         string
		existingResources []client.Object
		expectedError     bool
		setupMocks        func(r *NamespaceReconciler) *gomonkey.Patches
	}{
		{
			name:              "Success case - SubnetSets don't exist",
			namespace:         "test-ns",
			existingResources: []client.Object{},
			expectedError:     false,
			setupMocks:        func(r *NamespaceReconciler) *gomonkey.Patches { return nil },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := createNameSpaceReconciler(tt.existingResources)

			if tt.setupMocks != nil {
				patches := tt.setupMocks(r)
				if patches != nil {
					defer patches.Reset()
				}
			}

			// Call the function being tested
			err := r.deleteDefaultSubnetSet(tt.namespace)

			// Check the result
			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)

				// Verify that the SubnetSets were deleted
				for _, name := range []string{servicetypes.DefaultVMSubnetSet, servicetypes.DefaultPodSubnetSet} {
					subnetSet := &v1alpha1.SubnetSet{}
					err = r.Client.Get(context.Background(), client.ObjectKey{Namespace: tt.namespace, Name: name}, subnetSet)
					assert.True(t, apierrors.IsNotFound(err), "SubnetSet should be deleted")
				}
			}
		})
	}
}

type MockManager struct {
	ctrl.Manager
	client    client.Client
	apiReader client.Reader
	scheme    *runtime.Scheme
}

func (m *MockManager) GetClient() client.Client {
	return m.client
}

func (m *MockManager) GetAPIReader() client.Reader {
	return m.apiReader
}

func (m *MockManager) GetScheme() *runtime.Scheme {
	return m.scheme
}

func (m *MockManager) GetEventRecorderFor(name string) record.EventRecorder {
	return nil
}

func (m *MockManager) Add(runnable manager.Runnable) error {
	return nil
}

func (m *MockManager) Start(context.Context) error {
	return nil
}
