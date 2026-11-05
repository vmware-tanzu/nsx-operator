package subnetport

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type portOwnerFailureReader struct {
	client.Client
	fail bool
}

func (r *portOwnerFailureReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if key.Name == "owner" && r.fail {
		return context.DeadlineExceeded
	}
	return r.Client.Get(ctx, key, obj, opts...)
}

func TestSubnetPortRetriesAndRevalidatesOwner(t *testing.T) {
	patches := gomonkey.ApplyFunc(config.IsPerNamespaceProvidersSupported, func() bool { return true })
	defer patches.Reset()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	child := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "staging", Annotations: map[string]string{servicecommon.AnnotationSharedVPCNamespace: "owner"}}}
	owner := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "owner", Annotations: map[string]string{config.VPCNetworkConfigAnnotation: "shared"}}}
	port := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{Name: "port", Namespace: child.Name}}
	api := fake.NewClientBuilder().WithScheme(scheme).WithObjects(child, owner, port).Build()
	reader := &portOwnerFailureReader{Client: api, fail: true}
	r := &SubnetPortReconciler{Client: reader, APIReader: reader}
	r.StatusUpdater = common.NewStatusUpdater(api, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")
	reachedAllocation := errors.New("allocation reached")
	allocations := 0
	patches.ApplyMethod(r, "CheckAndGetSubnetPathForSubnetPort", func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
		allocations++
		return false, false, "", nil, nil, "", "", reachedAllocation
	})
	patches.ApplyFunc(setSubnetPortReadyStatusFalse, func(client.Client, context.Context, client.Object, metav1.Time, error, ...interface{}) {})
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer q.ShutDown()
	ev := event.CreateEvent{Object: port}
	require.True(t, common.SubnetPortNamespacePredicate(reader).Create(ev))
	(&handler.EnqueueRequestForObject{}).Create(context.Background(), ev, q)
	require.Equal(t, 1, q.Len(), "a transient owner error must not discard the only create event")
	req, _ := q.Get()
	defer q.Done(req)
	result, err := r.Reconcile(context.Background(), req)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, common.ResultRequeue, result)
	require.Zero(t, allocations)
	reader.fail = false
	_, err = r.Reconcile(context.Background(), req)
	require.ErrorIs(t, err, reachedAllocation)
	require.Equal(t, 1, allocations)
	// A queued/retried request is not an authorization grant. Revocation or a
	// newly added local T1 configuration must block all allocation/writes.
	for _, local := range []bool{false, true} {
		owner.Annotations = map[string]string{config.VPCNetworkConfigAnnotation: "shared"}
		if local {
			child.Annotations[config.T1DefaultConfigAnnotation] = "true"
		} else {
			owner.Annotations = map[string]string{}
		}
		require.NoError(t, api.Update(context.Background(), owner))
		require.NoError(t, api.Update(context.Background(), child))
		result, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(port)})
		require.NoError(t, err)
		require.Equal(t, common.ResultNormal, result)
		require.Equal(t, 1, allocations)
	}
}
