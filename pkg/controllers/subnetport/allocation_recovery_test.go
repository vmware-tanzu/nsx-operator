package subnetport

import (
	"context"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/mock"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
)

func TestRecoveredPortIsExistingForAllSubnetSelectionPaths(t *testing.T) {
	for _, selection := range []string{"subnet", "subnetset", "default"} {
		t.Run(selection, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			subnetCR := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "subnet", Namespace: "test"}, Spec: v1alpha1.SubnetSpec{IPAddressType: v1alpha1.IPAddressTypeIPv4}}
			set := &v1alpha1.SubnetSet{ObjectMeta: metav1.ObjectMeta{Name: "set", Namespace: "test"}, Spec: v1alpha1.SubnetSetSpec{IPAddressType: v1alpha1.IPAddressTypeIPv4}}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(subnetCR, set).Build()
			sp := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{Name: "port", Namespace: "test", UID: "port-uid"}, Spec: v1alpha1.SubnetPortSpec{
				InterfaceIPType: v1alpha1.IPAddressTypeIPv4, StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
				AddressBindings: []v1alpha1.PortAddressBinding{{IPAddress: "192.0.2.10"}},
			}}
			if selection == "subnet" {
				sp.Spec.Subnet = subnetCR.Name
			}
			if selection == "subnetset" {
				sp.Spec.SubnetSet = set.Name
			}
			path := "/orgs/default/projects/default/vpcs/vpc/subnets/shared"
			subnetService := &mock.MockSubnetServiceProvider{}
			subnetService.On("GetSubnetByCR", testifymock.Anything).Return(&model.VpcSubnet{Path: &path}, nil)
			r := &SubnetPortReconciler{Client: c, APIReader: c, SubnetService: subnetService,
				SubnetPortService: &subnetport.SubnetPortService{SubnetPortStore: &subnetport.SubnetPortStore{}},
			}
			var recovered *model.VpcSubnetPort
			calls := 0
			recoverPort := func(bindings []v1alpha1.PortAddressBinding, uid []types.UID) {
				require.Equal(t, []types.UID{sp.UID}, uid, "allocation must receive the actual CR identity")
				require.Equal(t, sp.Spec.AddressBindings, bindings)
				recovered = &model.VpcSubnetPort{Id: servicecommon.String("existing"), ParentPath: &path}
				calls++
			}
			patches := gomonkey.ApplyMethod(r.SubnetPortService.SubnetPortStore, "GetVpcSubnetPortByUID", func(_ *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
				require.Equal(t, sp.UID, uid)
				return recovered, nil
			})
			defer patches.Reset()
			patches.ApplyMethod(r.SubnetPortService, "AllocatePortFromSubnet", func(_ *subnetport.SubnetPortService, _ *model.VpcSubnet, _ bool, _ v1alpha1.IPAddressType, _ v1alpha1.StaticIPAllocationType, bindings []v1alpha1.PortAddressBinding, uid ...types.UID) (bool, error) {
				recoverPort(bindings, uid)
				return true, nil
			})
			patches.ApplyFunc(common.AllocateSubnetFromSubnetSet, func(_ client.Client, _ client.Reader, _ *v1alpha1.SubnetSet, _ servicecommon.VPCServiceProvider, _ servicecommon.SubnetServiceProvider, _ servicecommon.SubnetPortServiceProvider, _ v1alpha1.IPAddressType, _ v1alpha1.StaticIPAllocationType, bindings []v1alpha1.PortAddressBinding, uid ...types.UID) (string, v1alpha1.StaticIPAllocationType, *types.UID, *sync.RWMutex, error) {
				recoverPort(bindings, uid)
				return path, v1alpha1.StaticIPAllocationTypeIPv4, nil, nil, nil
			})
			patches.ApplyFunc(common.GetDefaultSubnetSetByNamespace, func(_ client.Client, _, _ string) (*v1alpha1.SubnetSet, error) { return set, nil })
			for retry := 0; retry < 2; retry++ {
				existing, _, actualPath, _, _, _, _, err := r.CheckAndGetSubnetPathForSubnetPort(context.Background(), sp)
				require.NoError(t, err)
				require.True(t, existing, "reconciliation must not release capacity for a recovered port")
				require.Equal(t, path, actualPath)
			}
			require.Equal(t, 1, calls)
		})
	}
}
