package common

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestSetReconfigureNicStatusRefetchesAfterConflict(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1.AddToScheme(scheme))
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "ns", UID: "pod-uid"},
		Status: v1.PodStatus{Conditions: []v1.PodCondition{
			{Type: v1.PodReady, Status: v1.ConditionFalse},
		}},
	}
	api := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1.Pod{}).WithObjects(pod).Build()
	key := client.ObjectKeyFromObject(pod)
	require.NoError(t, api.Get(ctx, key, pod))
	original := pod.DeepCopy()
	updates := 0
	writer := interceptor.NewClient(api, interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return fmt.Errorf("read through APIReader instead of the cached client")
		},
		SubResourceUpdate: func(ctx context.Context, c client.Client, subresource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			updates++
			if updates == 1 {
				// Another writer changes Pod status between our read and write.
				concurrent := &v1.Pod{}
				require.NoError(t, c.Get(ctx, key, concurrent))
				concurrent.Status.Conditions[0].Status = v1.ConditionTrue
				require.NoError(t, c.Status().Update(ctx, concurrent))
			}
			err := c.SubResource(subresource).Update(ctx, obj, opts...)
			if updates == 1 {
				require.True(t, apierrors.IsConflict(err), "first write must hit a real resourceVersion conflict")
			}
			return err
		},
	})
	err := retry.OnError(wait.Backoff{Steps: 2}, apierrors.IsConflict, func() error {
		return SetReconfigureNicStatus(writer, api, ctx, pod)
	})
	require.NoError(t, err)
	require.Equal(t, 2, updates)
	require.Equal(t, original, pod, "failed updates must not mutate the caller's Pod")
	stored := &v1.Pod{}
	require.NoError(t, api.Get(ctx, key, stored))
	require.Equal(t, v1.ConditionTrue, GetExistingPodConditionOfType(v1.PodReady, stored.Status.Conditions).Status)
	condition := GetExistingPodConditionOfType("PodNetworkReconfigRequired", stored.Status.Conditions)
	require.NotNil(t, condition)
	require.Equal(t, v1.ConditionTrue, condition.Status)
	require.Equal(t, "ReconfigureNic", condition.Reason)

	// Reusing the stale caller object must not write again once the API has the condition.
	require.NoError(t, SetReconfigureNicStatus(writer, api, ctx, pod))
	require.Equal(t, 2, updates)
}

func TestSetReconfigureNicStatusRejectsUnavailableOrReplacedPod(t *testing.T) {
	for _, tc := range []struct {
		name    string
		uid     types.UID
		getErr  error
		wantErr string
	}{
		{name: "missing", wantErr: "not found"},
		{name: "read failure", uid: "original-uid", getErr: fmt.Errorf("API unavailable"), wantErr: "API unavailable"},
		{name: "replacement", uid: "replacement-uid", wantErr: "UID changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			require.NoError(t, v1.AddToScheme(scheme))
			pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "ns", UID: "original-uid"}}
			builder := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1.Pod{})
			if tc.uid != "" {
				stored := pod.DeepCopy()
				stored.UID = tc.uid
				builder.WithObjects(stored)
			}
			api := builder.Build()
			updates := 0
			c := interceptor.NewClient(api, interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if tc.getErr != nil {
						return tc.getErr
					}
					return c.Get(ctx, key, obj, opts...)
				},
				SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
					updates++
					return nil
				},
			})
			require.ErrorContains(t, SetReconfigureNicStatus(c, c, ctx, pod), tc.wantErr)
			require.Zero(t, updates)
			require.Empty(t, pod.Status.Conditions)
			if tc.uid != "" {
				stored := &v1.Pod{}
				require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(pod), stored))
				require.Empty(t, stored.Status.Conditions)
			}
		})
	}
}
