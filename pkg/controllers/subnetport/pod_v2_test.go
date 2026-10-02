package subnetport

import (
	"context"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/mock"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	serviceport "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
)

type podNodeCacheResult []*model.HostTransportNode

func (nodes podNodeCacheResult) GetNodeByName(string) []*model.HostTransportNode { return nodes }

func TestPodSubnetPortMissingTransportNodeRetries(t *testing.T) {
	for _, tc := range []struct {
		name, wantError string
		nodes           podNodeCacheResult
	}{
		{name: "missing-node", wantError: "not found"},
		{name: "nil-node", nodes: podNodeCacheResult{nil}, wantError: "no unique ID"},
		{name: "nil-uuid", nodes: podNodeCacheResult{{}}, wantError: "no unique ID"},
		{name: "empty-uuid", nodes: podNodeCacheResult{{UniqueId: servicecommon.String("")}}, wantError: "no unique ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testPodSubnetPortNodeFailure(t, tc.nodes, tc.wantError)
		})
	}
}

func testPodSubnetPortNodeFailure(t *testing.T, nodes podNodeCacheResult, wantError string) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "ns", UID: "pod-uid"}, Spec: corev1.PodSpec{NodeName: "esx1"}}
	sp := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{Name: "port", Namespace: "ns", OwnerReferences: []metav1.OwnerReference{{Kind: "Pod", Name: pod.Name, UID: pod.UID}}}, Spec: v1alpha1.SubnetPortSpec{InterfaceIPType: v1alpha1.IPAddressTypeIPv6, StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone}}
	api := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod, sp).WithStatusSubresource(sp).Build()
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}
	svc := &serviceport.SubnetPortService{Service: servicecommon.Service{Client: api, NSXClient: &nsx.Client{}, NSXConfig: cfg}}
	subnetSvc := &mock.MockSubnetServiceProvider{}
	vpcSvc := &mock.MockVPCServiceProvider{}
	vpcSvc.On("IsRADeactivatedByVPCPath", testifymock.Anything).Return(false, nil)
	r := &SubnetPortReconciler{Client: api, SubnetPortService: svc, SubnetService: subnetSvc, VPCService: vpcSvc, NodeServiceReader: nodes, Recorder: fakeRecorder{}}
	r.StatusUpdater = common.NewStatusUpdater(api, cfg, r.Recorder, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")
	patches := gomonkey.ApplyFunc((*SubnetPortReconciler).CheckAndGetSubnetPathForSubnetPort, func(*SubnetPortReconciler, context.Context, *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
		return true, false, "/orgs/default/projects/default/vpcs/v/subnets/s", nil, nil, v1alpha1.IPAddressTypeIPv6, v1alpha1.StaticIPAllocationTypeNone, nil
	})
	defer patches.Reset()
	patches.ApplyFunc(common.IsSharedSubnetPath, func(context.Context, client.Client, string, string) (bool, error) { return false, nil })
	patches.ApplyFunc(setAddressBindingStatusBySubnetPort, func(client.Client, context.Context, *v1alpha1.SubnetPort, *serviceport.SubnetPortService, metav1.Time, error, bool) {
	})
	called := false
	contextID := "unset"
	patches.ApplyFunc((*serviceport.SubnetPortService).CreateOrUpdateSubnetPort, func(_ *serviceport.SubnetPortService, _ interface{}, _ *model.VpcSubnet, ctxID string, _ *map[string]string, _ bool, _ bool, _ v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
		called = true
		contextID = ctxID
		return nil, nil
	})
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: sp.Namespace, Name: sp.Name}})
	require.ErrorContains(t, err, wantError)
	latest := &v1alpha1.SubnetPort{}
	if e := api.Get(context.Background(), client.ObjectKeyFromObject(sp), latest); e != nil {
		t.Fatal(e)
	}
	t.Logf("backend called=%v context_id=%q result=%+v err=%v conditions=%+v", called, contextID, result, err, latest.Status.Conditions)
	if err == nil && result.RequeueAfter == 0 {
		t.Fatal("missing transport node was swallowed: successful result leaves no retry when the node cache becomes ready")
	}
	require.False(t, called, "must resolve the transport node before creating the NSX port")
	require.False(t, common.IsObjectReady(latest.Status.Conditions))
}
