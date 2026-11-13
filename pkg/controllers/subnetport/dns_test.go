/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/dns_services"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	subnetservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	portservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
)

const dnsSyncSubnetPath = "/orgs/org1/projects/project1/vpcs/vpc1/subnets/subnet1"

type dnsSyncMappingClient struct {
	projects.DnsAutoRecordConfigsClient
	enabled    bool
	zoneSuffix string
	fail       bool
	calls      int
}

func (c *dnsSyncMappingClient) List(_, _ string, _ *string, _, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.DnsAutoRecordConfigListResult, error) {
	c.calls++
	if c.fail {
		return model.DnsAutoRecordConfigListResult{}, errors.New("mapping unavailable")
	}
	if !c.enabled {
		return model.DnsAutoRecordConfigListResult{}, nil
	}
	return model.DnsAutoRecordConfigListResult{Results: []model.DnsAutoRecordConfig{
		{IpBlockPath: ptr.To("/infra/ip-blocks/v4"), ARecordZonePath: ptr.To("/orgs/org1/projects/project1/dns-services/dns1/zones/v4" + c.zoneSuffix)},
		{IpBlockPath: ptr.To("/infra/ip-blocks/v6"), ARecordZonePath: ptr.To("/orgs/org1/projects/project1/dns-services/dns1/zones/v6" + c.zoneSuffix)},
	}}, nil
}

type dnsSyncZoneClient struct {
	dns_services.ZonesClient
	calls int
	fail  bool
}

func (c *dnsSyncZoneClient) Get(_, _, _, zone string) (model.DnsZone, error) {
	c.calls++
	if c.fail {
		return model.DnsZone{}, errors.New("zone unavailable")
	}
	return model.DnsZone{DnsDomainName: ptr.To(zone + ".example.com")}, nil
}

type dnsSyncSubnetProvider struct {
	servicecommon.SubnetServiceProvider
	shared     bool
	fail       bool
	calls      int
	cacheCalls int
}

func (s *dnsSyncSubnetProvider) GetSubnetByPath(string, bool) (*model.VpcSubnet, error) {
	s.calls++
	if s.fail {
		return nil, errors.New("subnet unavailable")
	}
	if s.shared {
		return nil, subnetservice.ErrSubnetNotFoundInStore
	}
	return dnsSyncSubnet(), nil
}

func (s *dnsSyncSubnetProvider) GetNSXSubnetFromCacheOrAPI(_ string, force bool) (*model.VpcSubnet, error) {
	s.cacheCalls++
	if force {
		return nil, errors.New("unexpected forced API lookup")
	}
	return dnsSyncSubnet(), nil
}

func dnsSyncSubnet() *model.VpcSubnet {
	return &model.VpcSubnet{Path: ptr.To(dnsSyncSubnetPath), IpBlocks: []string{"/infra/ip-blocks/v4", "/infra/ip-blocks/v6"}}
}

func newDNSSyncReconciler(t *testing.T) (*SubnetPortReconciler, *dnsSyncMappingClient, *dnsSyncSubnetProvider) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	store := &portservice.SubnetPortStore{ResourceStore: servicecommon.ResourceStore{Indexer: cache.NewIndexer(
		func(obj interface{}) (string, error) { return *obj.(*model.VpcSubnetPort).Id, nil },
		cache.Indexers{
			servicecommon.TagScopeSubnetPortCRUID: func(obj interface{}) ([]string, error) { return []string{*obj.(*model.VpcSubnetPort).Id}, nil },
			servicecommon.TagScopePodUID:          func(interface{}) ([]string, error) { return nil, nil },
		},
	)}}
	var objects []client.Object
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("port%d", i)
		port := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: types.UID(name), OwnerReferences: dnsVMOwner()},
			Status: v1alpha1.SubnetPortStatus{NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{{IPAddress: "10.0.0.8"}, {IPAddress: "2001:db8::8"}},
			}}}
		nsxPort := &model.VpcSubnetPort{Id: &name, ParentPath: ptr.To(dnsSyncSubnetPath), DnsHostname: ptr.To("pod.ns")}
		if i == 2 {
			nsxPort.DnsHostname = nil
		}
		if i == 3 {
			port.Status.FQDNs = []string{"old.ns.old.example.com"}
		}
		require.NoError(t, store.Add(nsxPort))
		objects = append(objects, port)
	}
	mappings := &dnsSyncMappingClient{}
	subnets := &dnsSyncSubnetProvider{}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.SubnetPort{}).WithObjects(objects...).Build()
	return &SubnetPortReconciler{Client: k8s, APIReader: k8s, SubnetService: subnets,
		SubnetPortService: &portservice.SubnetPortService{SubnetPortStore: store, Service: servicecommon.Service{NSXClient: &nsx.Client{
			DnsAutoRecordConfigsClient: mappings, DnsZoneClient: &dnsSyncZoneClient{},
		}}}}, mappings, subnets
}

