package subnetbinding

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	controllerscommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetbinding"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vlanpool"
)

type fakeRecorder struct{}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
}

func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}

func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}

type MockManager struct {
	ctrl.Manager
	client   client.Client
	scheme   *runtime.Scheme
	recorder record.EventRecorder
}

func (m *MockManager) GetClient() client.Client {
	return m.client
}

func (m *MockManager) GetScheme() *runtime.Scheme {
	return m.scheme
}

func (m *MockManager) GetEventRecorderFor(name string) record.EventRecorder {
	return m.recorder
}

func (m *MockManager) Add(runnable manager.Runnable) error {
	return nil
}

func (m *MockManager) Start(context.Context) error {
	return nil
}

func newMockManager(objs ...client.Object) ctrl.Manager {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(objs...).WithStatusSubresource(&v1alpha1.SubnetConnectionBindingMap{}).Build()
	return &MockManager{
		client:   fakeClient,
		scheme:   newScheme,
		recorder: &fakeRecorder{},
	}
}

func TestReconcile(t *testing.T) {
	crName := "binding1"
	crNS := "default"
	request := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      crName,
			Namespace: crNS,
		},
	}
	validBM1 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "binding-uuid",
			Namespace: crNS,
			Name:      crName,
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:          "child",
			TargetSubnetSetName: "parentSubnetSet",
			VLANTrafficTag:      v1alpha1.VLANTrafficTagPtr(101),
		},
	}
	bmWithSubnetAssociation := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "binding-uuid-2",
			Namespace: crNS,
			Name:      crName,
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:        "child",
			TargetSubnetName:  "parent",
			SubnetAssociation: v1alpha1.SubnetAssociationBranch,
			VLANTrafficTag:    v1alpha1.VLANTrafficTagPtr(101),
		},
	}
	for _, tc := range []struct {
		name      string
		objects   []client.Object
		expectRes ctrl.Result
		patches   func(t *testing.T, r *Reconciler) *gomonkey.Patches
		verify    func(t *testing.T, r *Reconciler)
	}{
		{
			name: "Failed to reconcile due to an error getting the SubnetConnectionBindingMap CR",
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Client), "Get", func(_ client.Client, ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return fmt.Errorf("unable to get CR")
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "DeleteSubnetConnectionBindingMapsByCRName", func(_ *subnetbinding.BindingService, bindingName string, bindingNamespace string) error {
					require.Fail(t, "SubnetBindingService.DeleteSubnetConnectionBindingMapsByCRName should not called when failed to get SubnetConnectionBindingMap CR")
					return nil
				})
				return patches
			},
			expectRes: controllerscommon.ResultRequeue,
		},
		{
			name: "Failed to reconcile due to SubnetConnectionBindingMap CR doesn't exist",
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Client), "Get", func(_ client.Client, ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return apierrors.NewNotFound(v1alpha1.Resource("subnetconnectionbindingmap"), crName)
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "DeleteSubnetConnectionBindingMapsByCRName", func(_ *subnetbinding.BindingService, bindingName string, bindingNamespace string) error {
					return fmt.Errorf("NSX deletion failure")
				})
				return patches
			},
			expectRes: controllerscommon.ResultRequeue,
		}, {
			name: "Succeeded to delete SubnetConnectionBindingMaps if CR doesn't exist",
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Client), "Get", func(_ client.Client, ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return apierrors.NewNotFound(v1alpha1.Resource("subnetconnectionbindingmap"), crName)
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "DeleteSubnetConnectionBindingMapsByCRName", func(_ *subnetbinding.BindingService, bindingName string, bindingNamespace string) error {
					return nil
				})
				return patches
			},
			expectRes: controllerscommon.ResultNormal,
		}, {
			name:    "Failed to create/update SubnetConnectionBindingMap by nested dependencies",
			objects: []client.Object{validBM1},
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateDependency", func(_ *Reconciler, ctx context.Context, bindingMap *v1alpha1.SubnetConnectionBindingMap) (string, []string, *errorWithRetry) {
					return "", nil, &errorWithRetry{
						message: "Subnet is already used as target",
						error:   fmt.Errorf("subnet is already used as target"),
						retry:   true,
					}
				})
				return patches
			},
			expectRes: controllerscommon.ResultRequeueAfter60sec,
		}, {
			name:    "Failed to create/update SubnetConnectionBindingMap due to the dependency validation error",
			objects: []client.Object{validBM1},
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateDependency", func(_ *Reconciler, ctx context.Context, bindingMap *v1alpha1.SubnetConnectionBindingMap) (string, []string, *errorWithRetry) {
					return "", nil, &errorWithRetry{
						message: "Unable to get Subnet CR net1",
						error:   fmt.Errorf("cr not ready"),
						retry:   true,
					}
				})
				return patches
			},
			expectRes: controllerscommon.ResultRequeueAfter60sec,
		}, {
			name:    "Failed to create/update SubnetConnectionBindingMap on NSX",
			objects: []client.Object{validBM1},
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateDependency", func(_ *Reconciler, ctx context.Context, bindingMap *v1alpha1.SubnetConnectionBindingMap) (string, []string, *errorWithRetry) {
					return "/subnet-child", []string{"/subnet-parent"}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "CreateOrUpdateSubnetConnectionBindingMap",
					func(_ *subnetbinding.BindingService, subnetBinding *v1alpha1.SubnetConnectionBindingMap, preferredVlan int64, childSubnetPath string, parentSubnetPaths []string) (int64, error) {
						return 0, fmt.Errorf("failed to configure NSX")
					})
				return patches
			},
			expectRes: controllerscommon.ResultRequeue,
			verify: func(t *testing.T, r *Reconciler) {
				got := &v1alpha1.SubnetConnectionBindingMap{}
				require.NoError(t, r.Client.Get(context.Background(), request.NamespacedName, got))
				require.Len(t, got.Status.Conditions, 1)
				assert.Equal(t, string(v1alpha1.Ready), string(got.Status.Conditions[0].Type))
				assert.Equal(t, string(corev1.ConditionFalse), string(got.Status.Conditions[0].Status))
				assert.Equal(t, "ConfigureFailed", got.Status.Conditions[0].Reason)
			},
		}, {
			name:    "Failed to create/update SubnetConnectionBindingMap due to VLAN allocation error",
			objects: []client.Object{validBM1},
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateDependency", func(_ *Reconciler, ctx context.Context, bindingMap *v1alpha1.SubnetConnectionBindingMap) (string, []string, *errorWithRetry) {
					return "/subnet-child", []string{"/subnet-parent"}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "CreateOrUpdateSubnetConnectionBindingMap",
					func(_ *subnetbinding.BindingService, subnetBinding *v1alpha1.SubnetConnectionBindingMap, preferredVlan int64, childSubnetPath string, parentSubnetPaths []string) (int64, error) {
						return 0, &vlanpool.VlanAllocationError{Err: fmt.Errorf("no available VLAN in pool")}
					})
				return patches
			},
			expectRes: controllerscommon.ResultRequeue,
			verify: func(t *testing.T, r *Reconciler) {
				got := &v1alpha1.SubnetConnectionBindingMap{}
				require.NoError(t, r.Client.Get(context.Background(), request.NamespacedName, got))
				require.Len(t, got.Status.Conditions, 1)
				assert.Equal(t, string(v1alpha1.Ready), string(got.Status.Conditions[0].Type))
				assert.Equal(t, string(corev1.ConditionFalse), string(got.Status.Conditions[0].Status))
				assert.Equal(t, "VlanAllocationFailed", got.Status.Conditions[0].Reason)
				assert.Contains(t, got.Status.Conditions[0].Message, "no available VLAN in pool")
			},
		}, {
			name:    "Failed to reconcile when SubnetAssociation is specified on unsupported NSX version",
			objects: []client.Object{bmWithSubnetAssociation},
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetBindingService.NSXClient), "NSXCheckVersion",
					func(_ *nsx.Client, feature int) bool {
						return feature != nsx.SubnetAssociation
					})
				return patches
			},
			expectRes: controllerscommon.ResultNormal,
		}, {
			name:    "Succeeded to create/update SubnetConnectionBindingMap",
			objects: []client.Object{validBM1},
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateDependency", func(_ *Reconciler, ctx context.Context, bindingMap *v1alpha1.SubnetConnectionBindingMap) (string, []string, *errorWithRetry) {
					return "/subnet-child", []string{"/subnet-parent"}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "CreateOrUpdateSubnetConnectionBindingMap",
					func(_ *subnetbinding.BindingService, subnetBinding *v1alpha1.SubnetConnectionBindingMap, preferredVlan int64, childSubnetPath string, parentSubnetPaths []string) (int64, error) {
						return 201, nil
					})
				return patches
			},
			expectRes: controllerscommon.ResultNormal,
		}, {
			name: "Auto-allocate VLAN and set status.vlanTrafficTag",
			objects: []client.Object{&v1alpha1.SubnetConnectionBindingMap{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "binding-uuid-auto",
					Namespace: crNS,
					Name:      crName,
				},
				Spec: v1alpha1.SubnetConnectionBindingMapSpec{
					SubnetName:          "child",
					TargetSubnetSetName: "parentSubnetSet",
				},
			}},
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateDependency", func(_ *Reconciler, ctx context.Context, bindingMap *v1alpha1.SubnetConnectionBindingMap) (string, []string, *errorWithRetry) {
					return "/subnet-child", []string{"/subnet-parent"}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "CreateOrUpdateSubnetConnectionBindingMap",
					func(_ *subnetbinding.BindingService, subnetBinding *v1alpha1.SubnetConnectionBindingMap, preferredVlan int64, childSubnetPath string, parentSubnetPaths []string) (int64, error) {
						return 301, nil
					})
				return patches
			},
			expectRes: controllerscommon.ResultNormal,
			verify: func(t *testing.T, r *Reconciler) {
				got := &v1alpha1.SubnetConnectionBindingMap{}
				require.NoError(t, r.Client.Get(context.Background(), request.NamespacedName, got))
				require.NotNil(t, got.Status.VLANTrafficTag)
				assert.Equal(t, int64(301), *got.Status.VLANTrafficTag)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			r := createFakeReconciler(tc.objects...)
			patches := tc.patches(t, r)
			defer patches.Reset()

			rst, _ := r.Reconcile(ctx, request)
			assert.Equal(t, tc.expectRes, rst)
			if tc.verify != nil {
				tc.verify(t, r)
			}
		})
	}
}

