package common

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	pkgmock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestSubnetSetRequestedAddresses(t *testing.T) {
	for _, precreated := range []bool{false, true} {
		for _, tc := range []struct {
			name       string
			ips        []string
			want       int
			wantErr    string
			full       bool
			conflict   bool
			ineligible bool
			badCIDR    bool
		}{
			{name: "requested IPv4 selects second member", ips: []string{"192.0.2.10"}, want: 1},
			{name: "multiple IPv4 addresses", ips: []string{"192.0.2.10", "192.0.2.11"}, want: 1},
			{name: "no explicit IP", want: 0},
			{name: "MAC only", ips: []string{""}, want: 0},
			{name: "unmatched", ips: []string{"203.0.113.10"}, want: -1, wantErr: "contains all requested IPs"},
			{name: "split across members", ips: []string{"192.0.2.10", "198.51.100.10"}, want: -1, wantErr: "contains all requested IPs"},
			{name: "matching member full", ips: []string{"192.0.2.10"}, want: -1, full: true, wantErr: "cannot allocate requested IPs"},
			{name: "conflict", ips: []string{"192.0.2.10"}, want: -1, conflict: true, wantErr: "already allocated"},
			{name: "invalid IP", ips: []string{"invalid"}, want: -1, wantErr: "invalid requested IP"},
			{name: "duplicate IP", ips: []string{"192.0.2.10", "192.0.2.10"}, want: -1, wantErr: "duplicate requested IP"},
			{name: "bad CIDR", ips: []string{"192.0.2.10"}, want: -1, badCIDR: true, wantErr: "invalid Subnet CIDR"},
			{name: "default member removed from configuration", ips: []string{"192.0.2.10"}, want: -1, ineligible: true, wantErr: "no eligible Subnet"},
		} {
			if tc.ineligible && !precreated {
				continue
			}
			t.Run(fmt.Sprintf("precreated=%t/%s", precreated, tc.name), func(t *testing.T) {
				scheme := runtime.NewScheme()
				require.NoError(t, v1alpha1.AddToScheme(scheme))
				set := &v1alpha1.SubnetSet{ObjectMeta: metav1.ObjectMeta{Name: "set", Namespace: "vmware-system-net-staging", UID: types.UID(t.Name())}}
				names := []string{"unrelated", "matching"}
				if precreated {
					set.Spec.SubnetNames = &names
				}
				objects := []client.Object{set}
				var subnets []*model.VpcSubnet
				svc := &pkgmock.MockSubnetServiceProvider{}
				for index, name := range names {
					cr := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: set.Namespace}}
					objects = append(objects, cr)
					subnet := &model.VpcSubnet{Id: servicecommon.String(name), Path: servicecommon.String("/subnets/" + name), IpAddresses: []string{"198.51.100.0/24"}}
					if index == 1 {
						subnet.IpAddresses = []string{"192.0.2.0/24"}
						if tc.badCIDR {
							subnet.IpAddresses = []string{"invalid"}
						}
					}
					subnets = append(subnets, subnet)
					if precreated {
						svc.On("GetSubnetByCR", mock.MatchedBy(func(c *v1alpha1.Subnet) bool { return c.Name == name })).Return(subnet, nil).Maybe()
					}
				}
				if !precreated {
					svc.On("GetSubnetsByIndex", servicecommon.TagScopeSubnetSetCRUID, string(set.UID)).Return(subnets).Maybe()
				}
				vpc := &pkgmock.MockVPCServiceProvider{}
				if tc.ineligible {
					set.Labels = map[string]string{servicecommon.LabelDefaultNetwork: servicecommon.DefaultVMNetwork}
					vpc.On("GetVPCNetworkConfigByNamespace", set.Namespace).Return(&v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{Subnets: []v1alpha1.SharedSubnet{{Path: *subnets[0].Path, VMDefault: true}}}}, nil).Once()
				}
				var bindings []v1alpha1.PortAddressBinding
				for _, ip := range tc.ips {
					bindings = append(bindings, v1alpha1.PortAddressBinding{IPAddress: ip, MACAddress: "02:00:00:00:00:01"})
				}
				original := append([]v1alpha1.PortAddressBinding(nil), bindings...)
				ports := &pkgmock.MockSubnetPortServiceProvider{}
				candidate := tc.want
				if tc.full || tc.conflict {
					candidate = 1
				}
				if candidate >= 0 {
					var allocErr error
					if tc.conflict {
						allocErr = errors.New("requested IP is already allocated")
					}
					ports.On("AllocatePortFromSubnet", subnets[candidate], false, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeNone, bindings).Return(!tc.full && !tc.conflict, allocErr).Once()
				}
				api := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
				path, mode, uid, lock, err := AllocateSubnetFromSubnetSet(api, api, set, vpc, svc, ports, v1alpha1.IPAddressTypeIPv4, "", bindings)
				if lock != nil {
					RUnlockSubnetSet(*uid, lock)
				}
				if tc.wantErr != "" {
					require.ErrorContains(t, err, tc.wantErr)
					require.Empty(t, path)
				} else {
					require.NoError(t, err)
					require.Equal(t, *subnets[tc.want].Path, path)
					require.Equal(t, v1alpha1.StaticIPAllocationTypeNone, mode)
				}
				require.Equal(t, original, bindings)
				ports.AssertExpectations(t)
				svc.AssertNotCalled(t, "CreateOrUpdateSubnet", mock.Anything, mock.Anything, mock.Anything)
				vpc.AssertExpectations(t)
				if stored, ok := SubnetSetLocks.Load(set.UID); ok {
					mutex := stored.(*sync.RWMutex)
					require.True(t, mutex.TryLock(), "SubnetSet lock leaked")
					mutex.Unlock()
				}
			})
		}
	}
}
