package pod

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// Reproduce the manager client's split reads/writes: Create reaches the API server,
// but a subsequent indexed List can still see the previous informer snapshot.
type staleSubnetPortListClient struct{ client.Client }

func (c staleSubnetPortListClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if ports, ok := list.(*v1alpha1.SubnetPortList); ok {
		ports.Items = nil
		return nil
	}
	return c.Client.List(ctx, list, opts...)
}

func newPodV2TestReconciler(t *testing.T, precreated bool, stale bool) (*PodReconciler, client.Client, ctrl.Request) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "workload", Namespace: "ns", UID: "12345678-1234-1234-1234-123456789abc"}, Spec: v1.PodSpec{NodeName: "esx1"}}
	ss := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{Name: "default-pod", Namespace: "ns", Labels: map[string]string{servicecommon.LabelDefaultNetwork: servicecommon.DefaultPodNetwork}},
		Spec:       v1alpha1.SubnetSetSpec{IPAddressType: v1alpha1.IPAddressTypeIPv4},
	}
	if precreated {
		names := []string{"dhcp-subnet"}
		ss.Spec.SubnetNames = &names
	}
	api := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod, ss).
		WithIndex(&v1alpha1.SubnetPort{}, util.SubnetPortNamespacePodIndexKey, func(obj client.Object) []string {
			sp := obj.(*v1alpha1.SubnetPort)
			name := common.GetPodNameForSubnetPort(sp)
			if name == "" {
				return nil
			}
			return []string{fmt.Sprintf("%s/%s", sp.Namespace, name)}
		}).Build()
	var cached client.Client = api
	if stale {
		cached = staleSubnetPortListClient{api}
	}
	enabled := true
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{PodV2: &enabled}}
	svc := &subnetport.SubnetPortService{Service: servicecommon.Service{Client: cached, NSXClient: &nsx.Client{}, NSXConfig: cfg}}
	r := &PodReconciler{Client: cached, APIReader: api, Scheme: scheme, SubnetPortService: svc, Recorder: fakeRecorder{}}
	r.StatusUpdater = common.NewStatusUpdater(cached, cfg, r.Recorder, MetricResTypePod, "SubnetPort", "Pod")
	return r, api, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}}
}

func TestPodV2StaleCacheDoesNotDuplicateSubnetPorts(t *testing.T) {
	r, api, req := newPodV2TestReconciler(t, false, true)
	for i := 0; i < 2; i++ {
		if _, err := r.Reconcile(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	ports := &v1alpha1.SubnetPortList{}
	if err := api.List(context.Background(), ports); err != nil {
		t.Fatal(err)
	}
	for _, sp := range ports.Items {
		t.Logf("Created %s owned by Pod UID %s", sp.Name, sp.OwnerReferences[0].UID)
	}
	if len(ports.Items) != 1 {
		t.Fatalf("same Pod must own one SubnetPort, got %d after two reconciles against stale List cache", len(ports.Items))
	}
}

func TestPodV2PrecreatedSubnetUsesActualIPAllocation(t *testing.T) {
	r, api, req := newPodV2TestReconciler(t, true, false)
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	ports := &v1alpha1.SubnetPortList{}
	if err := api.List(context.Background(), ports); err != nil {
		t.Fatal(err)
	}
	if len(ports.Items) != 1 {
		t.Fatalf("want 1 port, got %d", len(ports.Items))
	}
	// The referenced pre-created subnet uses DHCP, while the SubnetSet only
	// carries subnetNames and IPAddressType, as required by its API contract.
	nsxSubnet := &model.VpcSubnet{SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: servicecommon.String("DHCP_SERVER")}, AdvancedConfig: &model.SubnetAdvancedConfig{StaticIpAllocation: &model.StaticIpAllocation{Enabled: servicecommon.Bool(false)}}}
	sp := ports.Items[0]
	expected := util.ComputeDefaultStaticIPAllocationType(nsxSubnet, sp.Spec.InterfaceIPType)
	actual := common.ResolveEffectiveStaticIPAllocationType(sp.Spec.StaticIPAllocationType, nsxSubnet, sp.Spec.InterfaceIPType)
	if actual != expected {
		t.Fatalf("Pod-created CR forces %s; the actual DHCP subnet requires %s (explicit value prevents downstream defaulting)", actual, expected)
	}
}

func TestPodV2NameCollisionsPreserveOtherOwners(t *testing.T) {
	ctx := context.Background()
	for _, collisions := range []int{1} {
		t.Run(fmt.Sprintf("collisions=%d", collisions), func(t *testing.T) {
			r, api, req := newPodV2TestReconciler(t, false, false)
			pod := &v1.Pod{}
			require.NoError(t, api.Get(ctx, req.NamespacedName, pod))
			for attempt := 0; attempt < collisions; attempt++ {
				other := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{
					Name: common.GenerateSubnetPortName(pod, attempt), Namespace: pod.Namespace,
					OwnerReferences: []metav1.OwnerReference{{Kind: "Pod", Name: "other", UID: "other-uid", Controller: servicecommon.Bool(true)}},
				}}
				require.NoError(t, api.Create(ctx, other))
			}
			for i := 0; i < 2; i++ {
				_, err := r.Reconcile(ctx, req)
				require.NoError(t, err)
			}
			ports := &v1alpha1.SubnetPortList{}
			require.NoError(t, api.List(ctx, ports))
			require.Len(t, ports.Items, collisions+1)
			owned := 0
			for _, sp := range ports.Items {
				if metav1.IsControlledBy(&sp, pod) {
					owned++
					require.Equal(t, "default-pod", sp.Spec.SubnetSet)
				} else {
					require.Equal(t, types.UID("other-uid"), sp.OwnerReferences[0].UID)
				}
			}
			require.Equal(t, 1, owned)
		})
	}
}

