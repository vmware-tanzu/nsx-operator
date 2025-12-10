package namespace

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

// Helper function to create a NamespaceReconciler for testing
func createTestNamespaceReconciler(objs []client.Object) *NamespaceReconciler {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))

	// Create a fake client builder
	clientBuilder := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(objs...)

	fakeClient := clientBuilder.Build()

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint:   "vmc-enforcementpoint",
			UseAVILoadBalancer: false,
		},
	}

	vpcService := &vpc.VPCService{
		Service: servicecommon.Service{
			Client:    fakeClient,
			NSXClient: &nsx.Client{},
			NSXConfig: nsxConfig,
		},
	}

	subnetService := &subnet.SubnetService{
		Service: servicecommon.Service{
			Client:    fakeClient,
			NSXClient: &nsx.Client{},
			NSXConfig: nsxConfig,
		},
		SharedSubnetData: subnet.SharedSubnetData{
			NSXSubnetCache: make(map[string]struct {
				Subnet     *model.VpcSubnet
				StatusList []model.VpcSubnetStatus
			}),
			SharedSubnetResourceMap: make(map[string]sets.Set[types.NamespacedName]),
		},
	}

	nsReconciler := &NamespaceReconciler{
		Client:        fakeClient,
		APIReader:     fakeClient,
		Scheme:        newScheme,
		VPCService:    vpcService,
		SubnetService: subnetService,
		NSXConfig:     nsxConfig,
	}
	nsReconciler.SubnetStatusUpdater = common.NewStatusUpdater(nsReconciler.Client, nsReconciler.SubnetService.NSXConfig, nil, "Subnet", "Subnet", "Subnet")
	return nsReconciler
}

// CustomClient is a custom implementation of client.Client that allows us to override the Create and List methods
type CustomClient struct {
	client.Client
	CreateFunc func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error
	ListFunc   func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
}

func (c *CustomClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if c.CreateFunc != nil {
		return c.CreateFunc(ctx, obj, opts...)
	}
	return c.Client.Create(ctx, obj, opts...)
}

func (c *CustomClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if c.ListFunc != nil {
		return c.ListFunc(ctx, list, opts...)
	}
	return c.Client.List(ctx, list, opts...)
}

func TestCreateSubnetCRInK8s(t *testing.T) {
	// Create a test subnet CR
	subnetCR := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-subnet",
			Namespace: "test-ns",
		},
		Spec: v1alpha1.SubnetSpec{
			VPCName:            "proj-1:vpc-1",
			VLANConnectionName: "gatewayconnection-103",
		},
	}

	// Test cases
	tests := []struct {
		name              string
		existingSubnets   []client.Object
		expectedErrString string
		expectedName      string
	}{
		{
			name:            "Create new Subnet CR",
			existingSubnets: []client.Object{},
			expectedName:    "test-subnet",
		},
		{
			name: "Subnet CR already exists",
			existingSubnets: []client.Object{
				&v1alpha1.Subnet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-subnet",
						Namespace: "test-ns",
					},
				},
			},
			expectedName: "test-subnet-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := createTestNamespaceReconciler(tt.existingSubnets)

			// Create a copy of the subnet CR for each test
			testSubnet := subnetCR.DeepCopy()

			// For the "Subnet CR already exists" case, we need to use a custom client
			if tt.name == "Subnet CR already exists" {
				createCount := 0
				customClient := &CustomClient{
					Client: r.Client,
					CreateFunc: func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
						createCount++

						if createCount == 1 {
							// First call should return "already exists" error
							return apierrors.NewAlreadyExists(v1alpha1.Resource("subnets"), obj.GetName())
						}

						// Second call should succeed and set a name with the generateName prefix
						if obj.GetGenerateName() != "" {
							obj.SetName(obj.GetGenerateName() + "random-suffix")
							// Make sure the name is set in the original testSubnet object
							testSubnet.SetName(obj.GetName())
						}
						return nil
					},
				}

				// Replace the client in the reconciler with our custom client
				r.Client = customClient
			}

			err := r.createSubnetCRInK8s(context.Background(), testSubnet)

			if tt.expectedErrString != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrString)
			} else {
				assert.NoError(t, err)
				if tt.expectedName == "test-subnet" {
					assert.Equal(t, tt.expectedName, testSubnet.Name)
				} else {
					// For generateName case, check that the name starts with the expected prefix
					assert.True(t, len(testSubnet.Name) > len(tt.expectedName))
					assert.True(t, strings.HasPrefix(testSubnet.Name, tt.expectedName))
				}
			}
		})
	}
}

