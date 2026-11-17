/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dnsrecord

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	ctlcommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	pkgmock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
)

func enablePerNamespaceProviders(t *testing.T) {
	t.Helper()
	previous := config.IsPerNamespaceProvidersSupported()
	config.SetPerNamespaceProvidersSupportedForTest(true)
	t.Cleanup(func() { config.SetPerNamespaceProvidersSupportedForTest(previous) })
}

func TestPredicateNamespaceBecameVPC(t *testing.T) {
	enablePerNamespaceProviders(t)
	legacy := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team"}}
	vpc := legacy.DeepCopy()
	vpc.Annotations = map[string]string{config.VPCNetworkConfigAnnotation: "network-config"}
	p := predicateNamespaceBecameVPC()
	require.True(t, p.Create(event.CreateEvent{Object: vpc}))
	require.False(t, p.Create(event.CreateEvent{Object: legacy}))
	require.True(t, p.Update(event.UpdateEvent{ObjectOld: legacy, ObjectNew: vpc}))
	require.False(t, p.Update(event.UpdateEvent{ObjectOld: vpc, ObjectNew: legacy}))
	metadataOnly := vpc.DeepCopy()
	metadataOnly.Labels = map[string]string{"unrelated": "value"}
	require.False(t, p.Update(event.UpdateEvent{ObjectOld: vpc, ObjectNew: metadataOnly}))
	conflicting := vpc.DeepCopy()
	conflicting.Annotations[config.T1DefaultConfigAnnotation] = "true"
	require.False(t, p.Update(event.UpdateEvent{ObjectOld: legacy, ObjectNew: conflicting}))
	require.False(t, p.Delete(event.DeleteEvent{Object: vpc}))
	require.False(t, p.Generic(event.GenericEvent{Object: vpc}))
	require.False(t, p.Create(event.CreateEvent{}))
	require.False(t, p.Update(event.UpdateEvent{}))
}

func TestPredicateNetworkInfoDNSStateChanged(t *testing.T) {
	oldNI := &v1alpha1.NetworkInfo{
		ObjectMeta:              metav1.ObjectMeta{Name: "network", Namespace: "team", Generation: 1},
		AllowedDNSRecordDomains: []string{"bb.com", "aa.com"},
	}
	p := predicateNetworkInfoDNSStateChanged()
	require.True(t, p.Create(event.CreateEvent{Object: oldNI}))
	require.True(t, p.Create(event.CreateEvent{Object: &v1alpha1.NetworkInfo{}}))
	for _, tc := range []struct {
		name    string
		domains []string
		changed bool
	}{
		{"unchanged", []string{"bb.com", "aa.com"}, false},
		{"reordered", []string{"aa.com", "bb.com"}, false},
		{"added", []string{"aa.com", "bb.com", "cc.com"}, true},
		{"removed", []string{"aa.com"}, true},
		{"replaced", []string{"aa.com", "cc.com"}, true},
		{"cleared", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updated := oldNI.DeepCopy()
			updated.AllowedDNSRecordDomains = tc.domains
			require.Equal(t, tc.changed, p.Update(event.UpdateEvent{ObjectOld: oldNI, ObjectNew: updated}))
			require.Equal(t, []string{"bb.com", "aa.com"}, oldNI.AllowedDNSRecordDomains)
			require.Equal(t, tc.domains, updated.AllowedDNSRecordDomains)
		})
	}
	ready := oldNI.DeepCopy()
	ready.VPCs = []v1alpha1.VPCState{{Name: "vpc"}}
	require.True(t, p.Update(event.UpdateEvent{ObjectOld: oldNI, ObjectNew: ready}))
	ipOnly := ready.DeepCopy()
	ipOnly.VPCs[0].DefaultSNATIP = "192.0.2.1"
	ipOnly.Labels = map[string]string{"unrelated": "value"}
	require.False(t, p.Update(event.UpdateEvent{ObjectOld: ready, ObjectNew: ipOnly}))
	require.False(t, p.Update(event.UpdateEvent{ObjectOld: &v1alpha1.NetworkInfo{}, ObjectNew: &v1alpha1.NetworkInfo{AllowedDNSRecordDomains: []string{}}}))
	require.False(t, p.Delete(event.DeleteEvent{Object: oldNI}))
	require.False(t, p.Generic(event.GenericEvent{Object: oldNI}))
	require.False(t, p.Create(event.CreateEvent{Object: &corev1.Namespace{}}))
	require.False(t, p.Update(event.UpdateEvent{}))
}

