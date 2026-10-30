package framework

import (
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestDeploymentReady(t *testing.T) {
	ready := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Generation: 2},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To(int32(2))},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 2, Replicas: 2, UpdatedReplicas: 2,
			ReadyReplicas: 2, AvailableReplicas: 2,
		},
	}
	tests := []struct {
		name   string
		mutate func(*appsv1.Deployment)
		want   bool
	}{
		{"ready", func(*appsv1.Deployment) {}, true},
		{"old generation", func(d *appsv1.Deployment) { d.Status.ObservedGeneration = 1 }, false},
		{"old ready replica", func(d *appsv1.Deployment) { d.Status.UpdatedReplicas = 1 }, false},
		{"pending replica", func(d *appsv1.Deployment) { d.Status.ReadyReplicas = 1 }, false},
		{"unavailable replica", func(d *appsv1.Deployment) { d.Status.AvailableReplicas = 1 }, false},
		{"old replica remains", func(d *appsv1.Deployment) { d.Status.Replicas = 3 }, false},
		{"scaled down", func(d *appsv1.Deployment) { d.Spec.Replicas = ptr.To(int32(0)) }, false},
		{"no desired count", func(d *appsv1.Deployment) { d.Spec.Replicas = nil }, false},
		{"deleting", func(d *appsv1.Deployment) { d.DeletionTimestamp = ptr.To(metav1.Now()) }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := ready.DeepCopy()
			tt.mutate(d)
			require.Equal(t, tt.want, DeploymentReady(d))
		})
	}
}