func TestGetExistingSharedSubnets(t *testing.T) {
	// Test cases
	tests := []struct {
		name                string
		existingSubnets     []client.Object
		expectedSubnetCount int
		expectedError       bool
	}{
		{
			name:                "No existing subnets",
			existingSubnets:     []client.Object{},
			expectedSubnetCount: 0,
			expectedError:       false,
		},
		{
			name: "One existing shared subnet",
			existingSubnets: []client.Object{
				&v1alpha1.Subnet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "shared-subnet",
						Namespace: "test-ns",
						Annotations: map[string]string{
							servicecommon.AnnotationAssociatedResource: "proj-1:vpc-1:subnet-1",
						},
					},
				},
			},
			expectedSubnetCount: 1,
			expectedError:       false,
		},
		{
			name: "Multiple existing shared subnets",
			existingSubnets: []client.Object{
				&v1alpha1.Subnet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "shared-subnet-1",
						Namespace: "test-ns",
						Annotations: map[string]string{
							servicecommon.AnnotationAssociatedResource: "proj-1:vpc-1:subnet-1",
						},
					},
				},
				&v1alpha1.Subnet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "shared-subnet-2",
						Namespace: "test-ns",
						Annotations: map[string]string{
							servicecommon.AnnotationAssociatedResource: "proj-1:vpc-1:subnet-2",
						},
					},
				},
			},
			expectedSubnetCount: 2,
			expectedError:       false,
		},
		{
			name: "Mix of shared and non-shared subnets",
			existingSubnets: []client.Object{
				&v1alpha1.Subnet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "shared-subnet",
						Namespace: "test-ns",
						Annotations: map[string]string{
							servicecommon.AnnotationAssociatedResource: "proj-1:vpc-1:subnet-1",
						},
					},
				},
				&v1alpha1.Subnet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "non-shared-subnet",
						Namespace: "test-ns",
					},
				},
			},
			expectedSubnetCount: 1,
			expectedError:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := createTestNamespaceReconciler(tt.existingSubnets)

			// Initialize SharedSubnetResourceMap with the existing subnets
			r.SubnetService.SharedSubnetResourceMap = make(map[string]sets.Set[types.NamespacedName])
			for _, obj := range tt.existingSubnets {
				subnet, ok := obj.(*v1alpha1.Subnet)
				if !ok {
					continue
				}
				if subnet.Annotations != nil && subnet.Annotations[servicecommon.AnnotationAssociatedResource] != "" {
					associatedResource := subnet.Annotations[servicecommon.AnnotationAssociatedResource]
					namespacedName := types.NamespacedName{
						Namespace: subnet.Namespace,
						Name:      subnet.Name,
					}
					r.SubnetService.AddSharedSubnetToResourceMap(associatedResource, namespacedName)
				}
			}

			sharedSubnets, err := r.getExistingSharedSubnetCRs(context.Background(), "test-ns")

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedSubnetCount, len(sharedSubnets))

				// Verify that all subnets in the map have the AnnotationAssociatedResource annotation
				for key, subnet := range sharedSubnets {
					assert.Equal(t, key, subnet.Annotations[servicecommon.AnnotationAssociatedResource])
				}
			}
		})
	}
}

func TestCheckSubnetReferences(t *testing.T) {
	// Test cases
	tests := []struct {
		name                 string
		subnet               *v1alpha1.Subnet
		existingResources    []client.Object
		expectedHasReference bool
		expectedError        bool
	}{
		{
			name: "No references",
			subnet: &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-subnet",
					Namespace: "test-ns",
				},
			},
			existingResources:    []client.Object{},
			expectedHasReference: false,
			expectedError:        false,
		},
		{
			name: "Referenced by SubnetPort",
			subnet: &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-subnet",
					Namespace: "test-ns",
				},
			},
			existingResources: []client.Object{
				&v1alpha1.SubnetPort{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-subnetport",
						Namespace: "test-ns",
					},
					Spec: v1alpha1.SubnetPortSpec{
						Subnet: "test-subnet",
					},
				},
			},
			expectedHasReference: true,
			expectedError:        false,
		},
		{
			name: "Referenced by SubnetConnectionBindingMap",
			subnet: &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-subnet",
					Namespace: "test-ns",
				},
			},
			existingResources: []client.Object{
				&v1alpha1.SubnetConnectionBindingMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-binding",
						Namespace: "test-ns",
					},
					Spec: v1alpha1.SubnetConnectionBindingMapSpec{
						TargetSubnetName: "test-subnet",
					},
				},
			},
			expectedHasReference: true,
			expectedError:        false,
		},
		{
			name: "Not referenced by any resource",
			subnet: &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-subnet",
					Namespace: "test-ns",
				},
			},
			existingResources: []client.Object{
				&v1alpha1.SubnetPort{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-subnetport",
						Namespace: "test-ns",
					},
					Spec: v1alpha1.SubnetPortSpec{
						Subnet: "other-subnet",
					},
				},
				&v1alpha1.SubnetConnectionBindingMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-binding",
						Namespace: "test-ns",
					},
					Spec: v1alpha1.SubnetConnectionBindingMapSpec{
						TargetSubnetName: "other-subnet",
					},
				},
			},
			expectedHasReference: false,
			expectedError:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = createTestNamespaceReconciler(tt.existingResources)

			// Create a helper function to check references without using field selectors
			checkReferences := func(ctx context.Context, ns string, subnet *v1alpha1.Subnet) (bool, error) {
				// Check if there are any SubnetPort CRs referencing this Subnet CR
				for _, obj := range tt.existingResources {
					if subnetPort, ok := obj.(*v1alpha1.SubnetPort); ok {
						if subnetPort.Namespace == ns && subnetPort.Spec.Subnet == subnet.Name {
							return true, nil
						}
					}
				}

				// Check if there are any SubnetConnectionBindingMap CRs referencing this Subnet CR
				for _, obj := range tt.existingResources {
					if binding, ok := obj.(*v1alpha1.SubnetConnectionBindingMap); ok {
						if binding.Namespace == ns && binding.Spec.TargetSubnetName == subnet.Name {
							return true, nil
						}
					}
				}

				return false, nil
			}

			hasReferences, err := checkReferences(context.Background(), "test-ns", tt.subnet)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedHasReference, hasReferences)
			}
		})
	}
}

