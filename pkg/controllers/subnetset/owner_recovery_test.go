package subnetset

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	controllercommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestPrecreatedSubnetSetRecoversAfterOwnerOutage(t *testing.T) {
	previous := config.IsPerNamespaceProvidersSupported()
	config.SetPerNamespaceProvidersSupportedForTest(true)
	t.Cleanup(func() { config.SetPerNamespaceProvidersSupportedForTest(previous) })
	for _, waitingFor := range []string{"ready condition", "gateway status"} {
		t.Run(waitingFor, func(t *testing.T) {
			ctx := context.Background()
			child := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "child", Annotations: map[string]string{servicecommon.AnnotationSharedVPCNamespace: "owner"}}}
			member := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: child.Name}, Spec: v1alpha1.SubnetSpec{IPAddressType: v1alpha1.IPAddressTypeIPv4}, Status: v1alpha1.SubnetStatus{Conditions: []v1alpha1.Condition{{Type: v1alpha1.Ready, Status: corev1.ConditionFalse, Reason: "SharedVPCOwnerUnavailable"}}}}
			members := []string{member.Name}
			set := &v1alpha1.SubnetSet{ObjectMeta: metav1.ObjectMeta{Name: "named", Namespace: child.Name, UID: "existing-set"}, Spec: v1alpha1.SubnetSetSpec{SubnetNames: &members, IPAddressType: v1alpha1.IPAddressTypeIPv4}, Status: v1alpha1.SubnetSetStatus{Conditions: []v1alpha1.Condition{{Type: v1alpha1.Ready, Status: corev1.ConditionFalse, Reason: "SharedVPCOwnerUnavailable"}}}}
			port := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{Name: "existing-port", Namespace: child.Name}, Spec: v1alpha1.SubnetPortSpec{SubnetSet: set.Name, AddressBindings: []v1alpha1.PortAddressBinding{{IPAddress: "192.0.2.6"}}}}
			r := createFakeSubnetSetReconciler(nil)
			failRead := false
			api := fake.NewClientBuilder().WithScheme(r.Client.Scheme()).WithObjects(child, member, set, port).WithStatusSubresource(member, set).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*v1alpha1.Subnet); ok && failRead {
						return context.DeadlineExceeded
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build()
			r.Client = api
			r.StatusUpdater = controllercommon.NewStatusUpdater(api, r.SubnetService.NSXConfig, &fakeRecorder{}, MetricResTypeSubnetSet, "Subnet", "SubnetSet")
			req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)}
			// The controller's default limiter retains failures during a long owner
			// outage. A recovery event must not keep that delay for a pending member.
			limiter := workqueue.NewTypedItemExponentialFailureRateLimiter[ctrl.Request](5*time.Millisecond, 1000*time.Second)
			var backoff time.Duration
			for i := 0; i < 20; i++ {
				_, err := r.Reconcile(ctx, req)
				var ownerErr *controllercommon.SharedVPCOwnerUnavailableError
				require.ErrorAs(t, err, &ownerErr)
				backoff = limiter.When(req)
			}
			require.Greater(t, backoff, 8*time.Minute)
			require.NoError(t, api.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "owner", Annotations: map[string]string{config.VPCNetworkConfigAnnotation: "owner-config"}}}))
			if waitingFor == "gateway status" {
				member.Status.Conditions[0].Status = corev1.ConditionTrue
				require.NoError(t, api.Status().Update(ctx, member))
			}
			result, err := r.Reconcile(ctx, req)
			t.Logf("outage backoff=%s; owner restored, member pending: result=%+v error=%v", backoff, result, err)
			require.NoError(t, err, "a pending member must use an explicit retry interval, not the accumulated error backoff")
			require.Positive(t, result.RequeueAfter)
			require.LessOrEqual(t, result.RequeueAfter, 10*time.Second)
			require.NoError(t, api.Get(ctx, req.NamespacedName, set))
			require.False(t, controllercommon.IsObjectReady(set.Status.Conditions))
			// Genuine API failures must still propagate as errors, not become waits.
			failRead = true
			_, err = r.Reconcile(ctx, req)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			failRead = false
			// Only the member changes. The scheduled retry must restore the same
			// named set without a Namespace/config/port update or recreating the set.
			require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(member), member))
			member.Status.Conditions[0].Status = corev1.ConditionTrue
			member.Status.GatewayAddresses = []string{"192.0.2.1/28"}
			member.Status.NetworkAddresses = []string{"192.0.2.0/28"}
			require.NoError(t, api.Status().Update(ctx, member))
			result, err = r.Reconcile(ctx, req)
			require.NoError(t, err)
			require.Zero(t, result.RequeueAfter)
			require.NoError(t, api.Get(ctx, req.NamespacedName, set))
			require.True(t, controllercommon.IsObjectReady(set.Status.Conditions))
			require.Equal(t, "existing-set", string(set.UID))
			require.Equal(t, members, *set.Spec.SubnetNames)
			require.Equal(t, member.Status.NetworkAddresses, set.Status.Subnets[0].NetworkAddresses)
			gotPort := &v1alpha1.SubnetPort{}
			require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(port), gotPort))
			require.Equal(t, port.Spec, gotPort.Spec)
		})
	}
}