func enableDNSSync(t *testing.T) {
	t.Helper()
	patch := gomonkey.ApplyFunc(nsx.DNSAutoRecordFeatureEnabled, func(*nsx.Client, *config.NSXOperatorConfig) bool { return true })
	t.Cleanup(patch.Reset)
}

func TestSyncSubnetPortDNSStatus(t *testing.T) {
	enableDNSSync(t)
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%v", shared), func(t *testing.T) {
			r, mappings, subnets := newDNSSyncReconciler(t)
			subnets.shared = shared
			ctx := context.Background()
			require.NoError(t, r.syncSubnetPortDNSStatus(ctx))
			port := &v1alpha1.SubnetPort{}
			require.NoError(t, r.Client.Get(ctx, client.ObjectKey{Namespace: "ns", Name: "port0"}, port))
			require.Empty(t, port.Status.FQDNs)
			// A newly added mapping must replace the cached empty snapshot even before TTL expiry.
			mappings.enabled = true
			subnets.calls, subnets.cacheCalls = 0, 0
			require.NoError(t, r.syncSubnetPortDNSStatus(ctx))
			require.Equal(t, 2, mappings.calls, "one List per project per sync, not per port")
			require.Equal(t, 1, subnets.calls, "one subnet lookup per sync")
			if shared {
				require.Equal(t, 1, subnets.cacheCalls)
			}
			for i := 0; i < 4; i++ {
				require.NoError(t, r.Client.Get(ctx, client.ObjectKey{Namespace: "ns", Name: fmt.Sprintf("port%d", i)}, port))
				switch i {
				case 0, 1:
					require.Equal(t, []string{"pod.ns.v4.example.com", "pod.ns.v6.example.com"}, port.Status.FQDNs)
				case 2:
					require.Empty(t, port.Status.FQDNs, "legacy port has no NSX hostname")
				case 3:
					require.Equal(t, []string{"pod.ns.v4.example.com", "pod.ns.v6.example.com"}, port.Status.FQDNs)
				}
			}
			// Stable mappings require no per-port API reads or status writes.
			originalClient, originalReader := r.Client, r.APIReader
			counted := interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
				Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
					t.Fatal("unchanged FQDN must not require an API Get")
					return nil
				},
				SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
					t.Fatal("unchanged FQDN must not write status")
					return nil
				},
			})
			r.Client, r.APIReader = counted, counted
			require.NoError(t, r.syncSubnetPortDNSStatus(ctx))
			r.Client, r.APIReader = originalClient, originalReader
			mappings.zoneSuffix = "-new"
			require.NoError(t, r.syncSubnetPortDNSStatus(ctx))
			updated := &v1alpha1.SubnetPort{}
			require.NoError(t, r.Client.Get(ctx, client.ObjectKey{Namespace: "ns", Name: "port0"}, updated))
			require.Equal(t, []string{"pod.ns.v4-new.example.com", "pod.ns.v6-new.example.com"}, updated.Status.FQDNs)
			mappings.enabled = false
			require.NoError(t, r.syncSubnetPortDNSStatus(ctx))
			require.Equal(t, 5, mappings.calls)
			for _, name := range []string{"port0", "port1", "port3"} {
				cleared := &v1alpha1.SubnetPort{}
				require.NoError(t, r.Client.Get(ctx, client.ObjectKey{Namespace: "ns", Name: name}, cleared))
				require.Empty(t, cleared.Status.FQDNs, "deleting config clears old names")
			}
		})
	}
}