func TestCollectGarbage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		patches func(t *testing.T, r *Reconciler) *gomonkey.Patches
	}{
		{
			name: "Failed to list from CRs",
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "listBindingMapIDsFromCRs", func(_ *Reconciler, ctx context.Context) (sets.Set[string], error) {
					return sets.New[string](), fmt.Errorf("unable to list CRs")
				})
				return patches
			},
		}, {
			name: "Failed to delete on NSX",
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "listBindingMapIDsFromCRs", func(_ *Reconciler, ctx context.Context) (sets.Set[string], error) {
					return sets.New[string](), nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "ListSubnetConnectionBindingMapCRUIDsInStore", func(s *subnetbinding.BindingService) sets.Set[string] {
					return sets.New[string]()
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "DeleteMultiSubnetConnectionBindingMapsByCRs", func(s *subnetbinding.BindingService, bindingCRs sets.Set[string]) error {
					return fmt.Errorf("deletion failed")
				})
				return patches
			},
		}, {
			name: "Success",
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "listBindingMapIDsFromCRs", func(_ *Reconciler, ctx context.Context) (sets.Set[string], error) {
					return sets.New[string](), nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "ListSubnetConnectionBindingMapCRUIDsInStore", func(s *subnetbinding.BindingService) sets.Set[string] {
					return sets.New[string]()
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "DeleteMultiSubnetConnectionBindingMapsByCRs", func(s *subnetbinding.BindingService, bindingCRs sets.Set[string]) error {
					return nil
				})
				return patches
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			r := createFakeReconciler()
			patches := tc.patches(t, r)
			defer patches.Reset()

			r.CollectGarbage(ctx)
		})
	}
}

