package subnetport

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	vmv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	pkgmock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	portservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
)

// Exercise the real selection and reservation paths, then fail after allocation.
// Every retry must select the requested CIDR and release both addresses and locks.
func TestStagingSubnetPortReconcileReleasesRequestedIPs(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	set := &v1alpha1.SubnetSet{ObjectMeta: metav1.ObjectMeta{Name: "set", Namespace: "vmware-system-net-staging", UID: "staging-set"}, Spec: v1alpha1.SubnetSetSpec{SubnetNames: &[]string{"wrong", "right"}, IPAddressType: v1alpha1.IPAddressTypeIPv4}}
	port := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{Name: "port", Namespace: set.Namespace, UID: "staging-port"}, Spec: v1alpha1.SubnetPortSpec{SubnetSet: set.Name, AddressBindings: []v1alpha1.PortAddressBinding{{IPAddress: "192.0.2.10"}}}}
	wrong := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "wrong", Namespace: set.Namespace}}
	right := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "right", Namespace: set.Namespace}}
	api := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(port).WithObjects(set, port, wrong, right).Build()
	emptyIndex := func(interface{}) ([]string, error) { return nil, nil }
	store := &portservice.SubnetPortStore{ResourceStore: servicecommon.ResourceStore{Indexer: cache.NewIndexer(func(obj interface{}) (string, error) { return *obj.(*model.VpcSubnetPort).Id, nil }, cache.Indexers{
		servicecommon.TagScopeSubnetPortCRUID: emptyIndex,
		servicecommon.TagScopePodUID:          emptyIndex,
		servicecommon.IndexKeySubnetPath:      emptyIndex,
	})}}
	ports := &portservice.SubnetPortService{Service: servicecommon.Service{Client: api, NSXConfig: &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}}, SubnetPortStore: store}
	subnets := &pkgmock.MockSubnetServiceProvider{}
	wrongNSX := &model.VpcSubnet{Id: servicecommon.String("wrong"), Path: servicecommon.String("/orgs/default/projects/default/vpcs/vpc/subnets/wrong"), IpAddresses: []string{"198.51.100.0/24"}}
	rightNSX := &model.VpcSubnet{Id: servicecommon.String("right"), Path: servicecommon.String("/orgs/default/projects/default/vpcs/vpc/subnets/right"), IpAddresses: []string{"192.0.2.0/24"}}
	subnets.On("GetSubnetByCR", testifymock.MatchedBy(func(s *v1alpha1.Subnet) bool { return s.Name == "wrong" })).Return(wrongNSX, nil)
	subnets.On("GetSubnetByCR", testifymock.MatchedBy(func(s *v1alpha1.Subnet) bool { return s.Name == "right" })).Return(rightNSX, nil)
	r := &SubnetPortReconciler{Client: api, APIReader: api, SubnetPortService: ports, SubnetService: subnets, VPCService: &pkgmock.MockVPCServiceProvider{}, Recorder: fakeRecorder{}}
	r.StatusUpdater = common.NewStatusUpdater(api, ports.NSXConfig, r.Recorder, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")
	patches := gomonkey.ApplyFunc((*SubnetPortReconciler).getVirtualMachine, func(_ *SubnetPortReconciler, _ context.Context, current *v1alpha1.SubnetPort, _ bool) (*vmv1alpha1.VirtualMachine, string, error) {
		// Simulate a spec refresh after allocation, ensuring deferred cleanup uses
		// the original address instead of the updated object or backing slice.
		current.Spec.AddressBindings[0].IPAddress = "192.0.2.11"
		return nil, "", fmt.Errorf("owner lookup failed after allocation")
	})
	defer patches.Reset()
	patches.ApplyFunc(setSubnetPortReadyStatusFalse, func(_ client.Client, _ context.Context, _ client.Object, _ metav1.Time, _ error, _ ...interface{}) {})
	for attempt := 0; attempt < 2; attempt++ {
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: port.Namespace, Name: port.Name}})
		require.ErrorContains(t, err, "owner lookup failed after allocation")
		require.True(t, ports.IsEmptySubnet(*rightNSX.Path))
		lock, ok := common.SubnetSetLocks.Load(set.UID)
		require.True(t, ok)
		mutex := lock.(*sync.RWMutex)
		require.True(t, mutex.TryLock())
		mutex.Unlock()
	}
	_, wrongReserved := store.PortCountInfo.Load(*wrongNSX.Path)
	require.False(t, wrongReserved)
	subnets.AssertExpectations(t)
}
