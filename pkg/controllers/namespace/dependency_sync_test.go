package namespace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

// Fail only the next dependency operation. A single watch event must survive
// both lookup and resource reconciliation failures, without another CR update.
type namespaceFailureClient struct {
	client.Client
	listFailures, ownerFailures int
}

func (c *namespaceFailureClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*corev1.NamespaceList); ok && c.listFailures > 0 {
		c.listFailures--
		return context.DeadlineExceeded
	}
	return c.Client.List(ctx, list, opts...)
}

func (c *namespaceFailureClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*corev1.Namespace); ok && key.Name == "kube-system" && c.ownerFailures > 0 {
		c.ownerFailures--
		return context.DeadlineExceeded
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func TestSharedOwnerAnnotationEvents(t *testing.T) {
	owner := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "owner", Annotations: map[string]string{config.VPCNetworkConfigAnnotation: "a"}}}
	require.True(t, sharedOwnerPredicate.Create(event.CreateEvent{Object: owner}), "owner creation must wake waiting inheritors")
	for _, tc := range []struct {
		name, key, value string
		changed          bool
	}{
		{"config switch", config.VPCNetworkConfigAnnotation, "b", true},
		{"same config", config.VPCNetworkConfigAnnotation, "a", false},
		{"T1 conflict", config.T1DefaultConfigAnnotation, "true", true},
		{"unrelated annotation", "example.org/unrelated", "value", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updated := owner.DeepCopy()
			updated.Annotations[tc.key] = tc.value
			require.Equal(t, tc.changed, sharedOwnerPredicate.Update(event.UpdateEvent{ObjectOld: owner, ObjectNew: updated}))
		})
	}
	missing, explicitEmpty := owner.DeepCopy(), owner.DeepCopy()
	delete(missing.Annotations, config.VPCNetworkConfigAnnotation)
	explicitEmpty.Annotations[config.VPCNetworkConfigAnnotation] = ""
	require.True(t, sharedOwnerPredicate.Update(event.UpdateEvent{ObjectOld: missing, ObjectNew: explicitEmpty}))
}