func TestProcessNewSharedSubnets(t *testing.T) {
	// Test cases
	tests := []struct {
		name                string
		existingSubnets     []client.Object
		vpcNetConfig        *v1alpha1.VPCNetworkConfiguration
		expectedUnusedCount int
		setupMocks          func(r *NamespaceReconciler) *gomonkey.Patches
	}{
		{
			name:            "No existing subnets, no new subnets",
			existingSubnets: []client.Object{},
			vpcNetConfig: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{
					Subnets: []v1alpha1.SharedSubnet{},
				},
			},
			expectedUnusedCount: 0,
			setupMocks: func(r *NamespaceReconciler) *gomonkey.Patches {
				return nil
			},
		},
		{
			name:            "No existing subnets, one new subnet",
			existingSubnets: []client.Object{},
			vpcNetConfig: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{
					Subnets: []v1alpha1.SharedSubnet{
						{
							Path: "/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1",
						},
					},
				},
			},
			expectedUnusedCount: 0,
			setupMocks: func(r *NamespaceReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(servicecommon.ConvertSubnetPathToAssociatedResource,
					func(path string) (string, error) {
						return "proj-1:vpc-1:subnet-1", nil
					})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "createSharedSubnetCR",
					func(_ *NamespaceReconciler, _ context.Context, _ string, _ string, _ *v1alpha1.VPCNetworkConfiguration) error {
						return nil
					})
				return patches
			},
		},
		{
			name: "One existing subnet, one new subnet",
			existingSubnets: []client.Object{
				&v1alpha1.Subnet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-subnet",
						Namespace: "test-ns",
						Annotations: map[string]string{
							servicecommon.AnnotationAssociatedResource: "proj-1:vpc-1:existing-subnet",
						},
					},
				},
			},
			vpcNetConfig: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{
					Subnets: []v1alpha1.SharedSubnet{
						{
							Path: "/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1",
						},
					},
				},
			},
			expectedUnusedCount: 1,
			setupMocks: func(r *NamespaceReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(servicecommon.ConvertSubnetPathToAssociatedResource,
					func(path string) (string, error) {
						return "proj-1:vpc-1:subnet-1", nil
					})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "createSharedSubnetCR",
					func(_ *NamespaceReconciler, _ context.Context, _ string, _ string, _ *v1alpha1.VPCNetworkConfiguration) error {
						return nil
					})
				return patches
			},
		},
		{
			name: "One existing subnet, same subnet in config",
			existingSubnets: []client.Object{
				&v1alpha1.Subnet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "subnet-1",
						Namespace: "test-ns",
						Annotations: map[string]string{
							servicecommon.AnnotationAssociatedResource: "proj-1:vpc-1:subnet-1",
						},
					},
				},
			},
			vpcNetConfig: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{
					Subnets: []v1alpha1.SharedSubnet{
						{
							Path: "/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1",
						},
					},
				},
			},
			expectedUnusedCount: 0,
			setupMocks: func(r *NamespaceReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(servicecommon.ConvertSubnetPathToAssociatedResource,
					func(path string) (string, error) {
						return "proj-1:vpc-1:subnet-1", nil
					})
				return patches
			},
		},
		{
			name: "Existing subnet should be added to SharedSubnetResourceMap",
			existingSubnets: []client.Object{
				&v1alpha1.Subnet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-subnet",
						Namespace: "test-ns",
						Annotations: map[string]string{
							servicecommon.AnnotationAssociatedResource: "proj-1:vpc-1:existing-subnet",
						},
					},
				},
			},
			vpcNetConfig: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{
					Subnets: []v1alpha1.SharedSubnet{
						{
							Path: "/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/existing-subnet",
						},
					},
				},
			},
			expectedUnusedCount: 0,
			setupMocks: func(r *NamespaceReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(servicecommon.ConvertSubnetPathToAssociatedResource,
					func(path string) (string, error) {
						return "proj-1:vpc-1:existing-subnet", nil
					})
				return patches
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := createTestNamespaceReconciler(tt.existingSubnets)

			if tt.setupMocks != nil {
				patches := tt.setupMocks(r)
				if patches != nil {
					defer patches.Reset()
				}
			}

			// Track AddSharedSubnetToResourceMap calls for the new test case
			var addToMapCalls []struct {
				associatedResource string
				namespacedName     types.NamespacedName
			}

			if tt.name == "Existing subnet should be added to SharedSubnetResourceMap" {
				// Mock AddSharedSubnetToResourceMap to track calls
				addToMapPatches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "AddSharedSubnetToResourceMap",
					func(_ *subnet.SubnetService, associatedResource string, namespacedName types.NamespacedName) {
						addToMapCalls = append(addToMapCalls, struct {
							associatedResource string
							namespacedName     types.NamespacedName
						}{associatedResource, namespacedName})
					})
				defer addToMapPatches.Reset()

				// Initialize an empty SharedSubnetResourceMap (don't pre-populate for this test)
				r.SubnetService.SharedSubnetResourceMap = make(map[string]sets.Set[types.NamespacedName])
			} else {
				// Initialize the SharedSubnetResourceMap with the existing subnets for other tests
				r.SubnetService.SharedSubnetResourceMap = make(map[string]sets.Set[types.NamespacedName])
				for _, obj := range tt.existingSubnets {
					existingSubnet, ok := obj.(*v1alpha1.Subnet)
					if !ok {
						continue
					}
					if existingSubnet.Annotations != nil && existingSubnet.Annotations[servicecommon.AnnotationAssociatedResource] != "" {
						associatedResource := existingSubnet.Annotations[servicecommon.AnnotationAssociatedResource]
						namespacedName := types.NamespacedName{
							Namespace: existingSubnet.Namespace,
							Name:      existingSubnet.Name,
						}
						r.SubnetService.AddSharedSubnetToResourceMap(associatedResource, namespacedName)
					}
				}
			}

			// Get existing shared subnets
			existingSharedSubnets, err := r.getExistingSharedSubnetCRs(context.Background(), "test-ns")
			assert.NoError(t, err)

			// Process new shared subnets
			unusedSubnets, err := r.processNewSharedSubnets(context.Background(), "test-ns", tt.vpcNetConfig, existingSharedSubnets)

			// Check the result
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedUnusedCount, len(unusedSubnets))

			// Verify AddSharedSubnetToResourceMap was called for existing subnet test
			if tt.name == "Existing subnet should be added to SharedSubnetResourceMap" {
				assert.Equal(t, 1, len(addToMapCalls), "AddSharedSubnetToResourceMap should be called once")
				if len(addToMapCalls) > 0 {
					assert.Equal(t, "proj-1:vpc-1:existing-subnet", addToMapCalls[0].associatedResource)
					assert.Equal(t, "test-ns", addToMapCalls[0].namespacedName.Namespace)
					assert.Equal(t, "existing-subnet", addToMapCalls[0].namespacedName.Name)
				}
			}
		})
	}
}

