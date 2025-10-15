package subnetipreservation

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
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
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetipreservation"
)

func TestReconciler_StartController(t *testing.T) {
	mockMgr := &MockManager{scheme: runtime.NewScheme()}
	patches := gomonkey.ApplyFunc((*Reconciler).setupWithManager, func(r *Reconciler, mgr manager.Manager) error {
		return nil
	})
	patches.ApplyFunc(common.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context) error) {})
	patches.ApplyFunc((*Reconciler).SetupFieldIndexers, func(r *Reconciler, mgr manager.Manager) error {
		return nil
	})
	defer patches.Reset()
	r := createFakeReconciler()
	err := r.StartController(mockMgr, nil)
	assert.Nil(t, err)
}

func TestReconciler_Reconcile(t *testing.T) {
	request := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "ipr-1",
			Namespace: "ns-1",
		},
	}
	ipr := &v1alpha1.SubnetIPReservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ipr-1",
			Namespace: "ns-1",
		},
	}
	tests := []struct {
		name           string
		objects        []client.Object
		preparedFunc   func(r *Reconciler) *gomonkey.Patches
		expectedResult ctrl.Result
	}{
		{
			name:    "SubnetIPReservation not supported",
			objects: []client.Object{ipr},
			preparedFunc: func(r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.IPReservationService.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
					return false
				})
				return patches
			},
			expectedResult: common.ResultNormal,
		},
		{
			name: "SubnetIPReservation not supported with get error",
			preparedFunc: func(r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.IPReservationService.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
					return false
				})
				patches.ApplyMethod(reflect.TypeOf(r.Client), "Get", func(_ client.Client, ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return fmt.Errorf("mocked get error")
				})
				return patches
			},
			expectedResult: common.ResultRequeue,
		},
		{
			name: "SubnetIPReservation get error",
			preparedFunc: func(r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Client), "Get", func(_ client.Client, ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return fmt.Errorf("mocked get error")
				})
				patches.ApplyMethod(reflect.TypeOf(r.IPReservationService.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
					return true
				})
				return patches
			},
			expectedResult: common.ResultRequeue,
		},
		{
			name: "SubnetIPReservation delete error",
			preparedFunc: func(r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Client), "Get", func(_ client.Client, ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return apierrors.NewNotFound(v1alpha1.Resource("subnetipreservation"), "ipr-1")
				})
				patches.ApplyMethod(reflect.TypeOf(r.IPReservationService), "DeleteIPReservationByCRName", func(_ *subnetipreservation.IPReservationService, ns string, name string) error {
					return fmt.Errorf("mocked delete error")
				})
				patches.ApplyMethod(reflect.TypeOf(r.IPReservationService.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
					return true
				})
				return patches
			},
			expectedResult: common.ResultRequeue,
		},
		{
			name: "SubnetIPReservation delete success",
			preparedFunc: func(r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Client), "Get", func(_ client.Client, ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return apierrors.NewNotFound(v1alpha1.Resource("subnetipreservation"), "ipr-1")
				})
				patches.ApplyMethod(reflect.TypeOf(r.IPReservationService), "DeleteIPReservationByCRName", func(_ *subnetipreservation.IPReservationService, ns string, name string) error {
					return nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.IPReservationService.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
					return true
				})
				return patches
			},
			expectedResult: common.ResultNormal,
		},
		{
			name:    "Subnet validation error",
			objects: []client.Object{ipr},
			preparedFunc: func(r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateSubnet", func(_ *Reconciler, ctx context.Context, ns string, name string) (*v1alpha1.Subnet, *errorWithRetry) {
					return &v1alpha1.Subnet{}, &errorWithRetry{
						error:   fmt.Errorf("Subnet is not realized"),
						retry:   false,
						message: "Subnet is not realized",
					}
				})
				patches.ApplyMethod(reflect.TypeOf(r.IPReservationService.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
					return true
				})
				return patches
			},
			expectedResult: common.ResultNormal,
		},
		{
			name:    "Subnet validation error with retry",
			objects: []client.Object{ipr},
			preparedFunc: func(r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateSubnet", func(_ *Reconciler, ctx context.Context, ns string, name string) (*v1alpha1.Subnet, *errorWithRetry) {
					return &v1alpha1.Subnet{}, &errorWithRetry{
						error:   fmt.Errorf("fail to get Subnet"),
						retry:   true,
						message: "fail to get Subnet",
					}
				})
				patches.ApplyMethod(reflect.TypeOf(r.IPReservationService.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
					return true
				})
				return patches
			},
			expectedResult: common.ResultRequeue,
		},
		{
			name:    "NSX Subnet get error",
			objects: []client.Object{ipr},
			preparedFunc: func(r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateSubnet", func(_ *Reconciler, ctx context.Context, ns string, name string) (*v1alpha1.Subnet, *errorWithRetry) {
					return &v1alpha1.Subnet{}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetByCR", func(_ *subnet.SubnetService, subnet *v1alpha1.Subnet) (*model.VpcSubnet, error) {
					return nil, fmt.Errorf("mocked get error")
				})
				patches.ApplyMethod(reflect.TypeOf(r.IPReservationService.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
					return true
				})
				return patches
			},
			expectedResult: common.ResultRequeue,
		},
		{
			name:    "Create IPReservation error",
			objects: []client.Object{ipr},
			preparedFunc: func(r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateSubnet", func(_ *Reconciler, ctx context.Context, ns string, name string) (*v1alpha1.Subnet, *errorWithRetry) {
					return &v1alpha1.Subnet{}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetByCR", func(_ *subnet.SubnetService, subnet *v1alpha1.Subnet) (*model.VpcSubnet, error) {
					return &model.VpcSubnet{Path: servicecommon.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1")}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.IPReservationService), "GetOrCreateSubnetIPReservation", func(_ *subnetipreservation.IPReservationService, ipReservation *v1alpha1.SubnetIPReservation, subnetPath string) (*model.DynamicIpAddressReservation, error) {
					return nil, fmt.Errorf("mocked creation error")
				})
				patches.ApplyMethod(reflect.TypeOf(r.IPReservationService.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
					return true
				})
				return patches
			},
			expectedResult: common.ResultRequeue,
		},
		{
			name:    "Create IPReservation success",
			objects: []client.Object{ipr},
			preparedFunc: func(r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "validateSubnet", func(_ *Reconciler, ctx context.Context, ns string, name string) (*v1alpha1.Subnet, *errorWithRetry) {
					return &v1alpha1.Subnet{}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetByCR", func(_ *subnet.SubnetService, subnet *v1alpha1.Subnet) (*model.VpcSubnet, error) {
					return &model.VpcSubnet{Path: servicecommon.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1")}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.IPReservationService), "GetOrCreateSubnetIPReservation", func(_ *subnetipreservation.IPReservationService, ipReservation *v1alpha1.SubnetIPReservation, subnetPath string) (*model.DynamicIpAddressReservation, error) {
					return &model.DynamicIpAddressReservation{
						NumberOfIps: servicecommon.Int64(10),
						Ips:         []string{"10.0.0.1-10.0.0.9", "10.0.0.13"},
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.IPReservationService.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
					return true
				})
				return patches
			},
			expectedResult: common.ResultNormal,
		},
	}
	for _, tc := range tests {
		r := createFakeReconciler(tc.objects...)
		patches := tc.preparedFunc(r)
		result, err := r.Reconcile(context.TODO(), request)
		assert.Nil(t, err)
		assert.Equal(t, tc.expectedResult, result)
		if patches != nil {
			patches.Reset()
		}
	}
}

