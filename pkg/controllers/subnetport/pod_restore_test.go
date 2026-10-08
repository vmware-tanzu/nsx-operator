package subnetport

import (
	"context"
	"fmt"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	nsxsubnets "github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs/subnets"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	podcontroller "github.com/vmware-tanzu/nsx-operator/pkg/controllers/pod"
	"github.com/vmware-tanzu/nsx-operator/pkg/mock"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	serviceport "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

type podRestorePortsClient struct {
	nsxsubnets.PortsClient
	ports   map[string]model.VpcSubnetPort
	patches int
}

func (c *podRestorePortsClient) Patch(_, _, _, _, id string, port model.VpcSubnetPort) error {
	c.patches++
	c.ports[id] = port
	return nil
}

func (c *podRestorePortsClient) Get(_, _, _, _, id string) (model.VpcSubnetPort, error) {
	port, ok := c.ports[id]
	if !ok {
		return model.VpcSubnetPort{}, fmt.Errorf("port %s not found", id)
	}
	return port, nil
}

func (c *podRestorePortsClient) Delete(_, _, _, _, id string) error {
	delete(c.ports, id)
	return nil
}

type podRestoreSubnetService struct {
	servicecommon.SubnetServiceProvider
	subnet *model.VpcSubnet
}

func (s podRestoreSubnetService) GetSubnetsByIndex(_, _ string) []*model.VpcSubnet {
	return []*model.VpcSubnet{s.subnet}
}
func (s podRestoreSubnetService) GetSubnetByPath(_ string, _ bool) (*model.VpcSubnet, error) {
	return s.subnet, nil
}
func (s podRestoreSubnetService) GetAllGatewayPrefixesOfSubnet(*model.VpcSubnet) ([]servicecommon.GatewayPrefixInfo, error) {
	return []servicecommon.GatewayPrefixInfo{{Gateway: "10.0.0.1", Prefix: 24}}, nil
}

type podRestoreNodeService struct{}

func (podRestoreNodeService) GetNodeByName(string) []*model.HostTransportNode {
	return []*model.HostTransportNode{{UniqueId: servicecommon.String("host-uuid")}}
}

// Model informer lag for SubnetPort lists, while writes and the APIReader see
// the real objects. Restore must not rely on a new CR appearing in the cache.
type podRestoreCachedClient struct{ client.Client }

func (c podRestoreCachedClient) List(ctx context.Context, list client.ObjectList, options ...client.ListOption) error {
	if ports, ok := list.(*v1alpha1.SubnetPortList); ok {
		ports.Items = nil
		return nil
	}
	return c.Client.List(ctx, list, options...)
}

func newPodRestorePortStore() *serviceport.SubnetPortStore {
	indexers := cache.Indexers{}
	for _, scope := range []string{servicecommon.TagScopeSubnetPortCRUID, servicecommon.TagScopePodUID, servicecommon.TagScopeNamespace, servicecommon.TagScopeVMNamespace, servicecommon.TagScopeStatefulSetUID} {
		indexers[scope] = func(obj interface{}) ([]string, error) {
			var values []string
			for _, tag := range obj.(*model.VpcSubnetPort).Tags {
				if *tag.Scope == scope {
					values = append(values, *tag.Tag)
				}
			}
			return values, nil
		}
	}
	indexers[servicecommon.IndexKeySubnetPath] = func(obj interface{}) ([]string, error) {
		return []string{*obj.(*model.VpcSubnetPort).ParentPath}, nil
	}
	return &serviceport.SubnetPortStore{ResourceStore: servicecommon.ResourceStore{
		Indexer: cache.NewIndexer(func(obj interface{}) (string, error) {
			if key, ok := obj.(string); ok {
				return key, nil
			}
			return *obj.(*model.VpcSubnetPort).Id, nil
		}, indexers),
		BindingType: model.VpcSubnetPortBindingType(),
	}}
}

func TestPodV2RestoreThenNormalReconcileKeepsOnePort(t *testing.T) {
	for _, restoreVIF := range []bool{true, false} {
		for _, failStatus := range []bool{false, true} {
			t.Run(fmt.Sprintf("restore_vif=%v/status_failure=%v", restoreVIF, failStatus), func(t *testing.T) {
				ctx := context.Background()
				scheme := runtime.NewScheme()
				require.NoError(t, v1.AddToScheme(scheme))
				require.NoError(t, v1alpha1.AddToScheme(scheme))
				pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{
					Name: "workload", Namespace: "ns", UID: "12345678-1234-1234-1234-123456789abc",
					Annotations: map[string]string{servicecommon.AnnotationPodMAC: "02:50:56:00:00:01", servicecommon.AnnotationAttachment: "original-attachment"},
				}, Spec: v1.PodSpec{NodeName: "esx1"}, Status: v1.PodStatus{Phase: v1.PodRunning, PodIP: "10.0.0.10", PodIPs: []v1.PodIP{{IP: "10.0.0.10"}}}}
				ss := &v1alpha1.SubnetSet{ObjectMeta: metav1.ObjectMeta{Name: "pod-default", Namespace: "ns", UID: "subnetset-uid", Labels: map[string]string{servicecommon.LabelDefaultNetwork: servicecommon.DefaultPodNetwork}}, Spec: v1alpha1.SubnetSetSpec{IPAddressType: v1alpha1.IPAddressTypeIPv4}}
				statusUnavailable := failStatus
				api := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.SubnetPort{}).
					WithObjects(pod, ss, &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns", UID: "namespace-uid"}}).
					WithIndex(&v1alpha1.SubnetPort{}, util.SubnetPortNamespacePodIndexKey, subnetPortNamespacePodIndexFunc).
					WithIndex(&v1alpha1.Subnet{}, util.SubnetAssociatedResource, subnetAssociatedResourceIndexFunc).
					WithInterceptorFuncs(interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						if sp, ok := obj.(*v1alpha1.SubnetPort); ok {
							sp.UID = "aaaaaaaa-1234-1234-1234-123456789abc"
						}
						return c.Create(ctx, obj, opts...)
					}, SubResourceUpdate: func(ctx context.Context, c client.Client, subresource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						if subresource == "status" && statusUnavailable {
							return fmt.Errorf("status temporarily unavailable")
						}
						return c.SubResource(subresource).Update(ctx, obj, opts...)
					}}).Build()
				cached := podRestoreCachedClient{api}
				cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{PodV2: servicecommon.Bool(true), RestoreVif: &restoreVIF}, CoeConfig: &config.CoeConfig{Cluster: "test"}}
				backend := &podRestorePortsClient{ports: map[string]model.VpcSubnetPort{}}
				svc := &serviceport.SubnetPortService{Service: servicecommon.Service{Client: api, NSXConfig: cfg, NSXClient: &nsx.Client{PortClient: backend}}, SubnetPortStore: newPodRestorePortStore()}
				p := &podcontroller.PodReconciler{Client: cached, APIReader: api, Scheme: scheme, SubnetPortService: svc, Recorder: fakeRecorder{}}
				p.StatusUpdater = common.NewStatusUpdater(cached, cfg, p.Recorder, "Pod", "SubnetPort", "Pod")
				patches := gomonkey.ApplyFunc((*nsx.Client).NSXCheckVersion, func(*nsx.Client, int) bool { return true })
				defer patches.Reset()
				patches.ApplyGlobalVar(&util.K8sClientRetry, wait.Backoff{Steps: 1})
				patches.ApplyFunc((*serviceport.SubnetPortService).CheckSubnetPortState, func(s *serviceport.SubnetPortService, obj interface{}, path string) (*model.SegmentPortState, error) {
					sp := obj.(*v1alpha1.SubnetPort)
					port, err := s.SubnetPortStore.GetVpcSubnetPortByUID(sp.UID)
					require.NoError(t, err)
					require.NotNil(t, port)
					return &model.SegmentPortState{Attachment: &model.SegmentPortAttachmentState{Id: port.Attachment.Id}, RealizedBindings: []model.AddressBindingEntry{{Binding: &model.PacketAddressClassifier{IpAddress: servicecommon.String("10.0.0.10"), MacAddress: servicecommon.String("02:50:56:00:00:01")}}}}, nil
				})
				// Missing NSX port and missing CR: Pod restore only creates the CR.
				require.NoError(t, p.RestoreReconcile())
				require.NoError(t, p.RestoreReconcile()) // retry while List cache is still stale
				crs := &v1alpha1.SubnetPortList{}
				require.NoError(t, api.List(ctx, crs))
				require.Len(t, crs.Items, 1)
				require.Empty(t, crs.Items[0].Status.NetworkInterfaceConfig.IPAddresses)
				require.Empty(t, backend.ports)
				nsxSubnet := &model.VpcSubnet{Id: servicecommon.String("subnet"), Path: servicecommon.String("/orgs/default/projects/default/vpcs/vpc/subnets/subnet"), RealizationId: servicecommon.String("switch-uuid"), IpAddressType: servicecommon.String("IPV4"), IpAddresses: []string{"10.0.0.0/24"}, AccessMode: servicecommon.String("PUBLIC"), AdvancedConfig: &model.SubnetAdvancedConfig{StaticIpAllocation: &model.StaticIpAllocation{Enabled: servicecommon.Bool(true)}}}
				vpcSvc := &mock.MockVPCServiceProvider{}
				vpcSvc.On("IsRADeactivatedByVPCPath", testifymock.Anything).Return(false, nil)
				r := &SubnetPortReconciler{Client: cached, APIReader: api, Scheme: scheme, SubnetPortService: svc, SubnetService: podRestoreSubnetService{subnet: nsxSubnet}, VPCService: vpcSvc, IpAddressAllocationService: &mock.MockIPAddressAllocationProvider{}, NodeServiceReader: podRestoreNodeService{}, Recorder: fakeRecorder{}}
				r.StatusUpdater = common.NewStatusUpdater(cached, cfg, r.Recorder, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")
				if failStatus {
					require.ErrorContains(t, r.RestoreReconcile(), "restore status is not persisted")
					require.Len(t, backend.ports, 1)
					pending, err := r.getRestoreList()
					require.NoError(t, err)
					require.Len(t, pending, 1, "retry must include the CR even though its NSX port now exists")
					statusUnavailable = false
				}
				require.NoError(t, r.RestoreReconcile())
				require.Len(t, backend.ports, 1)
				key := client.ObjectKeyFromObject(&crs.Items[0])
				restored := &v1alpha1.SubnetPort{}
				require.NoError(t, api.Get(ctx, key, restored))
				require.True(t, common.IsObjectReady(restored.Status.Conditions))
				require.Equal(t, "02:50:56:00:00:01", restored.Status.NetworkInterfaceConfig.MACAddress)
				require.Equal(t, "10.0.0.10/24", restored.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress)
				attachment := restored.Status.Attachment.ID
				if restoreVIF {
					require.Equal(t, "original-attachment", attachment)
				} else {
					require.NotEqual(t, "original-attachment", attachment)
				}
				for _, port := range backend.ports {
					require.Equal(t, "10.0.0.10", *port.AddressBindings[0].IpAddress)
					require.Equal(t, "02:50:56:00:00:01", *port.AddressBindings[0].MacAddress)
					require.Equal(t, attachment, *port.Attachment.Id)
				}
				require.NoError(t, r.RestoreReconcile()) // completed restore is idempotent
				// Simulate fresh controllers after restart: same CR and same NSX store.
				normalPod := &podcontroller.PodReconciler{Client: api, APIReader: api, Scheme: scheme, SubnetPortService: svc, Recorder: fakeRecorder{}, StatusUpdater: p.StatusUpdater}
				_, err := normalPod.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}})
				require.NoError(t, err)
				r.restoreMode = false
				r.Client = api
				_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
				require.NoError(t, err)
				require.Len(t, backend.ports, 1)
				require.Equal(t, 1, backend.patches)
				require.NoError(t, api.Get(ctx, key, restored))
				require.Equal(t, attachment, restored.Status.Attachment.ID)
			})
		}
	}
}