func TestValidateDependency(t *testing.T) {
	name := "binding1"
	namespace := "default"
	childSubnet := "subnet"
	targetSubnet := "targetSubnet"
	targetSubnetSet := "targetSubnetSet"

	childSubnetCR := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      childSubnet,
			Namespace: namespace,
			UID:       types.UID("child-uuid"),
		},
	}
	targetSubnetCR := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      targetSubnet,
			Namespace: namespace,
			UID:       types.UID("target-uuid"),
		},
	}
	targetSubnetSetCR := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      targetSubnetSet,
			Namespace: namespace,
			UID:       types.UID("target-set-uuid"),
		},
	}

	bindingCR1 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       childSubnet,
			TargetSubnetName: targetSubnet,
			VLANTrafficTag:   v1alpha1.VLANTrafficTagPtr(101),
		},
	}
	bindingCR2 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:          childSubnet,
			TargetSubnetSetName: targetSubnetSet,
			VLANTrafficTag:      v1alpha1.VLANTrafficTagPtr(101),
		},
	}

	for _, tc := range []struct {
		name             string
		objects          []client.Object
		patches          func(t *testing.T, r *Reconciler) *gomonkey.Patches
		bindingMap       *v1alpha1.SubnetConnectionBindingMap
		expErr           string
		expMsg           string
		expSubnet        string
		expTargetSubnets []string
	}{
		{
			name:       "child subnet is not ready",
			bindingMap: bindingCR1,
			objects:    []client.Object{targetSubnetCR},
			expErr:     "failed to get Subnet subnet in Namespace default with error: subnets.crd.nsx.vmware.com \"subnet\" not found",
			expMsg:     "Unable to get Subnet CR subnet",
		}, {
			name:       "parent subnet is not ready",
			bindingMap: bindingCR1,
			objects:    []client.Object{childSubnetCR},
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key, value string) []*model.VpcSubnet {
					return []*model.VpcSubnet{{Id: common.String("s1"), Path: common.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-child")}}
				})
				return patches
			},
			expErr: "failed to get Subnet targetSubnet in Namespace default with error: subnets.crd.nsx.vmware.com \"targetSubnet\" not found",
			expMsg: "Unable to get Subnet CR targetSubnet",
		}, {
			name:       "parent subnet is ready",
			bindingMap: bindingCR1,
			objects:    []client.Object{childSubnetCR, targetSubnetCR},
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key, value string) []*model.VpcSubnet {
					if value == "child-uuid" {
						return []*model.VpcSubnet{{Id: common.String("s1"), Path: common.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-child")}}
					}
					return []*model.VpcSubnet{{Id: common.String("s2"), Path: common.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-child")}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "GetSubnetConnectionBindingMapsByParentSubnet", func(_ *subnetbinding.BindingService, subnetPath string) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{}
				})
				return patches
			},
			expSubnet:        "/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-child",
			expTargetSubnets: []string{"/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-child"},
		}, {
			name:       "parent subnetSet is not ready",
			bindingMap: bindingCR2,
			objects:    []client.Object{childSubnetCR},
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key, value string) []*model.VpcSubnet {
					return []*model.VpcSubnet{{Id: common.String("s1"), Path: common.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-child")}}
				})
				return patches
			},
			expErr: "failed to get SubnetSet targetSubnetSet in Namespace default with error: subnetsets.crd.nsx.vmware.com \"targetSubnetSet\" not found",
			expMsg: "Unable to get SubnetSet CR targetSubnetSet",
		}, {
			name:       "parent subnetSet is ready",
			bindingMap: bindingCR2,
			objects:    []client.Object{childSubnetCR, targetSubnetSetCR},
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key, value string) []*model.VpcSubnet {
					if value == "child-uuid" {
						return []*model.VpcSubnet{{Id: common.String("s1"), Path: common.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-child")}}
					}
					return []*model.VpcSubnet{{Id: common.String("s2"), Path: common.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-parent")}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "GetSubnetConnectionBindingMapsByParentSubnet", func(_ *subnetbinding.BindingService, subnetPath string) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{}
				})
				return patches
			},
			expSubnet:        "/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-child",
			expTargetSubnets: []string{"/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-parent"},
		}, {
			name:       "parent subnet and child subnet in different vpcName",
			bindingMap: bindingCR1,
			objects:    []client.Object{childSubnetCR, targetSubnetCR},
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key, value string) []*model.VpcSubnet {
					if value == "child-uuid" {
						return []*model.VpcSubnet{{Id: common.String("s1"), Path: common.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-child")}}
					}
					return []*model.VpcSubnet{{Id: common.String("s2"), Path: common.String("/orgs/default/projects/default/vpcs/ns-2/subnets/subnet-parent")}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "GetSubnetConnectionBindingMapsByParentSubnet", func(_ *subnetbinding.BindingService, subnetPath string) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{}
				})
				return patches
			},
			expSubnet:        "/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-child",
			expTargetSubnets: []string{"/orgs/default/projects/default/vpcs/ns-2/subnets/subnet-parent"},
		}, {
			name:       "parent subnetSet and child subnet in different vpcName",
			bindingMap: bindingCR2,
			objects:    []client.Object{childSubnetCR, targetSubnetSetCR},
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key, value string) []*model.VpcSubnet {
					if value == "child-uuid" {
						return []*model.VpcSubnet{{Id: common.String("s1"), Path: common.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-child")}}
					}
					return []*model.VpcSubnet{{Id: common.String("s2"), Path: common.String("/orgs/default/projects/default/vpcs/ns-2/subnets/subnet-parent")}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "GetSubnetConnectionBindingMapsByParentSubnet", func(_ *subnetbinding.BindingService, subnetPath string) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{}
				})
				return patches
			},
			expSubnet:        "/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-child",
			expTargetSubnets: []string{"/orgs/default/projects/default/vpcs/ns-2/subnets/subnet-parent"},
		}, {
			name:       "parent Subnet is pre-created Subnet",
			bindingMap: bindingCR1,
			objects:    []client.Object{childSubnetCR, targetSubnetCR},
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key, value string) []*model.VpcSubnet {
					if value == "child-uuid" {
						return []*model.VpcSubnet{{Id: common.String("s1"), Path: common.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-child")}}
					}
					return []*model.VpcSubnet{{Id: common.String("s2"), Path: common.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-parent")}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "GetSubnetConnectionBindingMapsByParentSubnet", func(_ *subnetbinding.BindingService, subnetPath string) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{}
				})
				return patches
			},
			expSubnet:        "/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-child",
			expTargetSubnets: []string{"/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-parent"},
		}, {
			name: "cross-VPC Branch binding with shared target subnet",
			bindingMap: &v1alpha1.SubnetConnectionBindingMap{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns-vpc-a"},
				Spec: v1alpha1.SubnetConnectionBindingMapSpec{
					SubnetName:        "parent-subnet",
					TargetSubnetName:  "child-subnet",
					SubnetAssociation: v1alpha1.SubnetAssociationBranch,
					VLANTrafficTag:    v1alpha1.VLANTrafficTagPtr(201),
				},
			},
			objects: []client.Object{
				&v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "parent-subnet", Namespace: "ns-vpc-a", UID: "p-uuid"}},
				&v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "child-subnet", Namespace: "ns-vpc-a", UID: "c-uuid"}},
			},
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key, value string) []*model.VpcSubnet {
					if value == "p-uuid" {
						return []*model.VpcSubnet{{Id: common.String("s1"), Path: common.String("/orgs/default/projects/default/vpcs/vpc-a/subnets/parent-subnet")}}
					}
					return []*model.VpcSubnet{{Id: common.String("s2"), Path: common.String("/orgs/default/projects/default/vpcs/vpc-b/subnets/child-subnet")}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "GetSubnetConnectionBindingMapsByChildSubnet", func(_ *subnetbinding.BindingService, subnetPath string) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{}
				})
				return patches
			},
			expSubnet:        "/orgs/default/projects/default/vpcs/vpc-a/subnets/parent-subnet",
			expTargetSubnets: []string{"/orgs/default/projects/default/vpcs/vpc-b/subnets/child-subnet"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			r := createFakeReconciler(tc.objects...)
			if tc.patches != nil {
				patches := tc.patches(t, r)
				defer patches.Reset()
			}

			subnet, targetSubnets, err := r.validateDependency(ctx, tc.bindingMap)
			if tc.expErr != "" {
				require.EqualError(t, err.error, tc.expErr)
				require.Equal(t, tc.expMsg, err.message)
			} else {
				require.Nil(t, err)
			}
			require.Equal(t, tc.expSubnet, subnet)
			require.ElementsMatch(t, tc.expTargetSubnets, targetSubnets)
		})
	}
}