func TestNamespaceDNSRecordFanoutRetriesAndStaysScoped(t *testing.T) {
	for _, source := range []string{"namespace", "networkinfo"} {
		for _, priority := range []bool{false, true} {
			t.Run(source+map[bool]string{false: "/fifo", true: "/priority"}[priority], func(t *testing.T) {
				ctx := context.Background()
				rec := &v1alpha1.DNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "ready", Namespace: "team"}}
				failed := &v1alpha1.DNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "failed", Namespace: "team"},
					Status: v1alpha1.DNSRecordStatus{Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionFalse}}}}
				other := &v1alpha1.DNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "other"}}
				failList := false
				c := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(rec, failed, other).
					WithInterceptorFuncs(interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						_, isRecords := list.(*v1alpha1.DNSRecordList)
						require.True(t, isRecords, "fan-out must not list Namespaces or other resources")
						options := &client.ListOptions{}
						options.ApplyOptions(opts)
						require.Equal(t, "team", options.Namespace)
						if failList {
							return errors.New("temporary list failure")
						}
						return c.List(ctx, list, opts...)
					}}).Build()
				r := &DNSRecordReconciler{Client: c, usePriorityQueue: &priority}
				q := r.getQueue("namespace-retry", workqueue.DefaultTypedControllerRateLimiter[ctrl.Request]())
				t.Cleanup(q.ShutDown)
				var obj client.Object = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team"}}
				if source == "networkinfo" {
					obj = &v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "network", Namespace: "team"}}
				}
				want := []ctrl.Request{{NamespacedName: client.ObjectKeyFromObject(rec)}, {NamespacedName: client.ObjectKeyFromObject(failed)}}
				require.ElementsMatch(t, want, r.namespaceToDNSRecordRequests(ctx, obj))
				failList = true
				retry := r.namespaceToDNSRecordRequests(ctx, obj)
				require.Equal(t, []ctrl.Request{{NamespacedName: types.NamespacedName{Namespace: namespaceRetryNamespace, Name: "team"}}}, retry)
				_, err := r.Reconcile(ctx, retry[0])
				require.ErrorContains(t, err, "temporary list failure")
				require.Zero(t, q.Len())
				failList = false
				q.Add(want[0])
				result, err := r.Reconcile(ctx, retry[0])
				require.NoError(t, err)
				require.Equal(t, ResultNormal, result)
				require.Equal(t, 2, q.Len())
				var got []ctrl.Request
				for range 2 {
					req, shutdown := q.Get()
					require.False(t, shutdown)
					got = append(got, req)
					q.Done(req)
				}
				require.ElementsMatch(t, want, got)
			})
		}
	}
}

func TestNamespaceMigrationReconcilesPreviouslyFilteredDNSRecord(t *testing.T) {
	ctx := context.Background()
	ns := defaultNamespaceForTest()
	obj := &v1alpha1.DNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "existing", Namespace: ns.Name, Generation: 1},
		Spec: v1alpha1.DNSRecordSpec{DomainName: "example.com", RecordName: "api", RecordType: v1alpha1.DNSRecordTypeA, RecordValues: []string{"192.0.2.1"}}}
	c := fake.NewClientBuilder().WithScheme(setupScheme()).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(ns, obj).Build()
	svc, store := setupTestDNSRecordService(t, c)
	enablePerNamespaceProviders(t)
	r := &DNSRecordReconciler{Client: c, Service: svc,
		StatusUpdater: ctlcommon.NewStatusUpdater(c, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord")}
	require.False(t, ctlcommon.VPCNamespacePredicate(c).Create(event.CreateEvent{Object: obj}))
	require.Empty(t, store.ListDNSRecords())
	oldNS := ns.DeepCopy()
	ns.Annotations = map[string]string{config.VPCNetworkConfigAnnotation: "network-config"}
	require.NoError(t, c.Update(ctx, ns))
	require.True(t, predicateNamespaceBecameVPC().Update(event.UpdateEvent{ObjectOld: oldNS, ObjectNew: ns}))
	reqs := r.namespaceToDNSRecordRequests(ctx, ns)
	require.Len(t, reqs, 1)
	_, err := r.Reconcile(ctx, reqs[0])
	require.NoError(t, err)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
	require.Equal(t, int64(1), obj.Generation)
	require.True(t, meta.IsStatusConditionTrue(obj.Status.Conditions, "Ready"))
	require.Len(t, store.ListDNSRecords(), 1)
}

func TestNetworkInfoDomainUpdateRecoversRejectedDNSRecord(t *testing.T) {
	ctx := context.Background()
	obj := &v1alpha1.DNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "early", Namespace: "default", Generation: 1},
		Spec: v1alpha1.DNSRecordSpec{DomainName: "example.com", RecordName: "api", RecordType: v1alpha1.DNSRecordTypeA, RecordValues: []string{"192.0.2.1"}}}
	c := fake.NewClientBuilder().WithScheme(setupScheme()).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(defaultNamespaceForTest(), obj).Build()
	svc, store := setupTestDNSRecordService(t, c)
	nc := &v1alpha1.VPCNetworkConfiguration{}
	vpcSvc := &pkgmock.MockVPCServiceProvider{}
	vpcSvc.On("GetVPCNetworkConfigByNamespace", "default").Return(nc, nil)
	svc.VPCService = vpcSvc
	r := &DNSRecordReconciler{Client: c, Service: svc,
		StatusUpdater: ctlcommon.NewStatusUpdater(c, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord")}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
	result, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Equal(t, ResultNormal, result, "validation failure has no pending retry")
	require.NoError(t, c.Get(ctx, req.NamespacedName, obj))
	require.True(t, meta.IsStatusConditionFalse(obj.Status.Conditions, "Ready"))
	require.Empty(t, store.ListDNSRecords())
	// Upstream has attached the zone and published the newly permitted domain.
	nc.Spec.DNSZones = []string{"/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-1"}
	oldNI := &v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "network", Namespace: "default"}}
	newNI := oldNI.DeepCopy()
	newNI.AllowedDNSRecordDomains = []string{"example.com"}
	require.True(t, predicateNetworkInfoDNSStateChanged().Update(event.UpdateEvent{ObjectOld: oldNI, ObjectNew: newNI}))
	reqs := r.namespaceToDNSRecordRequests(ctx, newNI)
	require.Equal(t, []ctrl.Request{req}, reqs)
	_, err = r.Reconcile(ctx, reqs[0])
	require.NoError(t, err)
	require.NoError(t, c.Get(ctx, req.NamespacedName, obj))
	require.Equal(t, int64(1), obj.Generation)
	require.True(t, meta.IsStatusConditionTrue(obj.Status.Conditions, "Ready"))
	require.Equal(t, "api.example.com", obj.Status.FQDN)
	require.Len(t, store.ListDNSRecords(), 1)
}