func TestRestoreSubnetPortStatusFromPod(t *testing.T) {
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "pod", Namespace: "ns", Annotations: map[string]string{
			servicecommon.AnnotationAttachment: "pod-attachment", servicecommon.AnnotationPodMAC: "02:50:56:00:00:01",
		},
	}, Status: v1.PodStatus{PodIP: "10.0.0.10", PodIPs: []v1.PodIP{{IP: "10.0.0.10"}, {IP: "fd00::10"}}}}
	t.Run("new CR restores both IP families", func(t *testing.T) {
		sp := &v1alpha1.SubnetPort{}
		require.NoError(t, restoreSubnetPortStatusFromPod(sp, pod))
		require.Equal(t, "pod-attachment", sp.Status.Attachment.ID)
		require.Equal(t, "02:50:56:00:00:01", sp.Status.NetworkInterfaceConfig.MACAddress)
		require.Equal(t, []v1alpha1.NetworkInterfaceIPAddress{{IPAddress: "10.0.0.10"}, {IPAddress: "fd00::10"}}, sp.Status.NetworkInterfaceConfig.IPAddresses)
		require.Empty(t, sp.Status.Conditions, "copying Pod state must not mark the port ready")
	})
	t.Run("existing CR retains its persisted state", func(t *testing.T) {
		sp := &v1alpha1.SubnetPort{Status: v1alpha1.SubnetPortStatus{
			Attachment:             v1alpha1.PortAttachment{ID: "cr-attachment"},
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{MACAddress: "02:50:56:00:00:02", IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{{IPAddress: "10.0.0.11/24", Gateway: "10.0.0.1"}}},
		}}
		before := sp.Status.DeepCopy()
		require.NoError(t, restoreSubnetPortStatusFromPod(sp, pod))
		require.Equal(t, *before, sp.Status)
	})
	t.Run("gateway-only CR obtains IPs from Pod", func(t *testing.T) {
		sp := &v1alpha1.SubnetPort{Status: v1alpha1.SubnetPortStatus{
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{{Gateway: "10.0.0.1"}}},
		}}
		require.NoError(t, restoreSubnetPortStatusFromPod(sp, pod))
		require.Equal(t, []v1alpha1.NetworkInterfaceIPAddress{{IPAddress: "10.0.0.10"}, {IPAddress: "fd00::10"}}, sp.Status.NetworkInterfaceConfig.IPAddresses)
	})
	t.Run("PodIP fallback", func(t *testing.T) {
		oldPod := pod.DeepCopy()
		oldPod.Status.PodIPs = nil
		sp := &v1alpha1.SubnetPort{}
		require.NoError(t, restoreSubnetPortStatusFromPod(sp, oldPod))
		require.Equal(t, []v1alpha1.NetworkInterfaceIPAddress{{IPAddress: "10.0.0.10"}}, sp.Status.NetworkInterfaceConfig.IPAddresses)
	})
	t.Run("missing saved IP stops restore for static IPs", func(t *testing.T) {
		incompletePod := pod.DeepCopy()
		incompletePod.Status = v1.PodStatus{}
		sp := &v1alpha1.SubnetPort{Spec: v1alpha1.SubnetPortSpec{StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4}}
		require.ErrorContains(t, restoreSubnetPortStatusFromPod(sp, incompletePod), "no persisted IP/MAC")
	})
	t.Run("DHCP bypasses missing IP/MAC check", func(t *testing.T) {
		incompletePod := pod.DeepCopy()
		incompletePod.Status = v1.PodStatus{}
		sp := &v1alpha1.SubnetPort{Spec: v1alpha1.SubnetPortSpec{StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone}}
		require.NoError(t, restoreSubnetPortStatusFromPod(sp, incompletePod))
	})
}