func TestSyncSubnetPortDNSFailures(t *testing.T) {
	enableDNSSync(t)
	ctx := context.Background()
	t.Run("failed mapping refresh is retried next cycle", func(t *testing.T) {
		r, mappings, subnets := newDNSSyncReconciler(t)
		mappings.enabled, mappings.fail = true, true
		require.ErrorContains(t, r.syncSubnetPortDNSStatus(ctx), "mapping unavailable")
		require.Equal(t, 1, mappings.calls)
		require.Equal(t, 1, subnets.calls, "ports and subnets are collected before refreshing projects")
		mappings.fail = false
		require.NoError(t, r.syncSubnetPortDNSStatus(ctx))
		require.Equal(t, 2, mappings.calls)
	})
	t.Run("failed subnet lookup is shared by ports and retried", func(t *testing.T) {
		r, mappings, subnets := newDNSSyncReconciler(t)
		mappings.enabled, subnets.fail = true, true
		require.ErrorContains(t, r.syncSubnetPortDNSStatus(ctx), "subnet unavailable")
		require.Equal(t, 1, subnets.calls)
		subnets.fail = false
		require.NoError(t, r.syncSubnetPortDNSStatus(ctx))
	})
	t.Run("list failure", func(t *testing.T) {
		r, _, _ := newDNSSyncReconciler(t)
		r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
			List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
				return errors.New("list failed")
			},
		})
		require.ErrorContains(t, r.syncSubnetPortDNSStatus(ctx), "list failed")
	})
	t.Run("cancelled context", func(t *testing.T) {
		r, _, _ := newDNSSyncReconciler(t)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		require.Error(t, r.syncSubnetPortDNSStatus(cancelled))
		s := &subnetPortDNSSync{reconciler: r}
		require.True(t, s.NeedLeaderElection())
		require.NoError(t, s.Start(cancelled))
	})
	t.Run("worker shuts down with manager", func(t *testing.T) {
		r, _, _ := newDNSSyncReconciler(t)
		workerCtx, cancel := context.WithCancel(ctx)
		r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
			List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
				cancel()
				return errors.New("stop worker")
			},
		})
		require.NoError(t, (&subnetPortDNSSync{reconciler: r}).Start(workerCtx))
	})
}

