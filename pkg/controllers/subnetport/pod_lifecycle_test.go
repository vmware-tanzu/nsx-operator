package subnetport

import (
	"context"
	"fmt"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	podcontroller "github.com/vmware-tanzu/nsx-operator/pkg/controllers/pod"
	"github.com/vmware-tanzu/nsx-operator/pkg/mock"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	serviceport "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
)

func TestPodPortCleanupPreservesStatefulSetPorts(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		for _, operation := range []string{"subnetport-gc", "pod-gc", "subnetport-delete", "pod-delete"} {
			t.Run(fmt.Sprintf("%s/enabled=%v", operation, enabled), func(t *testing.T) {
				ctx := context.Background()
				scheme := runtime.NewScheme()
				require.NoError(t, v1.AddToScheme(scheme))
				require.NoError(t, v1alpha1.AddToScheme(scheme))
				api := fake.NewClientBuilder().WithScheme(scheme).Build()
				cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{VpcWcpEnhance: &enabled}}
				backend := &podRestorePortsClient{ports: map[string]model.VpcSubnetPort{}}
				svc := &serviceport.SubnetPortService{Service: servicecommon.Service{
					Client: api, NSXConfig: cfg, NSXClient: &nsx.Client{PortClient: backend},
				}, SubnetPortStore: newPodRestorePortStore()}
				for _, id := range []string{"ordinary", "stateful"} {
					parent := "/orgs/default/projects/default/vpcs/vpc/subnets/subnet"
					port := &model.VpcSubnetPort{Id: servicecommon.String(id), DisplayName: servicecommon.String(id),
						ParentPath: &parent, Path: servicecommon.String(parent + "/ports/" + id), Tags: []model.Tag{
							{Scope: servicecommon.String(servicecommon.TagScopeNamespace), Tag: servicecommon.String("ns")},
							{Scope: servicecommon.String(servicecommon.TagScopePodName), Tag: servicecommon.String("gone")},
							{Scope: servicecommon.String(servicecommon.TagScopePodUID), Tag: servicecommon.String(id + "-pod")},
							{Scope: servicecommon.String(servicecommon.TagScopeSubnetPortCRName), Tag: servicecommon.String("gone")},
							{Scope: servicecommon.String(servicecommon.TagScopeSubnetPortCRUID), Tag: servicecommon.String(id + "-cr")},
						}}
					if id == "stateful" {
						port.Tags = append(port.Tags, model.Tag{Scope: servicecommon.String(servicecommon.TagScopeStatefulSetUID), Tag: servicecommon.String("sts-uid")})
					}
					require.NoError(t, svc.SubnetPortStore.Apply(port))
					backend.ports[id] = *port
				}
				patches := gomonkey.ApplyFunc((*nsx.Client).NSXCheckVersion, func(*nsx.Client, int) bool { return true })
				defer patches.Reset()
				r := &SubnetPortReconciler{Client: api, SubnetPortService: svc, IpAddressAllocationService: &mock.MockIPAddressAllocationProvider{}, Recorder: fakeRecorder{}}
				r.StatusUpdater = common.NewStatusUpdater(api, cfg, r.Recorder, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")
				p := &podcontroller.PodReconciler{Client: api, SubnetPortService: svc, Recorder: fakeRecorder{}}
				p.StatusUpdater = common.NewStatusUpdater(api, cfg, p.Recorder, "Pod", "SubnetPort", "Pod")
				var err error
				switch operation {
				case "subnetport-gc":
					err = r.CollectGarbage(ctx)
				case "pod-gc":
					err = p.CollectGarbage(ctx)
				case "subnetport-delete":
					err = r.deleteSubnetPortByName(ctx, "ns", "gone")
				case "pod-delete":
					_, err = p.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "gone"}})
				}
				require.NoError(t, err)
				require.NotContains(t, backend.ports, "ordinary")
				require.Nil(t, svc.SubnetPortStore.GetByKey("ordinary"))
				if enabled {
					require.Contains(t, backend.ports, "stateful")
					require.NotNil(t, svc.SubnetPortStore.GetByKey("stateful"))
				} else {
					require.Empty(t, backend.ports)
					require.Nil(t, svc.SubnetPortStore.GetByKey("stateful"))
				}
			})
		}
	}
}

