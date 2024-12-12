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
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(objs...).Build()
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
			VLANTrafficTag:      101,
		},
	}
	for _, tc := range []struct {
		name      string
		objects   []client.Object
		expectRes ctrl.Result
		patches   func(t *testing.T, r *Reconciler) *gomonkey.Patches
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
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateDependency", func(_ *Reconciler, ctx context.Context, bindingMap *v1alpha1.SubnetConnectionBindingMap) (*model.VpcSubnet, []*model.VpcSubnet, *errorWithRetry) {
					return nil, nil, &errorWithRetry{
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
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateDependency", func(_ *Reconciler, ctx context.Context, bindingMap *v1alpha1.SubnetConnectionBindingMap) (*model.VpcSubnet, []*model.VpcSubnet, *errorWithRetry) {
					return nil, nil, &errorWithRetry{
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
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateDependency", func(_ *Reconciler, ctx context.Context, bindingMap *v1alpha1.SubnetConnectionBindingMap) (*model.VpcSubnet, []*model.VpcSubnet, *errorWithRetry) {
					return &model.VpcSubnet{Id: common.String("child")}, []*model.VpcSubnet{{Id: common.String("parent")}}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "CreateOrUpdateSubnetConnectionBindingMap",
					func(_ *subnetbinding.BindingService, subnetBinding *v1alpha1.SubnetConnectionBindingMap, childSubnet *model.VpcSubnet, parentSubnets []*model.VpcSubnet) error {
						return fmt.Errorf("failed to configure NSX")
					})
				return patches
			},
			expectRes: controllerscommon.ResultRequeue,
		}, {
			name:    "Succeeded to create/update SubnetConnectionBindingMap",
			objects: []client.Object{validBM1},
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateDependency", func(_ *Reconciler, ctx context.Context, bindingMap *v1alpha1.SubnetConnectionBindingMap) (*model.VpcSubnet, []*model.VpcSubnet, *errorWithRetry) {
					return &model.VpcSubnet{Id: common.String("child")}, []*model.VpcSubnet{{Id: common.String("parent")}}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "CreateOrUpdateSubnetConnectionBindingMap",
					func(_ *subnetbinding.BindingService, subnetBinding *v1alpha1.SubnetConnectionBindingMap, childSubnet *model.VpcSubnet, parentSubnets []*model.VpcSubnet) error {
						return nil
					})
				return patches
			},
			expectRes: controllerscommon.ResultNormal,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			r := createFakeReconciler(tc.objects...)
			patches := tc.patches(t, r)
			defer patches.Reset()

			rst, _ := r.Reconcile(ctx, request)
			assert.Equal(t, tc.expectRes, rst)
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
	bindingCR1 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       childSubnet,
			TargetSubnetName: targetSubnet,
			VLANTrafficTag:   101,
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
			VLANTrafficTag:      101,
		},
	}

	for _, tc := range []struct {
		name       string
		patches    func(t *testing.T, r *Reconciler) *gomonkey.Patches
		bindingMap *v1alpha1.SubnetConnectionBindingMap
		expErr     string
		expMsg     string
		expChild   *model.VpcSubnet
		expParents []*model.VpcSubnet
	}{
		{
			name:       "child subnet is not ready",
			bindingMap: bindingCR1,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateVpcSubnetsBySubnetCR", func(_ *Reconciler, ctx context.Context, namespace, name string, isTarget bool) ([]*model.VpcSubnet, *errorWithRetry) {
					return nil, &errorWithRetry{
						message: "Unable to get Subnet CR net1",
						error:   fmt.Errorf("unable to get CR"),
					}
				})
				return patches
			},
			expErr:   "unable to get CR",
			expMsg:   "Unable to get Subnet CR net1",
			expChild: nil,
		}, {
			name:       "parent subnet is not ready",
			bindingMap: bindingCR1,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateVpcSubnetsBySubnetCR", func(_ *Reconciler, ctx context.Context, namespace, name string, isTarget bool) ([]*model.VpcSubnet, *errorWithRetry) {
					if !isTarget {
						return []*model.VpcSubnet{{Id: common.String("child")}}, nil
					}
					return nil, &errorWithRetry{
						message: "Unable to get Subnet CR net1",
						error:   fmt.Errorf("unable to get CR"),
					}
				})
				return patches
			},
			expErr:   "unable to get CR",
			expMsg:   "Unable to get Subnet CR net1",
			expChild: nil,
		}, {
			name:       "parent subnet is ready",
			bindingMap: bindingCR1,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateVpcSubnetsBySubnetCR", func(_ *Reconciler, ctx context.Context, namespace, name string, isTarget bool) ([]*model.VpcSubnet, *errorWithRetry) {
					if !isTarget {
						return []*model.VpcSubnet{{Id: common.String("child")}}, nil
					}
					return []*model.VpcSubnet{{Id: common.String("parent")}}, nil
				})
				return patches
			},
			expChild:   &model.VpcSubnet{Id: common.String("child")},
			expParents: []*model.VpcSubnet{{Id: common.String("parent")}},
		}, {
			name:       "parent subnetSet is not ready",
			bindingMap: bindingCR2,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateVpcSubnetsBySubnetCR", func(_ *Reconciler, ctx context.Context, namespace, name string, isTarget bool) ([]*model.VpcSubnet, *errorWithRetry) {
					return []*model.VpcSubnet{{Id: common.String("child")}}, nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "validateVpcSubnetsBySubnetSetCR", func(_ *Reconciler, ctx context.Context, namespace, name string) ([]*model.VpcSubnet, *errorWithRetry) {
					return nil, &errorWithRetry{
						message: "Unable to get Subnet CR net1",
						error:   fmt.Errorf("unable to get CR"),
					}
				})
				return patches
			},
			expErr:   "unable to get CR",
			expMsg:   "Unable to get Subnet CR net1",
			expChild: nil,
		}, {
			name:       "parent subnetSet is ready",
			bindingMap: bindingCR2,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateVpcSubnetsBySubnetCR", func(_ *Reconciler, ctx context.Context, namespace, name string, isTarget bool) ([]*model.VpcSubnet, *errorWithRetry) {
					return []*model.VpcSubnet{{Id: common.String("child")}}, nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "validateVpcSubnetsBySubnetSetCR", func(_ *Reconciler, ctx context.Context, namespace, name string) ([]*model.VpcSubnet, *errorWithRetry) {
					return []*model.VpcSubnet{{Id: common.String("parent")}}, nil
				})
				return patches
			},
			expChild:   &model.VpcSubnet{Id: common.String("child")},
			expParents: []*model.VpcSubnet{{Id: common.String("parent")}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			r := createFakeReconciler()
			patches := tc.patches(t, r)
			defer patches.Reset()

			child, parents, err := r.validateDependency(ctx, tc.bindingMap)
			if tc.expErr != "" {
				require.EqualError(t, err.error, tc.expErr)
				require.Equal(t, tc.expMsg, err.message)
			}
			require.Equal(t, tc.expChild, child)
			require.ElementsMatch(t, tc.expParents, parents)
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
	for _, tc := range []struct {
		name     string
		isTarget bool
		objects  []client.Object
		patches  func(t *testing.T, r *Reconciler) *gomonkey.Patches
		expErr   string
		expMsg   string
		expRetry bool
		subnets  []*model.VpcSubnet
	}{
		{
			name:     "Failed to get Subnet CR",
			isTarget: false,
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
			isTarget: false,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListSubnetCreatedBySubnet", func(_ *subnet.SubnetService, id string) []*model.VpcSubnet {
					return []*model.VpcSubnet{}
				})
				return patches
			},
			objects:  []client.Object{subnetCR},
			expRetry: false,
			expMsg:   "Subnet CR net1 is not realized on NSX",
			expErr:   "not found NSX VpcSubnets created by Subnet CR 'default/net1'",
		}, {
			name:     "Child subnet CR is also used as parent",
			isTarget: false,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListSubnetCreatedBySubnet", func(_ *subnet.SubnetService, id string) []*model.VpcSubnet {
					return []*model.VpcSubnet{{Id: common.String("net1")}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "GetSubnetConnectionBindingMapsByParentSubnet", func(_ *subnetbinding.BindingService, subnet *model.VpcSubnet) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{{Id: common.String("binding1")}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "GetSubnetConnectionBindingMapCRName", func(_ *subnetbinding.BindingService, bindingMap *model.SubnetConnectionBindingMap) string {
					return "binding1"
				})
				return patches
			},
			objects:  []client.Object{subnetCR},
			expRetry: true,
			expMsg:   "Subnet CR net1 is working as target by binding1",
			expErr:   "Subnet net1 already works as target in SubnetConnectionBindingMap binding1",
		}, {
			name:     "Child subnet CR is not used as parent",
			isTarget: false,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListSubnetCreatedBySubnet", func(_ *subnet.SubnetService, id string) []*model.VpcSubnet {
					return []*model.VpcSubnet{{Id: common.String("net1")}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "GetSubnetConnectionBindingMapsByParentSubnet", func(_ *subnetbinding.BindingService, subnet *model.VpcSubnet) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{}
				})
				return patches
			},
			objects: []client.Object{subnetCR},
			subnets: []*model.VpcSubnet{{Id: common.String("net1")}},
		}, {
			name:     "Parent subnet CR is also used as child",
			isTarget: true,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListSubnetCreatedBySubnet", func(_ *subnet.SubnetService, id string) []*model.VpcSubnet {
					return []*model.VpcSubnet{{Id: common.String("net1")}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "GetSubnetConnectionBindingMapsByChildSubnet", func(_ *subnetbinding.BindingService, subnet *model.VpcSubnet) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{{Id: common.String("binding1")}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "GetSubnetConnectionBindingMapCRName", func(_ *subnetbinding.BindingService, bindingMap *model.SubnetConnectionBindingMap) string {
					return "binding1"
				})
				return patches
			},
			objects:  []client.Object{subnetCR},
			expRetry: true,
			expMsg:   "Target Subnet CR net1 is associated by binding1",
			expErr:   "target Subnet net1 is already associated by SubnetConnectionBindingMap binding1",
		}, {
			name:     "Parent subnet CR is not used as child",
			isTarget: true,
			patches: func(t *testing.T, r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListSubnetCreatedBySubnet", func(_ *subnet.SubnetService, id string) []*model.VpcSubnet {
					return []*model.VpcSubnet{{Id: common.String("net1")}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetBindingService), "GetSubnetConnectionBindingMapsByChildSubnet", func(_ *subnetbinding.BindingService, subnet *model.VpcSubnet) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{}
				})
				return patches
			},
			objects: []client.Object{subnetCR},
			subnets: []*model.VpcSubnet{{Id: common.String("net1")}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			r := createFakeReconciler(tc.objects...)
			patches := tc.patches(t, r)
			defer patches.Reset()

			subnets, err := r.validateVpcSubnetsBySubnetCR(ctx, subnetNamespace, subnetName, tc.isTarget)
			if tc.expErr != "" {
				require.EqualError(t, err.error, tc.expErr)
				require.Equal(t, tc.expMsg, err.message)
				require.Equal(t, tc.expRetry, err.retry)
			}
			require.ElementsMatch(t, tc.subnets, subnets)
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
			UID:       "subnetset-uuid",
		},
	}
	for _, tc := range []struct {
		name    string
		objects []client.Object
		patches func(t *testing.T, r *Reconciler) *gomonkey.Patches
		expErr  string
		expMsg  string
		subnets []*model.VpcSubnet
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
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListSubnetCreatedBySubnetSet", func(_ *subnet.SubnetService, id string) []*model.VpcSubnet {
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
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListSubnetCreatedBySubnetSet", func(_ *subnet.SubnetService, id string) []*model.VpcSubnet {
					return []*model.VpcSubnet{{Id: common.String("net1")}}
				})
				return patches
			},
			objects: []client.Object{subnetSetCR},
			expMsg:  "",
			expErr:  "",
			subnets: []*model.VpcSubnet{{Id: common.String("net1")}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			r := createFakeReconciler(tc.objects...)
			patches := tc.patches(t, r)
			defer patches.Reset()

			subnets, err := r.validateVpcSubnetsBySubnetSetCR(ctx, namespace, name)
			if tc.expErr != "" {
				require.EqualError(t, err.error, tc.expErr)
				require.Equal(t, tc.expMsg, err.message)
				require.False(t, err.retry)
			}
			require.ElementsMatch(t, tc.subnets, subnets)
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
			VLANTrafficTag:      101,
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
			VLANTrafficTag:      101,
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
			VLANTrafficTag:      101,
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
			VLANTrafficTag:      101,
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
			VLANTrafficTag:      101,
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
			VLANTrafficTag:      102,
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
			VLANTrafficTag:      101,
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
	subnetService := &subnet.SubnetService{
		Service:     svc,
		SubnetStore: &subnet.SubnetStore{},
	}
	bindingService := &subnetbinding.BindingService{
		Service:      svc,
		BindingStore: subnetbinding.SetupStore(),
	}

	return newReconciler(mgr, subnetService, bindingService)
}