func TestUpdateSubnetPortDNSStatus(t *testing.T) {
	enableDNSSync(t)
	ctx := context.Background()
	t.Run("retry conflict without overwriting other status", func(t *testing.T) {
		r, mappings, _ := newDNSSyncReconciler(t)
		mappings.enabled = true
		candidate := &v1alpha1.SubnetPort{}
		require.NoError(t, r.SubnetPortService.RefreshDNSAutoRecordConfig("org1", "project1"))
		require.NoError(t, r.Client.Get(ctx, client.ObjectKey{Namespace: "ns", Name: "port0"}, candidate))
		patches := 0
		r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, resource string, port client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				patches++
				if patches == 1 {
					return apierrors.NewConflict(schema.GroupResource{Resource: "subnetports"}, port.GetName(), errors.New("conflict"))
				}
				return c.SubResource(resource).Patch(ctx, port, patch, opts...)
			},
		})
		require.NoError(t, r.updateSubnetPortDNSStatus(ctx, candidate, dnsSyncSubnet(), &model.VpcSubnetPort{DnsHostname: ptr.To("pod.ns")}))
		require.Equal(t, 2, patches)
	})
	for _, name := range []string{"deleted", "replaced", "completed", "lookup failure", "addressless port"} {
		t.Run(name, func(t *testing.T) {
			r, mappings, _ := newDNSSyncReconciler(t)
			mappings.enabled = true
			candidate := &v1alpha1.SubnetPort{}
			require.NoError(t, r.SubnetPortService.RefreshDNSAutoRecordConfig("org1", "project1"))
			require.NoError(t, r.Client.Get(ctx, client.ObjectKey{Namespace: "ns", Name: "port0"}, candidate))
			switch name {
			case "deleted":
				require.NoError(t, r.Client.Delete(ctx, candidate))
			case "replaced":
				candidate.UID = "old-uid"
			case "completed":
				candidate.Status.FQDNs = []string{"already.example.com"}
				require.NoError(t, r.Client.Status().Update(ctx, candidate))
			case "lookup failure":
				r.APIReader = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
					Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
						return errors.New("get failed")
					},
				})
			case "addressless port":
				candidate.Status.NetworkInterfaceConfig.IPAddresses = nil
				require.NoError(t, r.Client.Status().Update(ctx, candidate))
			}
			err := r.updateSubnetPortDNSStatus(ctx, candidate, dnsSyncSubnet(), &model.VpcSubnetPort{DnsHostname: ptr.To("pod.ns")})
			if name == "lookup failure" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestUpdateSubnetPortDNSStatusConcurrentChange(t *testing.T) {
	for _, invalidIP := range []bool{false, true} {
		t.Run(fmt.Sprintf("invalidIP=%v", invalidIP), func(t *testing.T) {
			r, mappings, _ := newDNSSyncReconciler(t)
			mappings.enabled = true
			candidate := &v1alpha1.SubnetPort{}
			require.NoError(t, r.SubnetPortService.RefreshDNSAutoRecordConfig("org1", "project1"))
			require.NoError(t, r.Client.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "port0"}, candidate))
			r.APIReader = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if err := c.Get(ctx, key, obj, opts...); err != nil {
						return err
					}
					port := obj.(*v1alpha1.SubnetPort)
					if invalidIP {
						port.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress = "bad"
					} else {
						port.Status.FQDNs = []string{"pod.ns.v4.example.com", "pod.ns.v6.example.com"}
					}
					return nil
				},
			})
			err := r.updateSubnetPortDNSStatus(context.Background(), candidate, dnsSyncSubnet(), &model.VpcSubnetPort{DnsHostname: ptr.To("pod.ns")})
			require.NoError(t, err, "FQDNs are determined by subnet IPBlock paths, not CR IP addresses")
		})
	}
}

func TestSyncDHCPSubnetPortDNSStatus(t *testing.T) {
	enableDNSSync(t)
	r, mappings, _ := newDNSSyncReconciler(t)
	ctx := context.Background()
	port := &v1alpha1.SubnetPort{}
	key := client.ObjectKey{Namespace: "ns", Name: "port0"}
	require.NoError(t, r.Client.Get(ctx, key, port))
	port.Spec.InterfaceIPType = v1alpha1.IPAddressTypeIPv4IPv6
	require.NoError(t, r.Client.Update(ctx, port))
	port.Status.NetworkInterfaceConfig.IPAddresses = nil
	port.Status.FQDNs = []string{"old.example.com"}
	require.NoError(t, r.Client.Status().Update(ctx, port))
	// Even a DHCP port with no CR IP must clear old names after config deletion.
	require.NoError(t, r.syncSubnetPortDNSStatus(ctx))
	require.NoError(t, r.Client.Get(ctx, key, port))
	require.Empty(t, port.Status.FQDNs)
	mappings.enabled = true
	require.NoError(t, r.syncSubnetPortDNSStatus(ctx))
	require.NoError(t, r.Client.Get(ctx, key, port))
	require.Equal(t, []string{"pod.ns.v4.example.com", "pod.ns.v6.example.com"}, port.Status.FQDNs)
	// A refresh error preserves the known status rather than treating it as deletion.
	mappings.fail = true
	require.Error(t, r.syncSubnetPortDNSStatus(ctx))
	require.NoError(t, r.Client.Get(ctx, key, port))
	require.Equal(t, []string{"pod.ns.v4.example.com", "pod.ns.v6.example.com"}, port.Status.FQDNs)
}