func TestStatefulSetReplacementReusesOriginalSubnetOnlyForCurrentOwner(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-0", Namespace: "ns", UID: "new-pod", OwnerReferences: []metav1.OwnerReference{
		{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "web", UID: "sts-uid", Controller: servicecommon.Bool(true)},
	}}}
	api := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()
	svc := &serviceport.SubnetPortService{Service: servicecommon.Service{Client: api, NSXClient: &nsx.Client{},
		NSXConfig: &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{VpcWcpEnhance: servicecommon.Bool(true)}},
	}, SubnetPortStore: newPodRestorePortStore()}
	path := "/orgs/default/projects/default/vpcs/vpc/subnets/original"
	port := &model.VpcSubnetPort{Id: servicecommon.String("old-port"), ParentPath: &path, Tags: []model.Tag{
		{Scope: servicecommon.String(servicecommon.TagScopeStatefulSetUID), Tag: servicecommon.String("sts-uid")},
		{Scope: servicecommon.String(servicecommon.TagScopePodName), Tag: servicecommon.String(pod.Name)},
	}}
	require.NoError(t, svc.SubnetPortStore.Apply(port))
	patches := gomonkey.ApplyFunc((*nsx.Client).NSXCheckVersion, func(*nsx.Client, int) bool { return true })
	defer patches.Reset()
	r := &SubnetPortReconciler{Client: api, SubnetPortService: svc}
	sp := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{Name: "port", Namespace: pod.Namespace, UID: "new-cr", OwnerReferences: []metav1.OwnerReference{
		{Kind: "Pod", Name: pod.Name, UID: "old-pod"},
	}}}
	exists, _, gotPath, _, _, _, _, err := r.CheckAndGetSubnetPathForSubnetPort(ctx, sp)
	require.ErrorContains(t, err, "UID changed")
	require.False(t, exists)
	require.Empty(t, gotPath)
	sp.OwnerReferences[0].UID = pod.UID
	exists, _, gotPath, _, _, _, _, err = r.CheckAndGetSubnetPathForSubnetPort(ctx, sp)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, path, gotPath)
}

func TestTerminatingPodDoesNotReconcileSubnetPort(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	now := metav1.Now()
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "pod", Namespace: "ns", UID: "pod-uid",
		DeletionTimestamp: &now, Finalizers: []string{"test-finalizer"},
	}}
	sp := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{
		Name: "port", Namespace: pod.Namespace, UID: "cr-uid",
		OwnerReferences: []metav1.OwnerReference{{Kind: "Pod", Name: pod.Name, UID: pod.UID}},
	}}
	api := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod, sp).Build()
	require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(sp), sp))
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}
	// No backend services: a terminating owner must be checked before allocation.
	r := &SubnetPortReconciler{Client: api, APIReader: api}
	r.StatusUpdater = common.NewStatusUpdater(api, cfg, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")
	for _, restoreMode := range []bool{false, true} {
		r.restoreMode = restoreMode
		result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(sp)})
		require.NoError(t, err)
		require.Equal(t, common.ResultNormal, result)
	}
	after := &v1alpha1.SubnetPort{}
	require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(sp), after))
	require.Equal(t, sp, after)
}