func TestValidateVpcSubnetsBySubnetCR(t *testing.T) {
	subnetName := "net1"
	subnetNamespace := "default"
	subnetCR := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      subnetName,
			Namespace: subnetNamespace,
			UID:       "subnet-uuid",
		},
	}
	sharedSubnetCR := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        subnetName,
			Namespace:   subnetNamespace,
			UID:         "subnet-uuid",
			Annotations: map[string]string{common.AnnotationAssociatedResource: ":ns-1:subnet-1"},
		},
		Status: v1alpha1.SubnetStatus{
			Shared: true,
			Conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.Ready,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	for _, tc := range []struct {
		name     string
		isParent bool
		objects  []client.Object
		patches  func(t *testing.T, r *Reconciler) *gomonkey.Patches
		expErr   string
		expMsg   string
		expRetry bool
		paths    []string
	}{
		{
			name:     "Failed to get Subnet CR",
			isParent: true,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Client), "Get", func(_ client.Client, ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return fmt.Errorf("unable to get CR")
				})
				return patches
			},
			expRetry: false,
			expMsg:   "Unable to get Subnet CR net1",
			expErr:   "failed to get Subnet net1 in Namespace default with error: unable to get CR",
		}, {
			name:     "Subnet CR is not realized",
			isParent: true,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key, value string) []*model.VpcSubnet {
					return []*model.VpcSubnet{}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService.BindingStore), "GetBindingsByChildSubnet", func(_ *subnetbinding.BindingStore, subnetPath string) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{}
				})
				return patches
			},
			objects:  []client.Object{subnetCR},
			expRetry: false,
			expMsg:   "Subnet CR net1 is not realized on NSX",
			expErr:   "not found NSX VpcSubnets created by Subnet CR 'default/net1'",
		}, {
			name:     "Child subnet CR is already used as branch with DisplayName",
			isParent: true,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key, value string) []*model.VpcSubnet {
					return []*model.VpcSubnet{{Id: common.String("net1"), Path: common.String("/subnet-1")}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService.BindingStore), "GetByIndex", func(_ *subnetbinding.BindingStore, key, value string) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{{
						DisplayName: common.String("binding1"),
						Id:          common.String("binding-id-1"),
					}}
				})
				return patches
			},
			objects:  []client.Object{subnetCR},
			expRetry: true,
			expMsg:   "Subnet CR net1 is already used as a branch by binding1",
			expErr:   "the Subnet net1 already works as a branch in SubnetConnectionBindingMap binding1",
		}, {
			name:     "Child subnet CR is already used as branch with nil DisplayName",
			isParent: true,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key, value string) []*model.VpcSubnet {
					return []*model.VpcSubnet{{Id: common.String("net1"), Path: common.String("/subnet-1")}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService.BindingStore), "GetByIndex", func(_ *subnetbinding.BindingStore, key, value string) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{{
						DisplayName: nil,
						Id:          common.String("binding-id-1"),
					}}
				})
				return patches
			},
			objects:  []client.Object{subnetCR},
			expRetry: true,
			expMsg:   "Subnet CR net1 is already used as a branch by binding-id-1",
			expErr:   "the Subnet net1 already works as a branch in SubnetConnectionBindingMap binding-id-1",
		}, {
			name:     "Child subnet CR is not used as branch",
			isParent: true,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key, value string) []*model.VpcSubnet {
					return []*model.VpcSubnet{{Id: common.String("net1"), Path: common.String("/subnet-1")}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService.BindingStore), "GetByIndex", func(_ *subnetbinding.BindingStore, key, value string) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{}
				})
				return patches
			},
			objects: []client.Object{subnetCR},
			paths:   []string{"/subnet-1"},
		}, {
			name:     "Target subnet CR is already used as trunk with DisplayName",
			isParent: false,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key, value string) []*model.VpcSubnet {
					return []*model.VpcSubnet{{Id: common.String("net1"), Path: common.String("/subnet-1")}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService.BindingStore), "GetByIndex", func(_ *subnetbinding.BindingStore, key, value string) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{{
						DisplayName: common.String("binding1"),
						Id:          common.String("binding-id-1"),
					}}
				})
				return patches
			},
			objects:  []client.Object{subnetCR},
			expRetry: true,
			expMsg:   "Subnet CR net1 is already used as a trunk by binding1",
			expErr:   "the Subnet net1 already works as a trunk in SubnetConnectionBindingMap binding1",
		}, {
			name:     "Target subnet CR is already used as trunk with nil DisplayName",
			isParent: false,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key, value string) []*model.VpcSubnet {
					return []*model.VpcSubnet{{Id: common.String("net1"), Path: common.String("/subnet-1")}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService.BindingStore), "GetByIndex", func(_ *subnetbinding.BindingStore, key, value string) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{{
						DisplayName: nil,
						Path:        common.String("/path/to/binding1"),
					}}
				})
				return patches
			},
			objects:  []client.Object{subnetCR},
			expRetry: true,
			expMsg:   "Subnet CR net1 is already used as a trunk by /path/to/binding1",
			expErr:   "the Subnet net1 already works as a trunk in SubnetConnectionBindingMap /path/to/binding1",
		}, {
			name:     "Target subnet CR is not used as trunk",
			isParent: false,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key, value string) []*model.VpcSubnet {
					return []*model.VpcSubnet{{Id: common.String("net1"), Path: common.String("/subnet-1")}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService.BindingStore), "GetByIndex", func(_ *subnetbinding.BindingStore, key, value string) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{}
				})
				return patches
			},
			objects: []client.Object{subnetCR},
			paths:   []string{"/subnet-1"},
		}, {
			name:     "Child subnet is shared Subnet",
			isParent: false,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(common.GetSubnetPathFromAssociatedResource, func(associatedResource string) (string, error) {
					return "/subnet-1", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService.BindingStore), "GetByIndex", func(_ *subnetbinding.BindingStore, key, value string) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{}
				})
				return patches
			},
			objects: []client.Object{sharedSubnetCR},
			paths:   []string{"/subnet-1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			r := createFakeReconciler(tc.objects...)
			patches := tc.patches(t, r)
			defer patches.Reset()

			paths, err := r.validateVpcSubnetsBySubnetCR(ctx, subnetNamespace, subnetName, tc.isParent)
			if tc.expErr != "" {
				require.NotNil(t, err)
				require.EqualError(t, err.error, tc.expErr)
				require.Equal(t, tc.expMsg, err.message)
				require.Equal(t, tc.expRetry, err.retry)
			} else {
				require.Nil(t, err)
			}
			require.ElementsMatch(t, tc.paths, paths)
		})
	}
}