func TestSyncSubnetPortDNSFeatureDisabled(t *testing.T) {
	r, mappings, _ := newDNSSyncReconciler(t)
	r.SubnetPortService.NSXClient = nil
	require.NoError(t, r.syncSubnetPortDNSStatus(context.Background()))
	require.Zero(t, mappings.calls)
}

func TestSyncSubnetPortDNSInvalidParent(t *testing.T) {
	enableDNSSync(t)
	r, _, _ := newDNSSyncReconciler(t)
	stored := r.SubnetPortService.SubnetPortStore.GetByKey("port0")
	copy := *stored
	copy.ParentPath = ptr.To("invalid")
	require.NoError(t, r.SubnetPortService.SubnetPortStore.Update(&copy))
	require.Error(t, r.syncSubnetPortDNSStatus(context.Background()))
}

func dnsVMOwner() []metav1.OwnerReference {
	return []metav1.OwnerReference{{APIVersion: "vmoperator.vmware.com/v1alpha1", Kind: "VirtualMachine", Name: "vm-name"}}
}

func TestSubnetPortDNSHostname(t *testing.T) {
	port := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{Namespace: "Team-A", OwnerReferences: []metav1.OwnerReference{
		{APIVersion: "v1", Kind: "Pod", Name: "ignored"},
		{APIVersion: "vmoperator.vmware.com/v1alpha4", Kind: "VirtualMachine", Name: "VM-Name"},
	}}}
	port.Annotations = map[string]string{servicecommon.AnnotationAttachmentRef: "virtualmachine/VM-Name/eth1"}
	require.Equal(t, "vm-name.eth1.team-a", subnetPortDNSHostname(port))

}

func TestSubnetPortDNSExcludesNonVMPorts(t *testing.T) {
	for _, tc := range []struct {
		name string
		port *v1alpha1.SubnetPort
	}{
		{name: "nil"},
		{name: "no owner", port: &v1alpha1.SubnetPort{}},
		{name: "Pod owner", port: &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod"}}}}},
		{name: "different API group", port: &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{APIVersion: "other/v1", Kind: "VirtualMachine"}}}}},
		{name: "image fetcher", port: &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{OwnerReferences: dnsVMOwner(), Labels: map[string]string{servicecommon.LabelImageFetcher: "true"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hostname := subnetPortDNSHostname(tc.port)
			require.Empty(t, hostname)
		})
	}
}

func TestSyncDNSColdCacheClearsOldFQDN(t *testing.T) {
	enableDNSSync(t)
	r, mappings, _ := newDNSSyncReconciler(t)
	// No cache entry or NSX config exists for the project. The work list must
	// still include its existing CRs, rather than being derived from mappings.
	r.SubnetPortService.PruneDNSCache(nil)
	require.NoError(t, r.syncSubnetPortDNSStatus(context.Background()))
	port := &v1alpha1.SubnetPort{}
	require.NoError(t, r.Client.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "port3"}, port))
	require.Empty(t, port.Status.FQDNs)
	require.Equal(t, 1, mappings.calls)
}

func TestSyncDNSWarmsDomainsBeforeUpdatingCRs(t *testing.T) {
	enableDNSSync(t)
	r, mappings, _ := newDNSSyncReconciler(t)
	mappings.enabled = true
	zones := r.SubnetPortService.NSXClient.DnsZoneClient.(*dnsSyncZoneClient)
	zones.fail = true
	require.ErrorContains(t, r.syncSubnetPortDNSStatus(context.Background()), "zone unavailable")
	port := &v1alpha1.SubnetPort{}
	require.NoError(t, r.Client.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "port3"}, port))
	require.Equal(t, []string{"old.ns.old.example.com"}, port.Status.FQDNs)
	zones.fail, zones.calls = false, 0
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, c client.Client, resource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			require.Equal(t, 2, zones.calls, "both domains must be cached before the first CR patch")
			return c.SubResource(resource).Patch(ctx, obj, patch, opts...)
		},
	})
	require.NoError(t, r.syncSubnetPortDNSStatus(context.Background()))
	require.Equal(t, 2, zones.calls, "DNS zone lookups are shared across all ports")
}