func TestReconcile_validateSubnet(t *testing.T) {
	subnetReady := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "subnet-1",
			Namespace: "ns-1",
		},
		Status: v1alpha1.SubnetStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.Ready,
					Status: v1.ConditionTrue,
				},
			},
		},
	}
	subnetUnready := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "subnet-1",
			Namespace: "ns-1",
		},
		Status: v1alpha1.SubnetStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.Ready,
					Status: v1.ConditionFalse,
				},
			},
		},
	}
	tests := []struct {
		name           string
		objects        []client.Object
		preparedFunc   func(r *Reconciler) *gomonkey.Patches
		expectedErr    string
		expectedMsg    string
		expectedRetry  bool
		expectedSubnet *v1alpha1.Subnet
	}{
		{
			name: "Subnet CR not created",
			preparedFunc: func(r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Client), "Get", func(_ client.Client, ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return apierrors.NewNotFound(v1alpha1.Resource("subnet"), "subnet-1")
				})
				return patches
			},
			expectedErr:   "not found",
			expectedMsg:   "Subnet is not created",
			expectedRetry: false,
		},
		{
			name: "Subnet CR get error",
			preparedFunc: func(r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Client), "Get", func(_ client.Client, ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return fmt.Errorf("mocked error")
				})
				return patches
			},
			expectedErr:   "mocked error",
			expectedMsg:   "failed to get Subnet",
			expectedRetry: true,
		},
		{
			name:          "Subnet CR not realized",
			objects:       []client.Object{subnetUnready},
			expectedErr:   "Subnet is not realized",
			expectedMsg:   "Subnet is not realized",
			expectedRetry: false,
		},
		{
			name:           "Subnet CR ready",
			objects:        []client.Object{subnetReady},
			expectedSubnet: subnetReady,
		},
	}

	for _, tc := range tests {
		r := createFakeReconciler(tc.objects...)
		var patches *gomonkey.Patches
		if tc.preparedFunc != nil {
			patches = tc.preparedFunc(r)
		}
		subnet, err := r.validateSubnet(context.TODO(), "ns-1", "subnet-1")
		if tc.expectedErr != "" {
			assert.Equal(t, tc.expectedRetry, err.retry)
			assert.Contains(t, err.error.Error(), tc.expectedErr)
			assert.Contains(t, err.message, tc.expectedMsg)
		} else {
			assert.Nil(t, err)
			assert.Equal(t, tc.expectedSubnet, subnet)
		}
		if patches != nil {
			patches.Reset()
		}
	}
}