func TestValidateVpcSubnetsBySubnetSetCR(t *testing.T) {
	name := "net1"
	namespace := "default"
	subnetSetCR := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       "subnetset-uuid-1",
		},
	}
	sharedSubnetCR := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "subnet-1",
			Namespace: namespace,
			UID:       "subnet-1-uuid",
		},
	}
	sharedSubnetSetCR := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       "subnetset-uuid-1",
		},
		Spec: v1alpha1.SubnetSetSpec{
			SubnetNames: &[]string{"subnet-1"},
		},
	}
	for _, tc := range []struct {
		name    string
		objects []client.Object
		patches func(t *testing.T, r *Reconciler) *gomonkey.Patches
		expErr  string
		expMsg  string
		paths   []string
	}{
		{
			name: "Failed to get SubnetSet CR",
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Client), "Get", func(_ client.Client, ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return fmt.Errorf("unable to get CR")
				})
				return patches
			},
			expMsg: "Unable to get SubnetSet CR net1",
			expErr: "failed to get SubnetSet net1 in Namespace default with error: unable to get CR",
		}, {
			name: "SubnetSet CR is not realized",
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key, value string) []*model.VpcSubnet {
					return []*model.VpcSubnet{}
				})
				return patches
			},
			objects: []client.Object{subnetSetCR},
			expMsg:  "SubnetSet CR net1 is not realized on NSX",
			expErr:  "no existing NSX VpcSubnet created by SubnetSet CR 'default/net1'",
		}, {
			name: "SubnetSet CR is realized",
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key, value string) []*model.VpcSubnet {
					return []*model.VpcSubnet{{Id: common.String("net1"), Path: common.String("/subnet-1")}}
				})
				return patches
			},
			objects: []client.Object{subnetSetCR},
			expMsg:  "",
			expErr:  "",
			paths:   []string{"/subnet-1"},
		}, {
			name:    "SubnetSet CR with shared Subnet",
			objects: []client.Object{sharedSubnetSetCR, sharedSubnetCR},
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key, value string) []*model.VpcSubnet {
					return []*model.VpcSubnet{{Id: common.String("subnet-1"), Path: common.String("/subnet-1")}}
				})
				return patches
			},
			expMsg: "",
			expErr: "",
			paths:  []string{"/subnet-1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			r := createFakeReconciler(tc.objects...)
			if tc.patches != nil {
				patches := tc.patches(t, r)
				defer patches.Reset()
			}

			paths, err := r.validateVpcSubnetsBySubnetSetCR(ctx, namespace, name)
			if tc.expErr != "" {
				require.NotNil(t, err)
				require.EqualError(t, err.error, tc.expErr)
				require.Equal(t, tc.expMsg, err.message)
				require.False(t, err.retry)
			} else {
				require.Nil(t, err)
			}
			require.ElementsMatch(t, tc.paths, paths)
		})
	}
}