func TestSyncSubnetPortDNSExcludesNonVMPorts(t *testing.T) {
	enableDNSSync(t)
	r, mappings, subnets := newDNSSyncReconciler(t)
	ports := &v1alpha1.SubnetPortList{}
	require.NoError(t, r.Client.List(context.Background(), ports))
	for i := range ports.Items {
		port := &ports.Items[i]
		if i%2 == 0 {
			port.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod"}}
		} else {
			port.Labels = map[string]string{servicecommon.LabelImageFetcher: "true"}
		}
		require.NoError(t, r.Client.Update(context.Background(), port))
	}
	require.NoError(t, r.syncSubnetPortDNSStatus(context.Background()))
	require.Zero(t, mappings.calls)
	require.Zero(t, subnets.calls)
}

func TestPopulateSubnetPortFQDNs(t *testing.T) {
	enableDNSSync(t)
	r, mappings, _ := newDNSSyncReconciler(t)
	mappings.enabled = true
	require.NoError(t, r.SubnetPortService.RefreshDNSAutoRecordConfig("org1", "project1"))
	port := &v1alpha1.SubnetPort{}
	key := client.ObjectKey{Namespace: "ns", Name: "port0"}
	require.NoError(t, r.Client.Get(context.Background(), key, port))
	require.NoError(t, r.populateSubnetPortFQDNs(port, dnsSyncSubnet()))
	require.Equal(t, []string{"pod.ns.v4.example.com", "pod.ns.v6.example.com"}, port.Status.FQDNs)
	stored := &v1alpha1.SubnetPort{}
	require.NoError(t, r.Client.Get(context.Background(), key, stored))
	require.Empty(t, stored.Status.FQDNs, "normal reconcile persists FQDNs with other status fields")
	port.OwnerReferences = nil
	require.NoError(t, r.populateSubnetPortFQDNs(port, dnsSyncSubnet()))
}

func TestUpdateSubnetPortCRFQDNs(t *testing.T) {
	r, mappings, _ := newDNSSyncReconciler(t)
	port := &v1alpha1.SubnetPort{Status: v1alpha1.SubnetPortStatus{FQDNs: []string{"existing.example.com"}}}
	nsxPort := &model.VpcSubnetPort{DnsHostname: ptr.To("vm.eth0.ns")}
	subnet := dnsSyncSubnet()
	require.ErrorContains(t, r.updateSubnetPortCRFQDNs(port, subnet, nsxPort), "has not been loaded")
	require.Equal(t, []string{"existing.example.com"}, port.Status.FQDNs)
	require.NoError(t, r.updateSubnetPortCRFQDNs(nil, subnet, nsxPort))
	for _, missing := range []*model.VpcSubnetPort{nil, {}, {DnsHostname: ptr.To("")}, {DnsHostname: ptr.To(" ")}} {
		require.NoError(t, r.updateSubnetPortCRFQDNs(port, subnet, missing))
		require.Equal(t, []string{"existing.example.com"}, port.Status.FQDNs)
	}
	for _, missing := range []*model.VpcSubnet{nil, {}} {
		require.NoError(t, r.updateSubnetPortCRFQDNs(port, missing, nsxPort))
		require.Equal(t, []string{"existing.example.com"}, port.Status.FQDNs)
	}
	mappings.enabled = true
	require.NoError(t, r.SubnetPortService.RefreshDNSAutoRecordConfig("org1", "project1"))
	require.NoError(t, r.updateSubnetPortCRFQDNs(port, subnet, nsxPort))
	require.Equal(t, []string{"vm.eth0.ns.v4.example.com", "vm.eth0.ns.v6.example.com"}, port.Status.FQDNs)
	mappings.enabled = false
	require.NoError(t, r.SubnetPortService.RefreshDNSAutoRecordConfig("org1", "project1"))
	require.NoError(t, r.updateSubnetPortCRFQDNs(port, subnet, nsxPort))
	require.Empty(t, port.Status.FQDNs, "removing the config clears the CR status")
}