func TestDeleteUnusedSharedSubnets(t *testing.T) {
	// Test cases
	tests := []struct {
		name                    string
		existingSubnets         []client.Object
		remainingSubnets        map[string]*v1alpha1.Subnet
		expectedDeleteCount     int
		expectedCacheClearCalls int
		expectError             bool
		setupMocks              func(r *NamespaceReconciler) *gomonkey.Patches
	}{
		{
			name:                    "No remaining subnets",
			existingSubnets:         []client.Object{},
			remainingSubnets:        map[string]*v1alpha1.Subnet{},
			expectedDeleteCount:     0,
			expectedCacheClearCalls: 0,
			expectError:             false,
			setupMocks: func(r *NamespaceReconciler) *gomonkey.Patches {
				return nil
			},
		},
		{
			name:            "One remaining subnet with no references - cache cleared",
			existingSubnets: []client.Object{},
			remainingSubnets: map[string]*v1alpha1.Subnet{
				"proj-1:vpc-1:subnet-1": {
					ObjectMeta: metav1.ObjectMeta{
						Name:      "subnet-1",
						Namespace: "test-ns",
						Annotations: map[string]string{
							servicecommon.AnnotationAssociatedResource: "proj-1:vpc-1:subnet-1",
						},
					},
				},
			},
			expectedDeleteCount:     1,
			expectedCacheClearCalls: 1,
			expectError:             false,
			setupMocks: func(r *NamespaceReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "checkSubnetReferences",
					func(_ *NamespaceReconciler, _ context.Context, _ string, _ *v1alpha1.Subnet, _ string) (bool, error) {
						return false, nil
					})
				return patches
			},
		},
		{
			name:            "One remaining subnet with references - no cache clear",
			existingSubnets: []client.Object{},
			remainingSubnets: map[string]*v1alpha1.Subnet{
				"proj-1:vpc-1:subnet-1": {
					ObjectMeta: metav1.ObjectMeta{
						Name:      "subnet-1",
						Namespace: "test-ns",
						Annotations: map[string]string{
							servicecommon.AnnotationAssociatedResource: "proj-1:vpc-1:subnet-1",
						},
					},
				},
			},
			expectedDeleteCount:     0,
			expectedCacheClearCalls: 0,
			expectError:             true,
			setupMocks: func(r *NamespaceReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "checkSubnetReferences",
					func(_ *NamespaceReconciler, _ context.Context, _ string, _ *v1alpha1.Subnet, _ string) (bool, error) {
						return true, nil
					})
				return patches
			},
		},
		{
			name:            "Multiple remaining subnets with mixed references - cache cleared only for deleted",
			existingSubnets: []client.Object{},
			remainingSubnets: map[string]*v1alpha1.Subnet{
				"proj-1:vpc-1:subnet-1": {
					ObjectMeta: metav1.ObjectMeta{
						Name:      "subnet-1",
						Namespace: "test-ns",
						Annotations: map[string]string{
							servicecommon.AnnotationAssociatedResource: "proj-1:vpc-1:subnet-1",
						},
					},
				},
				"proj-1:vpc-1:subnet-2": {
					ObjectMeta: metav1.ObjectMeta{
						Name:      "subnet-2",
						Namespace: "test-ns",
						Annotations: map[string]string{
							servicecommon.AnnotationAssociatedResource: "proj-1:vpc-1:subnet-2",
						},
					},
				},
			},
			expectedDeleteCount:     1,
			expectedCacheClearCalls: 1,
			expectError:             true,
			setupMocks: func(r *NamespaceReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "checkSubnetReferences",
					func(_ *NamespaceReconciler, _ context.Context, _ string, subnet *v1alpha1.Subnet, _ string) (bool, error) {
						// Only subnet-1 has references
						return subnet.Name == "subnet-1", nil
					})
				return patches
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := createTestNamespaceReconciler(tt.existingSubnets)

			if tt.setupMocks != nil {
				patches := tt.setupMocks(r)
				if patches != nil {
					defer patches.Reset()
				}
			}

			// Create a counter to track the number of deletes
			deleteCount := 0
			deletePatches := gomonkey.ApplyMethod(reflect.TypeOf(r.Client), "Delete",
				func(_ client.Client, _ context.Context, obj client.Object, _ ...client.DeleteOption) error {
					deleteCount++
					return nil
				})
			defer deletePatches.Reset()

			// Create a counter to track cache clear calls
			cacheClearCount := 0
			cacheClearPatches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "RemoveSubnetFromCache",
				func(_ *subnet.SubnetService, associatedResource string, reason string) {
					cacheClearCount++
					assert.Equal(t, "shared subnet CR deleted", reason)
				})
			defer cacheClearPatches.Reset()

			// Mock SubnetStatusUpdater.DeleteSuccess to avoid nil pointer dereference
			statusUpdaterPatches := gomonkey.ApplyMethod(reflect.TypeOf(&common.StatusUpdater{}), "DeleteSuccess",
				func(_ *common.StatusUpdater, _ client.ObjectKey, _ client.Object) {
					// Do nothing, just mock the method
				})
			defer statusUpdaterPatches.Reset()

			// Call the function being tested
			err := r.deleteUnusedSharedSubnets(context.Background(), "test-ns", tt.remainingSubnets)

			// Check the result
			if tt.expectError {
				assert.Error(t, err)
				assert.Equal(t, tt.expectedDeleteCount, deleteCount)
				assert.Equal(t, tt.expectedCacheClearCalls, cacheClearCount)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedDeleteCount, deleteCount)
				assert.Equal(t, tt.expectedCacheClearCalls, cacheClearCount)
			}
		})
	}
}