func TestUpdateBindingMapStatusWithConditions(t *testing.T) {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))

	name := "binding1"
	namespace := "default"
	bindingMap1 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:          "child",
			TargetSubnetSetName: "parent",
			VLANTrafficTag:      v1alpha1.VLANTrafficTagPtr(101),
		},
	}
	bindingMap2 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:          "child",
			TargetSubnetSetName: "parent",
			VLANTrafficTag:      v1alpha1.VLANTrafficTagPtr(101),
		},
		Status: v1alpha1.SubnetConnectionBindingMapStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.Ready,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	bindingMap3 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:          "child",
			TargetSubnetSetName: "parent",
			VLANTrafficTag:      v1alpha1.VLANTrafficTagPtr(101),
		},
		Status: v1alpha1.SubnetConnectionBindingMapStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:    v1alpha1.Ready,
					Status:  corev1.ConditionFalse,
					Message: "old message",
					Reason:  "crNotFound",
				},
			},
		},
	}
	msg := "Subnet CR net1 is not realized on NSX"
	bindingMap4 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:          "child",
			TargetSubnetSetName: "parent",
			VLANTrafficTag:      v1alpha1.VLANTrafficTagPtr(101),
		},
		Status: v1alpha1.SubnetConnectionBindingMapStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:    v1alpha1.Ready,
					Status:  corev1.ConditionFalse,
					Message: msg,
					Reason:  "DependencyNotReady",
				},
			},
		},
	}

	for _, tc := range []struct {
		name       string
		existingBM *v1alpha1.SubnetConnectionBindingMap
	}{
		{
			name:       "Add new condition",
			existingBM: bindingMap1,
		}, {
			name:       "Update ready condition to unready",
			existingBM: bindingMap2,
		}, {
			name:       "Update unready condition message and reason",
			existingBM: bindingMap3,
		}, {
			name:       "Not update unready condition if message and ready equals",
			existingBM: bindingMap4,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(tc.existingBM).WithStatusSubresource(tc.existingBM).Build()
			updateBindingMapStatusWithUnreadyCondition(fakeClient, ctx, tc.existingBM, metav1.Now(), nil, "DependencyNotReady", msg)

			updatedBM := &v1alpha1.SubnetConnectionBindingMap{}
			err := fakeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, updatedBM)
			require.NoError(t, err)
			require.Equal(t, 1, len(updatedBM.Status.Conditions))
			cond := updatedBM.Status.Conditions[0]
			assert.Equal(t, "DependencyNotReady", cond.Reason)
			assert.Equal(t, msg, cond.Message)
			assert.Equal(t, v1alpha1.Ready, cond.Type)
			assert.Equal(t, corev1.ConditionFalse, cond.Status)

			fakeClient2 := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(tc.existingBM).WithStatusSubresource(tc.existingBM).Build()
			updateBindingMapStatusWithReadyCondition(fakeClient2, ctx, tc.existingBM, metav1.Now())

			updatedBM2 := &v1alpha1.SubnetConnectionBindingMap{}
			err = fakeClient2.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, updatedBM2)
			require.NoError(t, err)
			require.Equal(t, 1, len(updatedBM2.Status.Conditions))
			cond = updatedBM2.Status.Conditions[0]
			assert.Equal(t, v1alpha1.Ready, cond.Type)
			assert.Equal(t, corev1.ConditionTrue, cond.Status)
		})
	}
}