func TestPodV2NameCollisionRetryLimit(t *testing.T) {
	ctx := context.Background()
	for _, reuseLast := range []bool{false, true} {
		t.Run(fmt.Sprintf("reuse-last-attempt=%v", reuseLast), func(t *testing.T) {
			r, api, req := newPodV2TestReconciler(t, false, false)
			pod := &v1.Pod{}
			require.NoError(t, api.Get(ctx, req.NamespacedName, pod))
			attempts := 0
			var foreignPorts []*v1alpha1.SubnetPort
			r.Client = interceptor.NewClient(api.(client.WithWatch), interceptor.Funcs{
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					sp, ok := obj.(*v1alpha1.SubnetPort)
					if !ok {
						return c.Create(ctx, obj, opts...)
					}
					attempts++
					require.LessOrEqual(t, attempts, 2, "reconcile must yield after exhausting name collision attempts")
					// Simulate another writer taking each name, including the random fallback,
					// before Create reaches the API server.
					existing := sp.DeepCopy()
					if !reuseLast || attempts < 2 {
						existing.OwnerReferences = []metav1.OwnerReference{{Kind: "Pod", Name: "other", UID: "other-uid", Controller: servicecommon.Bool(true)}}
					}
					require.NoError(t, c.Create(ctx, existing, opts...))
					if !metav1.IsControlledBy(existing, pod) {
						foreignPorts = append(foreignPorts, existing.DeepCopy())
					}
					return apierrors.NewAlreadyExists(v1alpha1.Resource("subnetports"), sp.Name)
				},
			})
			_, err := r.Reconcile(ctx, req)
			require.Equal(t, 2, attempts)
			if reuseLast {
				require.NoError(t, err, "a CR owned by this Pod can be reused on the last attempt")
			} else {
				require.ErrorContains(t, err, "after 2 name collisions")
				require.True(t, apierrors.IsAlreadyExists(err))
			}
			owned, err := common.GetSubnetPortForPod(ctx, api, pod)
			require.NoError(t, err)
			if reuseLast {
				require.NotNil(t, owned)
			} else {
				require.Nil(t, owned)
			}

			// Once competing writes stop, later reconciles create or reuse exactly one CR.
			r.Client = api
			// If we simulated a foreign collision on Attempt 1 (full UID), delete it now
			// since in reality full UIDs do not collide, and we need the next Reconcile to succeed.
			if !reuseLast && len(foreignPorts) == 2 {
				api.Delete(ctx, foreignPorts[1])
				foreignPorts = foreignPorts[:1]
			}
			for i := 0; i < 2; i++ {
				_, err = r.Reconcile(ctx, req)
				require.NoError(t, err)
			}
			ports := &v1alpha1.SubnetPortList{}
			require.NoError(t, api.List(ctx, ports))
			require.Len(t, ports.Items, len(foreignPorts)+1)
			owned, err = common.GetSubnetPortForPod(ctx, api, pod)
			require.NoError(t, err)
			require.NotNil(t, owned)
			for _, foreign := range foreignPorts {
				actual := &v1alpha1.SubnetPort{}
				require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(foreign), actual))
				require.Equal(t, foreign, actual, "name collisions must not modify another Pod's CR")
			}
		})
	}
}