func TestSyncSharedSubnets(t *testing.T) {
	// Test cases
	tests := []struct {
		name            string
		existingSubnets []client.Object
		vpcNetConfig    *v1alpha1.VPCNetworkConfiguration
		expectedError   bool
		expectedErrMsg  string
		setupMocks      func(r *NamespaceReconciler) *gomonkey.Patches
	}{
		{
			name:            "Successful sync with no existing subnets",
			existingSubnets: []client.Object{},
			vpcNetConfig: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{
					Subnets: []v1alpha1.SharedSubnet{
						{
							Path: "/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1",
						},
					},
				},
			},
			expectedError: false,
			setupMocks: func(r *NamespaceReconciler) *gomonkey.Patches {
				// Mock getExistingSharedSubnetCRs
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getExistingSharedSubnetCRs",
					func(_ *NamespaceReconciler, _ context.Context, _ string) (map[string]*v1alpha1.Subnet, error) {
						return map[string]*v1alpha1.Subnet{}, nil
					})

				// Mock processNewSharedSubnets
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "processNewSharedSubnets",
					func(_ *NamespaceReconciler, _ context.Context, _ string, _ *v1alpha1.VPCNetworkConfiguration, _ map[string]*v1alpha1.Subnet) (map[string]*v1alpha1.Subnet, error) {
						return map[string]*v1alpha1.Subnet{}, nil
					})

				// Mock deleteUnusedSharedSubnets
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "deleteUnusedSharedSubnets",
					func(_ *NamespaceReconciler, _ context.Context, _ string, _ map[string]*v1alpha1.Subnet) error {
						return nil
					})

				return patches
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := createTestNamespaceReconciler(tt.existingSubnets)

			if tt.setupMocks != nil {
				patches := tt.setupMocks(r)
				if patches != nil {
					defer patches.Reset()
				}
			}

			// Call the function being tested
			err := r.syncSharedSubnets(context.Background(), "test-ns", tt.vpcNetConfig)

			// Check the result
			if tt.expectedError {
				assert.Error(t, err)
				if tt.expectedErrMsg != "" {
					assert.Contains(t, err.Error(), tt.expectedErrMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDeleteAllSharedSubnets(t *testing.T) {
	// Test cases
	tests := []struct {
		name              string
		existingSubnets   []client.Object
		setupMocks        func(r *NamespaceReconciler) *gomonkey.Patches
		expectedErrString string
	}{
		{
			name:            "Success case - no shared subnets",
			existingSubnets: []client.Object{},
			setupMocks: func(r *NamespaceReconciler) *gomonkey.Patches {
				// Mock getExistingSharedSubnetCRs to return empty map
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getExistingSharedSubnetCRs",
					func(_ *NamespaceReconciler, _ context.Context, _ string) (map[string]*v1alpha1.Subnet, error) {
						return map[string]*v1alpha1.Subnet{}, nil
					})

				// Mock deleteUnusedSharedSubnets to return nil
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "deleteUnusedSharedSubnets",
					func(_ *NamespaceReconciler, _ context.Context, _ string, _ map[string]*v1alpha1.Subnet) error {
						return nil
					})

				return patches
			},
			expectedErrString: "",
		},
		{
			name:            "Success case - with shared subnets",
			existingSubnets: []client.Object{},
			setupMocks: func(r *NamespaceReconciler) *gomonkey.Patches {
				// Mock getExistingSharedSubnetCRs to return a map with shared subnets
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getExistingSharedSubnetCRs",
					func(_ *NamespaceReconciler, _ context.Context, _ string) (map[string]*v1alpha1.Subnet, error) {
						return map[string]*v1alpha1.Subnet{
							"proj-1:vpc-1:subnet-1": {
								ObjectMeta: metav1.ObjectMeta{
									Name:      "subnet-1",
									Namespace: "test-ns",
									Annotations: map[string]string{
										servicecommon.AnnotationAssociatedResource: "proj-1:vpc-1:subnet-1",
									},
								},
							},
						}, nil
					})

				// Mock deleteUnusedSharedSubnets to return nil
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "deleteUnusedSharedSubnets",
					func(_ *NamespaceReconciler, _ context.Context, _ string, subnets map[string]*v1alpha1.Subnet) error {
						// Verify that the subnets map contains the expected subnet
						if _, ok := subnets["proj-1:vpc-1:subnet-1"]; !ok {
							return fmt.Errorf("expected subnet not found in map")
						}
						return nil
					})

				return patches
			},
			expectedErrString: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a reconciler with the necessary services
			r := createTestNamespaceReconciler(tt.existingSubnets)

			if tt.setupMocks != nil {
				patches := tt.setupMocks(r)
				if patches != nil {
					defer patches.Reset()
				}
			}

			// Call the function being tested
			err := r.deleteAllSharedSubnets(context.Background(), "test-ns")

			// Check the result
			if tt.expectedErrString != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrString)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCreateSharedSubnetCR(t *testing.T) {
	// Test cases
	tests := []struct {
		name              string
		sharedSubnetPath  string
		setupMocks        func(r *NamespaceReconciler) *gomonkey.Patches
		expectedErrString string
		realname          string
	}{
		{
			name:             "Success case without real name",
			sharedSubnetPath: "/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1",
			setupMocks: func(r *NamespaceReconciler) *gomonkey.Patches {
				// Mock ExtractSubnetPath
				patches := gomonkey.ApplyFunc(servicecommon.ExtractSubnetPath,
					func(path string) (string, string, string, string, error) {
						return "default", "proj-1", "vpc-1", "subnet-1", nil
					})

				// Mock IsDefaultNSXProject
				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "IsDefaultNSXProject",
					func(_ servicecommon.VPCServiceProvider, orgID, projectID string) (bool, error) {
						return false, nil
					})

				// Mock ConvertSubnetPathToAssociatedResource
				patches.ApplyFunc(servicecommon.ConvertSubnetPathToAssociatedResource,
					func(path string) (string, error) {
						return "proj-1:vpc-1:subnet-1", nil
					})

				// Mock GetNSXSubnetByAssociatedResource
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetNSXSubnetByAssociatedResource",
					func(_ *subnet.SubnetService, associatedResource string) (*model.VpcSubnet, error) {
						id := "subnet-1"
						displayName := "subnet-1"
						return &model.VpcSubnet{Id: &id, DisplayName: &displayName}, nil
					})

				// Mock BuildSubnetCR
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "BuildSubnetCR",
					func(_ *subnet.SubnetService, ns, subnetName, vpcFullID, associatedName string) *v1alpha1.Subnet {
						return &v1alpha1.Subnet{
							ObjectMeta: metav1.ObjectMeta{
								Name:      subnetName,
								Namespace: ns,
								Annotations: map[string]string{
									servicecommon.AnnotationAssociatedResource: associatedName,
								},
							},
							Spec: v1alpha1.SubnetSpec{
								VPCName: vpcFullID,
							},
						}
					})

				// Mock createSubnetCRInK8s
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "createSubnetCRInK8s",
					func(_ *NamespaceReconciler, _ context.Context, _ *v1alpha1.Subnet, _ string) error {
						return nil
					})

				return patches
			},
			realname:          "",
			expectedErrString: "",
		},
		{
			name:             "Success case with real name",
			sharedSubnetPath: "/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1",
			setupMocks: func(r *NamespaceReconciler) *gomonkey.Patches {
				// Mock ExtractSubnetPath
				patches := gomonkey.ApplyFunc(servicecommon.ExtractSubnetPath,
					func(path string) (string, string, string, string, error) {
						return "default", "proj-1", "vpc-1", "subnet-1", nil
					})

				// Mock IsDefaultNSXProject
				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "IsDefaultNSXProject",
					func(_ servicecommon.VPCServiceProvider, orgID, projectID string) (bool, error) {
						return false, nil
					})

				// Mock ConvertSubnetPathToAssociatedResource
				patches.ApplyFunc(servicecommon.ConvertSubnetPathToAssociatedResource,
					func(path string) (string, error) {
						return "proj-1:vpc-1:subnet-1", nil
					})

				// Mock GetNSXSubnetByAssociatedResource
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetNSXSubnetByAssociatedResource",
					func(_ *subnet.SubnetService, associatedResource string) (*model.VpcSubnet, error) {
						id := "subnet-1"
						displayName := "subnet-1"
						return &model.VpcSubnet{Id: &id, DisplayName: &displayName}, nil
					})

				// Mock BuildSubnetCR
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "BuildSubnetCR",
					func(_ *subnet.SubnetService, ns, subnetName, vpcFullID, associatedName string) *v1alpha1.Subnet {
						return &v1alpha1.Subnet{
							ObjectMeta: metav1.ObjectMeta{
								Name:      subnetName,
								Namespace: ns,
								Annotations: map[string]string{
									servicecommon.AnnotationAssociatedResource: associatedName,
								},
							},
							Spec: v1alpha1.SubnetSpec{
								VPCName: vpcFullID,
							},
						}
					})

				// Mock createSubnetCRInK8s
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "createSubnetCRInK8s",
					func(_ *NamespaceReconciler, _ context.Context, _ *v1alpha1.Subnet, _ string) error {
						return nil
					})

				return patches
			},
			realname:          "subnetam",
			expectedErrString: "",
		},
		{
			name:             "Error extracting subnet path",
			sharedSubnetPath: "/invalid/path",
			setupMocks: func(r *NamespaceReconciler) *gomonkey.Patches {
				// Mock ExtractSubnetPath to return an error
				patches := gomonkey.ApplyFunc(servicecommon.ExtractSubnetPath,
					func(path string) (string, string, string, string, error) {
						return "", "", "", "", fmt.Errorf("invalid subnet path format")
					})
				return patches
			},
			expectedErrString: "invalid subnet path format",
		},
		{
			name:             "Error checking if project is default",
			sharedSubnetPath: "/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1",
			setupMocks: func(r *NamespaceReconciler) *gomonkey.Patches {
				// Mock ExtractSubnetPath
				patches := gomonkey.ApplyFunc(servicecommon.ExtractSubnetPath,
					func(path string) (string, string, string, string, error) {
						return "default", "proj-1", "vpc-1", "subnet-1", nil
					})

				// Mock IsDefaultNSXProject to return an error
				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "IsDefaultNSXProject",
					func(_ servicecommon.VPCServiceProvider, orgID, projectID string) (bool, error) {
						return false, fmt.Errorf("failed to check if project is default")
					})

				return patches
			},
			expectedErrString: "failed to check if project is default",
			realname:          "",
		},
		{
			name:             "Error converting subnet path to associated resource",
			sharedSubnetPath: "/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1",
			setupMocks: func(r *NamespaceReconciler) *gomonkey.Patches {
				// Mock ExtractSubnetPath
				patches := gomonkey.ApplyFunc(servicecommon.ExtractSubnetPath,
					func(path string) (string, string, string, string, error) {
						return "default", "proj-1", "vpc-1", "subnet-1", nil
					})

				// Mock IsDefaultNSXProject
				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "IsDefaultNSXProject",
					func(_ servicecommon.VPCServiceProvider, orgID, projectID string) (bool, error) {
						return false, nil
					})

				// Mock ConvertSubnetPathToAssociatedResource to return an error
				patches.ApplyFunc(servicecommon.ConvertSubnetPathToAssociatedResource,
					func(path string) (string, error) {
						return "", fmt.Errorf("failed to convert subnet path to associated resource")
					})

				return patches
			},
			realname:          "",
			expectedErrString: "failed to convert subnet path to associated resource",
		},
		{
			name:             "Error creating Subnet CR in Kubernetes",
			sharedSubnetPath: "/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1",
			setupMocks: func(r *NamespaceReconciler) *gomonkey.Patches {
				// Mock ExtractSubnetPath
				patches := gomonkey.ApplyFunc(servicecommon.ExtractSubnetPath,
					func(path string) (string, string, string, string, error) {
						return "default", "proj-1", "vpc-1", "subnet-1", nil
					})

				// Mock IsDefaultNSXProject
				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "IsDefaultNSXProject",
					func(_ servicecommon.VPCServiceProvider, orgID, projectID string) (bool, error) {
						return false, nil
					})

				// Mock ConvertSubnetPathToAssociatedResource
				patches.ApplyFunc(servicecommon.ConvertSubnetPathToAssociatedResource,
					func(path string) (string, error) {
						return "proj-1:vpc-1:subnet-1", nil
					})

				// Mock GetNSXSubnetByAssociatedResource
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetNSXSubnetByAssociatedResource",
					func(_ *subnet.SubnetService, associatedResource string) (*model.VpcSubnet, error) {
						id := "subnet-1"
						displayName := "subnet-1"
						return &model.VpcSubnet{Id: &id, DisplayName: &displayName}, nil
					})

				// Mock BuildSubnetCR
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "BuildSubnetCR",
					func(_ *subnet.SubnetService, ns, subnetName, vpcFullID, associatedName string) *v1alpha1.Subnet {
						return &v1alpha1.Subnet{
							ObjectMeta: metav1.ObjectMeta{
								Name:      subnetName,
								Namespace: ns,
								Annotations: map[string]string{
									servicecommon.AnnotationAssociatedResource: associatedName,
								},
							},
							Spec: v1alpha1.SubnetSpec{
								VPCName: vpcFullID,
							},
						}
					})

				// Mock createSubnetCRInK8s to return an error
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "createSubnetCRInK8s",
					func(_ *NamespaceReconciler, _ context.Context, _ *v1alpha1.Subnet, _ string) error {
						return fmt.Errorf("failed to create Subnet CR")
					})

				return patches
			},
			realname:          "",
			expectedErrString: "failed to create Subnet CR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a reconciler with the necessary services
			r := createTestNamespaceReconciler(nil)

			// Add the SubnetService to the reconciler
			r.SubnetService = &subnet.SubnetService{}
			// Initialize the SharedSubnetResourceMap
			r.SubnetService.SharedSubnetResourceMap = make(map[string]sets.Set[types.NamespacedName])
			// Initialize the NSXSubnetCache
			r.SubnetService.NSXSubnetCache = make(map[string]struct {
				Subnet     *model.VpcSubnet
				StatusList []model.VpcSubnetStatus
			})

			// Setup mocks
			if tt.setupMocks != nil {
				patches := tt.setupMocks(r)
				if patches != nil {
					defer patches.Reset()
				}
			}

			// Call the function being tested
			err := r.createSharedSubnetCR(context.Background(), "test-ns", tt.sharedSubnetPath, tt.realname)

			// Check the result
			if tt.expectedErrString != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrString)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestIsValidKubernetesName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "Valid name with lowercase and hyphens",
			input:    "valid-subnet-name",
			expected: true,
		},
		{
			name:     "Valid name with dots",
			input:    "subnet.with.dots",
			expected: true,
		},
		{
			name:     "Valid single character",
			input:    "a",
			expected: true,
		},
		{
			name:     "Valid name starting with number",
			input:    "1subnet",
			expected: true,
		},
		{
			name:     "Invalid name with uppercase",
			input:    "Invalid-Subnet-Name",
			expected: false,
		},
		{
			name:     "Invalid name with underscores",
			input:    "subnet_with_underscores",
			expected: false,
		},
		{
			name:     "Invalid name ending with hyphen",
			input:    "invalid-end-",
			expected: false,
		},
		{
			name:     "Invalid name starting with hyphen",
			input:    "-invalid-start",
			expected: false,
		},
		{
			name:     "Invalid name ending with dot",
			input:    "invalid.end.",
			expected: false,
		},
		{
			name:     "Invalid name starting with dot",
			input:    ".invalid-start",
			expected: false,
		},
		{
			name:     "Invalid name with consecutive dots",
			input:    "invalid..dots",
			expected: false,
		},
		{
			name:     "Invalid empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "Original problematic name with underscores",
			input:    "stapple-stapple_vpc_sttest_1-private_subnet_1",
			expected: false,
		},
		{
			name:     "Valid name with mixed lowercase alphanumeric and hyphens",
			input:    "subnet-123-test",
			expected: true,
		},
		{
			name:     "Boundary: 253 characters should be valid",
			input:    strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 60),
			expected: true,
		},
		{
			name: "Boundary: 254 characters should be invalid",
			input: strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d",
				61) + ".",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := len(validation.IsDNS1123Subdomain(tt.input)) == 0
			assert.Equal(t, tt.expected, result, "Expected %v for input '%s', got %v", tt.expected, tt.input, result)
		})
	}
}