func TestUpdateBindingMapConditionWithRetry(t *testing.T) {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))

	name := "binding1"
	namespace := "default"

	for _, tc := range []struct {
		name       string
		existingBM *v1alpha1.SubnetConnectionBindingMap
		condition  v1alpha1.Condition
		expStatus  corev1.ConditionStatus
		expReason  string
		expMessage string
	}{
		{
			name: "Successful update on first attempt",
			existingBM: &v1alpha1.SubnetConnectionBindingMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
				},
				Spec: v1alpha1.SubnetConnectionBindingMapSpec{
					SubnetName:          "child",
					TargetSubnetSetName: "parent",
					VLANTrafficTag:      v1alpha1.VLANTrafficTagPtr(101),
				},
			},
			condition: v1alpha1.Condition{
				Type:    v1alpha1.Ready,
				Status:  corev1.ConditionTrue,
				Reason:  "SubnetConnectionBindingMapReady",
				Message: "NSX resource has been successfully created/updated",
			},
			expStatus:  corev1.ConditionTrue,
			expReason:  "SubnetConnectionBindingMapReady",
			expMessage: "NSX resource has been successfully created/updated",
		},
		{
			name: "No update when condition already matches",
			existingBM: &v1alpha1.SubnetConnectionBindingMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
				},
				Spec: v1alpha1.SubnetConnectionBindingMapSpec{
					SubnetName:          "child",
					TargetSubnetSetName: "parent",
					VLANTrafficTag:      v1alpha1.VLANTrafficTagPtr(101),
				},
				Status: v1alpha1.SubnetConnectionBindingMapStatus{
					Conditions: []v1alpha1.Condition{
						{
							Type:    v1alpha1.Ready,
							Status:  corev1.ConditionTrue,
							Reason:  "SubnetConnectionBindingMapReady",
							Message: "Already ready",
						},
					},
				},
			},
			condition: v1alpha1.Condition{
				Type:    v1alpha1.Ready,
				Status:  corev1.ConditionTrue,
				Reason:  "SubnetConnectionBindingMapReady",
				Message: "Already ready",
			},
			expStatus:  corev1.ConditionTrue,
			expReason:  "SubnetConnectionBindingMapReady",
			expMessage: "Already ready",
		},
		{
			name: "Update existing condition with different status",
			existingBM: &v1alpha1.SubnetConnectionBindingMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
				},
				Spec: v1alpha1.SubnetConnectionBindingMapSpec{
					SubnetName:          "child",
					TargetSubnetSetName: "parent",
					VLANTrafficTag:      v1alpha1.VLANTrafficTagPtr(101),
				},
				Status: v1alpha1.SubnetConnectionBindingMapStatus{
					Conditions: []v1alpha1.Condition{
						{
							Type:    v1alpha1.Ready,
							Status:  corev1.ConditionFalse,
							Reason:  "DependencyNotReady",
							Message: "old error",
						},
					},
				},
			},
			condition: v1alpha1.Condition{
				Type:    v1alpha1.Ready,
				Status:  corev1.ConditionTrue,
				Reason:  "SubnetConnectionBindingMapReady",
				Message: "NSX resource ready",
			},
			expStatus:  corev1.ConditionTrue,
			expReason:  "SubnetConnectionBindingMapReady",
			expMessage: "NSX resource ready",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(tc.existingBM).WithStatusSubresource(tc.existingBM).Build()

			updateBindingMapCondition(fakeClient, ctx, tc.existingBM, tc.condition)

			updatedBM := &v1alpha1.SubnetConnectionBindingMap{}
			err := fakeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, updatedBM)
			require.NoError(t, err)
			require.Equal(t, 1, len(updatedBM.Status.Conditions))
			cond := updatedBM.Status.Conditions[0]
			assert.Equal(t, tc.expStatus, cond.Status)
			assert.Equal(t, tc.expReason, cond.Reason)
			assert.Equal(t, tc.expMessage, cond.Message)
			assert.Equal(t, v1alpha1.Ready, cond.Type)
		})
	}
}

func TestUpdateBindingMapConditionGetNotFound(t *testing.T) {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))

	name := "binding-not-exist"
	namespace := "default"

	// Create a bindingMap that doesn't exist in the fake client
	bindingMap := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	condition := v1alpha1.Condition{
		Type:    v1alpha1.Ready,
		Status:  corev1.ConditionTrue,
		Reason:  "SubnetConnectionBindingMapReady",
		Message: "Ready",
	}

	ctx := context.Background()
	// Create fake client without the bindingMap object
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).Build()

	// This should not panic, just log the error
	updateBindingMapCondition(fakeClient, ctx, bindingMap, condition)

	// Verify the object still doesn't exist
	updatedBM := &v1alpha1.SubnetConnectionBindingMap{}
	err := fakeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, updatedBM)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestListBindingMapIDsFromCRs(t *testing.T) {
	bm1 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "binding1-uuid",
			Namespace: "default",
			Name:      "binding1",
		},
	}
	bm2 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "binding2-uuid",
			Namespace: "ns1",
			Name:      "binding2",
		},
	}
	for _, tc := range []struct {
		name    string
		patches func(t *testing.T, r *Reconciler) *gomonkey.Patches
		objects []client.Object
		expCRs  []string
		expErr  string
	}{
		{
			name: "Failed to list CRs",
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Client), "List", func(_ client.Client, ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
					return fmt.Errorf("unable to list CRs")
				})
				return patches
			},
			expCRs: []string{},
			expErr: "unable to list CRs",
		}, {
			name:    "Success",
			objects: []client.Object{bm1, bm2},
			expCRs:  []string{"binding1-uuid", "binding2-uuid"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			r := createFakeReconciler(tc.objects...)
			if tc.patches != nil {
				patches := tc.patches(t, r)
				defer patches.Reset()
			}

			crIDs, err := r.listBindingMapIDsFromCRs(ctx)
			if tc.expErr != "" {
				require.EqualError(t, err, tc.expErr)
			}
			assert.ElementsMatch(t, tc.expCRs, crIDs.UnsortedList())
		})
	}
}

func TestPredicateFuncsBindingMaps(t *testing.T) {
	name := "binding1"
	namespace := "default"
	bindingMap1 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:          "child",
			TargetSubnetSetName: "parent",
			VLANTrafficTag:      v1alpha1.VLANTrafficTagPtr(101),
		},
		Status: v1alpha1.SubnetConnectionBindingMapStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.Ready,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	bindingMap2 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:          "child",
			TargetSubnetSetName: "parent",
			VLANTrafficTag:      v1alpha1.VLANTrafficTagPtr(102),
		},
		Status: v1alpha1.SubnetConnectionBindingMapStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.Ready,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	bindingMap3 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:          "child",
			TargetSubnetSetName: "parent",
			VLANTrafficTag:      v1alpha1.VLANTrafficTagPtr(101),
		},
		Status: v1alpha1.SubnetConnectionBindingMapStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:    v1alpha1.Ready,
					Status:  corev1.ConditionFalse,
					Message: "old message",
					Reason:  "crNotFound",
				},
			},
		},
	}
	createEvent := event.CreateEvent{Object: bindingMap1}
	updateEvent1 := event.UpdateEvent{ObjectOld: bindingMap1, ObjectNew: bindingMap2}
	updateEvent2 := event.UpdateEvent{ObjectOld: bindingMap1, ObjectNew: bindingMap3}
	deleteEvent := event.DeleteEvent{Object: bindingMap1}
	genericEvent := event.GenericEvent{Object: bindingMap1}
	assert.True(t, PredicateFuncsForBindingMaps.CreateFunc(createEvent))
	assert.True(t, PredicateFuncsForBindingMaps.Update(updateEvent1))
	assert.False(t, PredicateFuncsForBindingMaps.Update(updateEvent2))
	assert.True(t, PredicateFuncsForBindingMaps.Delete(deleteEvent))
	assert.False(t, PredicateFuncsForBindingMaps.GenericFunc(genericEvent))
}