func TestPodRestoreListRequiresRealizedCurrentOwner(t *testing.T) {
	for _, tc := range []struct {
		name, ownerUID                             string
		realized, terminal, wantRestore, wantError bool
		terminating                                bool
		v2mode                                     bool
		staticAlloc                                v1alpha1.StaticIPAllocationType
		savedState, gatewayOnly                    bool
		readyMismatch                              bool
	}{
		{name: "realized", v2mode: true, ownerUID: "pod-uid", realized: true, wantRestore: true},
		{name: "legacy-mac-v2-disabled", ownerUID: "pod-uid", realized: true},
		{name: "saved-cr-state-v2-disabled", ownerUID: "pod-uid", savedState: true},
		{name: "ready-mismatch-v2-disabled", ownerUID: "pod-uid", realized: true, readyMismatch: true},
		{name: "not-realized", ownerUID: "pod-uid"},
		{name: "not-realized-v2-unset", ownerUID: "pod-uid", v2mode: true},
		{name: "not-realized-v2-none", ownerUID: "pod-uid", v2mode: true, staticAlloc: v1alpha1.StaticIPAllocationTypeNone},
		{name: "not-realized-v2-static", ownerUID: "pod-uid", v2mode: true, staticAlloc: v1alpha1.StaticIPAllocationTypeIPv4},
		{name: "saved-cr-state-unset", ownerUID: "pod-uid", v2mode: true, savedState: true, wantRestore: true},
		{name: "saved-cr-state-none", ownerUID: "pod-uid", v2mode: true, staticAlloc: v1alpha1.StaticIPAllocationTypeNone, savedState: true, wantRestore: true},
		{name: "saved-cr-state-static", ownerUID: "pod-uid", v2mode: true, staticAlloc: v1alpha1.StaticIPAllocationTypeIPv4, savedState: true, wantRestore: true},
		{name: "gateway-only", ownerUID: "pod-uid", v2mode: true, gatewayOnly: true},
		{name: "terminal", v2mode: true, ownerUID: "pod-uid", realized: true, terminal: true},
		{name: "terminal-v2", ownerUID: "pod-uid", v2mode: true, terminal: true},
		{name: "terminating", v2mode: true, ownerUID: "pod-uid", realized: true, terminating: true},
		{name: "terminating-v2", ownerUID: "pod-uid", v2mode: true, savedState: true, terminating: true},
		{name: "terminating-ready-mismatch", v2mode: true, ownerUID: "pod-uid", realized: true, terminating: true, readyMismatch: true},
		{name: "replacement-owner", v2mode: true, ownerUID: "old-pod", realized: true, wantError: true},
		{name: "ready-but-attachment-mismatch", v2mode: true, ownerUID: "pod-uid", realized: true, wantRestore: true, readyMismatch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, v1.AddToScheme(scheme))
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "ns", UID: "pod-uid"}}
			if tc.realized {
				pod.Annotations = map[string]string{servicecommon.AnnotationPodMAC: "02:50:56:00:00:01"}
			}
			if tc.terminal {
				pod.Status.Phase = v1.PodSucceeded
			}
			if tc.terminating {
				now := metav1.Now()
				pod.DeletionTimestamp = &now
				pod.Finalizers = []string{"test-finalizer"}
			}
			sp := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{Name: "port", Namespace: "ns", UID: "cr-uid", OwnerReferences: []metav1.OwnerReference{
				{Kind: "Pod", Name: pod.Name, UID: types.UID(tc.ownerUID)},
			}}}
			sp.Spec.StaticIPAllocationType = tc.staticAlloc
			if tc.savedState {
				sp.Status.NetworkInterfaceConfig = v1alpha1.NetworkInterfaceConfig{
					MACAddress:  "02:50:56:00:00:01",
					IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{{IPAddress: "10.0.0.10/24"}},
				}
			}
			if tc.gatewayOnly {
				sp.Status.NetworkInterfaceConfig.IPAddresses = []v1alpha1.NetworkInterfaceIPAddress{{Gateway: "10.0.0.1"}}
			}

			store := newPodRestorePortStore()
			if tc.readyMismatch {
				sp.Status.Conditions = []v1alpha1.Condition{{Type: v1alpha1.Ready, Status: v1.ConditionTrue}}
				sp.Status.Attachment.ID = "old-attachment"
				nsxPort := &model.VpcSubnetPort{
					Id:         servicecommon.String("nsx-port-1"),
					Attachment: &model.PortAttachment{Id: servicecommon.String("new-attachment")},
					ParentPath: servicecommon.String("/infra/vpc/dummy"),
					Tags: []model.Tag{
						{Scope: servicecommon.String(servicecommon.TagScopeSubnetPortCRUID), Tag: servicecommon.String("cr-uid")},
					},
				}
				store.Add(nsxPort)
			}

			api := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod, sp).Build()
			svc := &serviceport.SubnetPortService{Service: servicecommon.Service{Client: api}, SubnetPortStore: store}
			if tc.v2mode {
				tr := true
				svc.NSXClient = &nsx.Client{}
				svc.NSXConfig = &config.NSXOperatorConfig{
					K8sConfig: &config.K8sConfig{PodV2: &tr},
				}
			}
			r := &SubnetPortReconciler{Client: api, APIReader: api, SubnetPortService: svc}
			pending, err := r.getRestoreList()
			if tc.wantError {
				require.ErrorContains(t, err, "UID changed")
			} else {
				require.NoError(t, err)
			}
			if tc.wantRestore {
				require.Equal(t, []types.NamespacedName{client.ObjectKeyFromObject(sp)}, pending)
			} else {
				require.Empty(t, pending)
			}
		})
	}
}
