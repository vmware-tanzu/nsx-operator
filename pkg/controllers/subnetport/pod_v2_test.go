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
		podV2           bool
		hostName        string
	}{
		{name: "missing-node", wantError: "not found"},
		{name: "nil-node", nodes: podNodeCacheResult{nil}, wantError: "no unique ID"},
		{name: "nil-uuid", nodes: podNodeCacheResult{{}}, wantError: "no unique ID"},
		{name: "empty-uuid", nodes: podNodeCacheResult{{UniqueId: servicecommon.String("")}}, wantError: "no unique ID"},
		{name: "v2-explicit-missing-host", podV2: true, hostName: "esx1", wantError: "not found"},
		{name: "v2-nil-node", podV2: true, nodes: podNodeCacheResult{nil}, wantError: "no unique ID"},
		{name: "v2-nil-uuid", podV2: true, nodes: podNodeCacheResult{{}}, wantError: "no unique ID"},
		{name: "v2-empty-uuid", podV2: true, nodes: podNodeCacheResult{{UniqueId: servicecommon.String("")}}, wantError: "no unique ID"},
		{name: "v2-ambiguous-node", podV2: true, nodes: podNodeCacheResult{{UniqueId: servicecommon.String("tn1")}, {UniqueId: servicecommon.String("tn2")}}, wantError: "multiple node IDs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testPodSubnetPortNodeFailure(t, tc.nodes, tc.wantError, tc.podV2, tc.hostName)
		})
	}
}

func testPodSubnetPortNodeFailure(t *testing.T, nodes podNodeCacheResult, wantError string, podV2 bool, hostName string) {
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
	sp.Annotations = map[string]string{servicecommon.AnnotationESXHostName: hostName}
	api := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod, sp).WithStatusSubresource(sp).Build()
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}, K8sConfig: &config.K8sConfig{PodV2: &podV2}}
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