func TestReconcile_CollectGarbage(t *testing.T) {
	ipr1 := &v1alpha1.SubnetIPReservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ipr-1",
			Namespace: "ns-1",
			UID:       "ipr-1-uuid",
		},
		Spec: v1alpha1.SubnetIPReservationSpec{
			NumberOfIPs: 10,
			Subnet:      "subnet-1",
		},
	}
	tests := []struct {
		name         string
		objects      []client.Object
		preparedFunc func(r *Reconciler) *gomonkey.Patches
		expectedErr  string
	}{
		{
			name: "List SubnetIPReservation error",
			preparedFunc: func(r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Client), "List", func(_ client.Client, ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
					return fmt.Errorf("mocked error")
				})
				return patches
			},
			expectedErr: "mocked error",
		},
		{
			name:    "NSX SubnetIPReservation deletion error",
			objects: []client.Object{ipr1},
			preparedFunc: func(r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.IPReservationService), "ListSubnetIPReservationCRUIDsInStore", func(_ *subnetipreservation.IPReservationService) sets.Set[string] {
					return sets.New("ipr-1-uuid", "ipr-2-uuid")
				})
				patches.ApplyMethod(reflect.TypeOf(r.IPReservationService), "DeleteIPReservationByCRId", func(_ *subnetipreservation.IPReservationService, id string) error {
					assert.Equal(t, "ipr-2-uuid", id)
					return fmt.Errorf("mocked deletion error")
				})
				return patches
			},
			expectedErr: "mocked deletion error",
		},
		{
			name:    "Success",
			objects: []client.Object{ipr1},
			preparedFunc: func(r *Reconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.IPReservationService), "ListSubnetIPReservationCRUIDsInStore", func(_ *subnetipreservation.IPReservationService) sets.Set[string] {
					return sets.New("ipr-1-uuid", "ipr-2-uuid")
				})
				patches.ApplyMethod(reflect.TypeOf(r.IPReservationService), "DeleteIPReservationByCRId", func(_ *subnetipreservation.IPReservationService, id string) error {
					assert.Equal(t, "ipr-2-uuid", id)
					return nil
				})
				return patches
			},
		},
	}
	for _, tc := range tests {
		r := createFakeReconciler(tc.objects...)
		patches := tc.preparedFunc(r)
		err := r.CollectGarbage(context.TODO())
		if tc.expectedErr != "" {
			assert.Contains(t, err.Error(), tc.expectedErr)
		} else {
			assert.Nil(t, err)
		}
		patches.Reset()
	}
}

func createFakeReconciler(objs ...client.Object) *Reconciler {
	var mgr ctrl.Manager
	if len(objs) == 0 {
		mgr = newMockManager()
	} else {
		mgr = newMockManager(objs...)
	}

	svc := servicecommon.Service{
		Client:    mgr.GetClient(),
		NSXClient: &nsx.Client{},

		NSXConfig: &config.NSXOperatorConfig{
			NsxConfig: &config.NsxConfig{
				EnforcementPoint: "vmc-enforcementpoint",
			},
		},
	}
	subnetService := &subnet.SubnetService{
		Service:     svc,
		SubnetStore: &subnet.SubnetStore{},
	}
	ipReservationService := &subnetipreservation.IPReservationService{
		Service:            svc,
		IPReservationStore: subnetipreservation.SetupStore(),
	}

	return NewReconciler(mgr, ipReservationService, subnetService)
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

type fakeRecorder struct{}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
}

func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}

func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}