func TestSubnetConnectionBindingMapSubnetNameAndTargetSubnetNameIndexFunc(t *testing.T) {
	branchBM := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:        "parent1",
			TargetSubnetName:  "child1",
			SubnetAssociation: v1alpha1.SubnetAssociationBranch,
		},
	}
	trunkBM := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       "child2",
			TargetSubnetName: "parent2",
		},
	}
	emptyBM := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
	}

	// Test SubnetNameIndexFunc
	assert.Equal(t, []string{"parent1"}, subnetConnectionBindingMapSubnetNameIndexFunc(branchBM))
	assert.Equal(t, []string{"child2"}, subnetConnectionBindingMapSubnetNameIndexFunc(trunkBM))
	assert.Equal(t, []string{}, subnetConnectionBindingMapSubnetNameIndexFunc(emptyBM))
	assert.Equal(t, []string{}, subnetConnectionBindingMapSubnetNameIndexFunc(&v1alpha1.Subnet{}))

	// Test TargetSubnetNameIndexFunc
	assert.Equal(t, []string{"child1"}, subnetConnectionBindingMapTargetSubnetNameIndexFunc(branchBM))
	assert.Equal(t, []string{"parent2"}, subnetConnectionBindingMapTargetSubnetNameIndexFunc(trunkBM))
	assert.Equal(t, []string{}, subnetConnectionBindingMapTargetSubnetNameIndexFunc(emptyBM))
	assert.Equal(t, []string{}, subnetConnectionBindingMapTargetSubnetNameIndexFunc(&v1alpha1.Subnet{}))
}

func TestGetSubnetConnectionBindingMapsBySubnetNameIndex(t *testing.T) {
	bm1 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-1",
			Name:      "bm-1",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       "subnet-child-1",
			TargetSubnetName: "subnet-parent-1",
		},
	}

	bm2 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-1",
			Name:      "bm-2",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       "subnet-child-2",
			TargetSubnetName: "subnet-parent-2",
		},
	}

	bmCrossNS := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-vpc-b",
			Name:      "bm-cross",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:        "parent-subnet",
			TargetSubnetName:  "child-subnet",
			SubnetAssociation: v1alpha1.SubnetAssociationBranch,
		},
	}

	r := createFakeReconciler(bm1, bm2, bmCrossNS)
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	r.Client = fake.NewClientBuilder().
		WithScheme(newScheme).
		WithObjects(bm1, bm2, bmCrossNS).
		WithIndex(&v1alpha1.SubnetConnectionBindingMap{}, "spec.subnetName", subnetConnectionBindingMapSubnetNameIndexFunc).
		WithIndex(&v1alpha1.SubnetConnectionBindingMap{}, "spec.targetSubnetName", subnetConnectionBindingMapTargetSubnetNameIndexFunc).
		Build()

	ctx := context.TODO()
	list := &v1alpha1.SubnetConnectionBindingMapList{}
	err := r.Client.List(ctx, list, client.InNamespace("ns-1"), client.MatchingFields{"spec.targetSubnetName": "subnet-parent-1"})
	assert.Nil(t, err)
	assert.Equal(t, 1, len(list.Items))
	assert.Equal(t, "bm-1", list.Items[0].Name)

	list = &v1alpha1.SubnetConnectionBindingMapList{}
	err = r.Client.List(ctx, list, client.InNamespace("ns-1"), client.MatchingFields{"spec.subnetName": "subnet-child-2"})
	assert.Nil(t, err)
	assert.Equal(t, 1, len(list.Items))
	assert.Equal(t, "bm-2", list.Items[0].Name)

	list = &v1alpha1.SubnetConnectionBindingMapList{}
	err = r.Client.List(ctx, list, client.InNamespace("ns-vpc-b"), client.MatchingFields{"spec.targetSubnetName": "child-subnet"})
	assert.Nil(t, err)
	assert.Equal(t, 1, len(list.Items))
	assert.Equal(t, "bm-cross", list.Items[0].Name)
}

func createFakeReconciler(objs ...client.Object) *Reconciler {
	var mgr ctrl.Manager
	if len(objs) == 0 {
		mgr = newMockManager()
	} else {
		mgr = newMockManager(objs...)
	}

	svc := common.Service{
		Client:    mgr.GetClient(),
		NSXClient: &nsx.Client{},

		NSXConfig: &config.NSXOperatorConfig{
			NsxConfig: &config.NsxConfig{
				EnforcementPoint:   "vmc-enforcementpoint",
				UseAVILoadBalancer: false,
			},
		},
	}
	subnetStore := &subnet.SubnetStore{
		ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{}),
		},
	}
	subnetService := &subnet.SubnetService{
		Service:     svc,
		SubnetStore: subnetStore,
	}
	bindingService := &subnetbinding.BindingService{
		Service:      svc,
		BindingStore: subnetbinding.SetupStore(),
	}

	return NewReconciler(mgr, subnetService, bindingService)
}