func TestNormalReconcilePersistsDNSFQDNStatus(t *testing.T) {
	enableDNSSync(t)
	r, mappings, _ := newDNSSyncReconciler(t)
	mappings.enabled = true
	require.NoError(t, r.SubnetPortService.RefreshDNSAutoRecordConfig("org1", "project1"))
	ctx := context.Background()
	key := client.ObjectKey{Namespace: "ns", Name: "port0"}
	port := &v1alpha1.SubnetPort{}
	require.NoError(t, r.Client.Get(ctx, key, port))
	require.NoError(t, r.populateSubnetPortFQDNs(port, dnsSyncSubnet()))
	setSubnetPortReadyStatusTrue(r.Client, ctx, port, metav1.Now())
	require.NoError(t, r.Client.Get(ctx, key, port))
	require.Equal(t, []string{"pod.ns.v4.example.com", "pod.ns.v6.example.com"}, port.Status.FQDNs)
	// Changing only FQDNs must persist even when Ready is unchanged.
	port.Status.FQDNs = nil
	setSubnetPortReadyStatusTrue(r.Client, ctx, port, metav1.Now())
	require.NoError(t, r.Client.Get(ctx, key, port))
	require.Empty(t, port.Status.FQDNs)
	require.NotEmpty(t, port.Status.Conditions)
}

func TestSyncDNSSkipsPortsWithoutNSXHostname(t *testing.T) {
	enableDNSSync(t)
	for _, hostname := range []*string{nil, ptr.To(""), ptr.To(" ")} {
		r, _, _ := newDNSSyncReconciler(t)
		ctx := context.Background()
		port := &v1alpha1.SubnetPort{}
		key := client.ObjectKey{Namespace: "ns", Name: "port0"}
		require.NoError(t, r.Client.Get(ctx, key, port))
		port.Status.FQDNs = []string{"stale.example.com"}
		require.NoError(t, r.Client.Status().Update(ctx, port))
		stored := *r.SubnetPortService.SubnetPortStore.GetByKey("port0")
		stored.DnsHostname = hostname
		require.NoError(t, r.SubnetPortService.SubnetPortStore.Update(&stored))
		require.NoError(t, r.syncSubnetPortDNSStatus(ctx))
		require.NoError(t, r.Client.Get(ctx, key, port))
		require.Equal(t, []string{"stale.example.com"}, port.Status.FQDNs)
	}
}

func TestGeneratedSubnetPortDNSHostname(t *testing.T) {
	for _, tc := range []struct {
		name, ref, want string
		pod             bool
	}{
		{name: "new VM", ref: "virtualmachine/vm-name/eth0", want: "vm-name.eth0.ns"},
		{name: "lowercase hostname", ref: "virtualmachine/VM-NAME/ETH0", want: "vm-name.eth0.ns"},
		{name: "missing attachment reference"},
		{name: "invalid attachment reference", ref: "virtualmachine/vm-name"},
		{name: "missing VM name", ref: "virtualmachine//eth0"},
		{name: "missing interface name", ref: "virtualmachine/vm-name/"},
		{name: "Pod", pod: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{Name: "new", Namespace: "ns", OwnerReferences: dnsVMOwner()}}
			if tc.ref != "" {
				port.Annotations = map[string]string{servicecommon.AnnotationAttachmentRef: tc.ref}
			}
			if tc.pod {
				port.OwnerReferences = nil
			}
			require.Equal(t, tc.want, subnetPortDNSHostname(port))
		})
	}
}