// Exercise creation and the subsequent host update through both the controller
// and service, mocking only allocation and NSX realization.
func TestPodV2SubnetPortBeforeHostAnnotation(t *testing.T) {
	for _, nodeName := range []string{"virtual-node", ""} {
		t.Run("node="+nodeName, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "ns", UID: "pod-uid"}, Spec: corev1.PodSpec{NodeName: nodeName}}
			sp := &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{Name: "port", Namespace: "ns", UID: "aaaaaaaa-1234-1234-1234-123456789abc", OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: pod.Name, UID: pod.UID}}},
				Spec:       v1alpha1.SubnetPortSpec{InterfaceIPType: v1alpha1.IPAddressTypeIPv4, StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4},
			}
			api := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(sp).
				WithObjects(pod, sp, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns", UID: "ns-uid"}}).Build()
			cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}, K8sConfig: &config.K8sConfig{PodV2: servicecommon.Bool(true)}, CoeConfig: &config.CoeConfig{Cluster: "test"}}
			backend := &podRestorePortsClient{ports: map[string]model.VpcSubnetPort{}}
			svc := &serviceport.SubnetPortService{Service: servicecommon.Service{Client: api, NSXConfig: cfg, NSXClient: &nsx.Client{PortClient: backend}}, SubnetPortStore: newPodRestorePortStore()}
			nsxSubnet := &model.VpcSubnet{Id: servicecommon.String("subnet"), Path: servicecommon.String("/orgs/default/projects/default/vpcs/vpc/subnets/subnet"), RealizationId: servicecommon.String("switch-uuid"), IpAddressType: servicecommon.String("IPV4"), IpAddresses: []string{"10.0.0.0/24"}, AccessMode: servicecommon.String("PUBLIC"), AdvancedConfig: &model.SubnetAdvancedConfig{StaticIpAllocation: &model.StaticIpAllocation{Enabled: servicecommon.Bool(true)}}}
			vpcSvc := &mock.MockVPCServiceProvider{}
			vpcSvc.On("IsRADeactivatedByVPCPath", testifymock.Anything).Return(false, nil)
			r := &SubnetPortReconciler{Client: api, SubnetPortService: svc, SubnetService: podRestoreSubnetService{subnet: nsxSubnet}, VPCService: vpcSvc, IpAddressAllocationService: &mock.MockIPAddressAllocationProvider{}, NodeServiceReader: podNodeCacheResult{}, Recorder: fakeRecorder{}}
			r.StatusUpdater = common.NewStatusUpdater(api, cfg, r.Recorder, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")
			patches := gomonkey.ApplyFunc((*SubnetPortReconciler).CheckAndGetSubnetPathForSubnetPort, func(*SubnetPortReconciler, context.Context, *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
				return true, false, *nsxSubnet.Path, nil, nil, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeIPv4, nil
			})
			defer patches.Reset()
			patches.ApplyFunc(common.IsSharedSubnetPath, func(context.Context, client.Client, string, string) (bool, error) { return false, nil })
			patches.ApplyFunc((*nsx.Client).NSXCheckVersion, func(*nsx.Client, int) bool { return true })
			patches.ApplyFunc((*serviceport.SubnetPortService).CheckSubnetPortState, func(s *serviceport.SubnetPortService, obj interface{}, _ string) (*model.SegmentPortState, error) {
				port, err := s.SubnetPortStore.GetVpcSubnetPortByUID(obj.(*v1alpha1.SubnetPort).UID)
				require.NoError(t, err)
				require.NotNil(t, port)
				return &model.SegmentPortState{Attachment: &model.SegmentPortAttachmentState{Id: port.Attachment.Id}, RealizedBindings: []model.AddressBindingEntry{{Binding: &model.PacketAddressClassifier{IpAddress: servicecommon.String("10.0.0.10"), MacAddress: servicecommon.String("02:50:56:00:00:01")}}}}, nil
			})
			reconcile := func() model.VpcSubnetPort {
				t.Helper()
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(sp)})
				require.NoError(t, err)
				require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(sp), sp))
				require.True(t, common.IsObjectReady(sp.Status.Conditions))
				require.Equal(t, "10.0.0.10/24", sp.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress)
				require.Equal(t, "02:50:56:00:00:01", sp.Status.NetworkInterfaceConfig.MACAddress)
				require.Len(t, backend.ports, 1)
				port, err := svc.SubnetPortStore.GetVpcSubnetPortByUID(sp.UID)
				require.NoError(t, err)
				require.Equal(t, sp.Status.Attachment.ID, *port.Attachment.Id)
				return backend.ports[*port.Id]
			}
			// Network configuration is available before the host is known.
			port := reconcile()
			require.Nil(t, port.Attachment.ContextId)
			require.Equal(t, 1, backend.patches)
			portID, attachmentID := *port.Id, *port.Attachment.Id
			// A later annotation updates the same port, preserving its attachment.
			sp.Annotations = map[string]string{servicecommon.AnnotationESXHostName: "esx1"}
			require.NoError(t, api.Update(ctx, sp))
			r.NodeServiceReader = podRestoreNodeService{}
			port = reconcile()
			require.Equal(t, "host-uuid", *port.Attachment.ContextId)
			require.Equal(t, portID, *port.Id)
			require.Equal(t, attachmentID, *port.Attachment.Id)
			require.Equal(t, 2, backend.patches)
			// Losing the host hint must not erase context during a label update.
			delete(sp.Annotations, servicecommon.AnnotationESXHostName)
			require.NoError(t, api.Update(ctx, sp))
			pod.Labels = map[string]string{"test-label": "updated"}
			require.NoError(t, api.Update(ctx, pod))
			r.NodeServiceReader = podNodeCacheResult{}
			port = reconcile()
			require.Equal(t, "host-uuid", *port.Attachment.ContextId)
			require.Equal(t, portID, *port.Id)
			require.Equal(t, attachmentID, *port.Attachment.Id)
			require.Equal(t, 3, backend.patches)
		})
	}
}