func TestPodV2APIFailuresCanRecover(t *testing.T) {
	ctx := context.Background()
	unavailable := fmt.Errorf("API temporarily unavailable")
	for _, operation := range []string{"get-pod", "list-ports", "list-terminal-ports", "create-port", "delete-port", "read-colliding-port"} {
		t.Run(operation, func(t *testing.T) {
			r, api, req := newPodV2TestReconciler(t, false, false)
			terminal := operation == "list-terminal-ports" || operation == "delete-port"
			if terminal {
				_, err := r.Reconcile(ctx, req)
				require.NoError(t, err)
				pod := &v1.Pod{}
				require.NoError(t, api.Get(ctx, req.NamespacedName, pod))
				pod.Status.Phase = v1.PodSucceeded
				require.NoError(t, api.Status().Update(ctx, pod))
			}
			if operation == "read-colliding-port" {
				_, err := r.Reconcile(ctx, req)
				require.NoError(t, err)
			}
			failing := true
			wrapped := interceptor.NewClient(api.(client.WithWatch), interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					_, isPod := obj.(*v1.Pod)
					if failing && ((isPod && operation == "get-pod") || (!isPod && operation == "read-colliding-port")) {
						return unavailable
					}
					return c.Get(ctx, key, obj, opts...)
				},
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if ports, ok := list.(*v1alpha1.SubnetPortList); ok {
						if failing && (operation == "list-ports" || operation == "list-terminal-ports") {
							return unavailable
						}
						if operation == "read-colliding-port" {
							ports.Items = nil // Reproduce informer lag after a successful Create.
							return nil
						}
					}
					return c.List(ctx, list, opts...)
				},
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if failing && operation == "create-port" {
						return unavailable
					}
					return c.Create(ctx, obj, opts...)
				},
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if failing && operation == "delete-port" {
						return unavailable
					}
					return c.Delete(ctx, obj, opts...)
				},
			})
			r.Client, r.APIReader = wrapped, wrapped
			_, err := r.Reconcile(ctx, req)
			require.ErrorIs(t, err, unavailable)
			failing = false
			_, err = r.Reconcile(ctx, req)
			require.NoError(t, err)
			ports := &v1alpha1.SubnetPortList{}
			require.NoError(t, api.List(ctx, ports))
			if terminal {
				require.Empty(t, ports.Items)
			} else {
				require.Len(t, ports.Items, 1, "retry must leave exactly one CR")
			}
		})
	}
}

func TestPodV2WaitsForDefaultSubnetSet(t *testing.T) {
	ctx := context.Background()
	for _, missing := range []bool{true, false} {
		t.Run(fmt.Sprintf("missing=%v", missing), func(t *testing.T) {
			r, api, req := newPodV2TestReconciler(t, false, false)
			ss := &v1alpha1.SubnetSet{}
			require.NoError(t, api.Get(ctx, types.NamespacedName{Namespace: "ns", Name: "default-pod"}, ss))
			if missing {
				require.NoError(t, api.Delete(ctx, ss))
			} else {
				ss.Spec.IPAddressType = ""
				require.NoError(t, api.Update(ctx, ss))
			}
			_, err := r.Reconcile(ctx, req)
			require.Error(t, err)
			ports := &v1alpha1.SubnetPortList{}
			require.NoError(t, api.List(ctx, ports))
			require.Empty(t, ports.Items)
			ss.Spec.IPAddressType = v1alpha1.IPAddressTypeIPv6
			if missing {
				ss.ResourceVersion = ""
				require.NoError(t, api.Create(ctx, ss))
			} else {
				require.NoError(t, api.Update(ctx, ss))
			}
			_, err = r.Reconcile(ctx, req)
			require.NoError(t, err)
			require.NoError(t, api.List(ctx, ports))
			require.Len(t, ports.Items, 1)
			require.Equal(t, v1alpha1.IPAddressTypeIPv6, ports.Items[0].Spec.InterfaceIPType)
		})
	}
}

func TestPodV2DeletedPodIsNotRecreated(t *testing.T) {
	r, api, req := newPodV2TestReconciler(t, false, false)
	ctx := context.Background()
	pod := &v1.Pod{}
	require.NoError(t, api.Get(ctx, req.NamespacedName, pod))
	require.NoError(t, api.Delete(ctx, pod))
	_, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	ports := &v1alpha1.SubnetPortList{}
	require.NoError(t, api.List(ctx, ports))
	require.Empty(t, ports.Items)
	require.True(t, apierrors.IsNotFound(api.Get(ctx, req.NamespacedName, &v1.Pod{})))
}