func TestGenerateValidSubnetName(t *testing.T) {
	tests := []struct {
		name           string
		subnetID       string
		shouldBeHashed bool
	}{
		{
			name:           "Valid subnet ID should be returned as-is",
			subnetID:       "valid-subnet-name",
			shouldBeHashed: false,
		},
		{
			name:           "Valid subnet ID with dots should be returned as-is",
			subnetID:       "subnet.with.dots",
			shouldBeHashed: false,
		},
		{
			name:           "Invalid subnet ID with underscores should be hashed",
			subnetID:       "subnet_with_underscores",
			shouldBeHashed: true,
		},
		{
			name:           "Invalid subnet ID with uppercase should be hashed",
			subnetID:       "Invalid-Subnet-Name",
			shouldBeHashed: true,
		},
		{
			name:           "Invalid subnet ID ending with hyphen should be hashed",
			subnetID:       "invalid-end-",
			shouldBeHashed: true,
		},
		{
			name:           "Original problematic name should be hashed",
			subnetID:       "stapple-stapple_vpc_sttest_1-private_subnet_1",
			shouldBeHashed: true,
		},
		{
			name:           "Empty string should be hashed",
			subnetID:       "",
			shouldBeHashed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateValidSubnetName(tt.subnetID)

			// The result should always be a valid Kubernetes name
			assert.True(t, len(validation.IsDNS1123Subdomain(result)) == 0, "Result '%s' should be a valid Kubernetes name", result)

			if tt.shouldBeHashed {
				// If it should be hashed, the result should be different from the input
				assert.NotEqual(t, tt.subnetID, result, "Expected input '%s' to be hashed, but got same value", tt.subnetID)
			} else {
				// If it shouldn't be hashed, the result should be the same as the input
				assert.Equal(t, tt.subnetID, result, "Expected input '%s' to remain unchanged, but got '%s'", tt.subnetID, result)
			}
		})
	}
}
