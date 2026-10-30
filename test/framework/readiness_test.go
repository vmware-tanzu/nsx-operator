package framework

import (
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

type operatorFixture struct {
	deployment  *appsv1.Deployment
	replicaSets []appsv1.ReplicaSet
	pods        []corev1.Pod
	slices      []discoveryv1.EndpointSlice
}

func newOperatorFixture() operatorFixture {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "operator", Namespace: "system", UID: "deployment", Generation: 2},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(2)),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"component": "operator"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "operator", Image: "operator:current"}}},
			},
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 2, Replicas: 2, UpdatedReplicas: 2,
			ReadyReplicas: 1, AvailableReplicas: 1,
		},
	}
	rs := appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "current", Namespace: "system", UID: "current-rs",
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(deployment, appsv1.SchemeGroupVersion.WithKind("Deployment"))},
		},
		Spec: appsv1.ReplicaSetSpec{Replicas: ptr.To(int32(2)), Template: *deployment.Spec.Template.DeepCopy()},
	}
	rs.Spec.Template.Labels[appsv1.DefaultDeploymentUniqueLabelKey] = "current-hash"
	master := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "master", Namespace: "system", UID: "current-master",
			Labels:          map[string]string{"component": "operator", "nsx-operator-role": "master"},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(&rs, appsv1.SchemeGroupVersion.WithKind("ReplicaSet"))},
		},
		Spec: deployment.Spec.Template.Spec,
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "operator", Ready: true,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
	pending := master.DeepCopy()
	pending.Name, pending.UID = "pending", "pending-pod"
	pending.Labels["nsx-operator-role"] = "standby"
	pending.Status = corev1.PodStatus{Phase: corev1.PodPending}
	slice := discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Name: "webhook", Namespace: "system"},
		Endpoints: []discoveryv1.Endpoint{{
			Addresses:  []string{"192.0.2.10"},
			Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(true), Serving: ptr.To(true)},
			TargetRef:  &corev1.ObjectReference{Kind: "Pod", Namespace: master.Namespace, Name: master.Name, UID: master.UID},
		}},
	}
	return operatorFixture{deployment, []appsv1.ReplicaSet{rs}, []corev1.Pod{master, *pending}, []discoveryv1.EndpointSlice{slice}}
}

func TestOperatorReady(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*operatorFixture)
		want   bool
	}{
		{"one serving master and one unscheduled replica", func(*operatorFixture) {}, true},
		{"unobserved generation", func(f *operatorFixture) { f.deployment.Status.ObservedGeneration = 1 }, false},
		{"scaled down", func(f *operatorFixture) { f.deployment.Spec.Replicas = ptr.To(int32(0)) }, false},
		{"no desired count", func(f *operatorFixture) { f.deployment.Spec.Replicas = nil }, false},
		{"deleting deployment", func(f *operatorFixture) { f.deployment.DeletionTimestamp = ptr.To(metav1.Now()) }, false},
		{"old template", func(f *operatorFixture) { f.replicaSets[0].Spec.Template.Spec.Containers[0].Image = "operator:old" }, false},
		{"recreated deployment", func(f *operatorFixture) { f.deployment.UID = "replacement-deployment" }, false},
		{"uncontrolled replica set", func(f *operatorFixture) { f.replicaSets[0].OwnerReferences = nil }, false},
		{"scaled down replica set", func(f *operatorFixture) { f.replicaSets[0].Spec.Replicas = ptr.To(int32(0)) }, false},
		{"deleting replica set", func(f *operatorFixture) { f.replicaSets[0].DeletionTimestamp = ptr.To(metav1.Now()) }, false},
		{"pod owned by another replica set", func(f *operatorFixture) { f.pods[0].OwnerReferences[0].UID = "old-rs" }, false},
		{"pod is pending", func(f *operatorFixture) { f.pods[0].Status.Phase = corev1.PodPending }, false},
		{"pod is not ready", func(f *operatorFixture) { f.pods[0].Status.Conditions[0].Status = corev1.ConditionFalse }, false},
		{"standby only", func(f *operatorFixture) { f.pods[0].Labels["nsx-operator-role"] = "standby" }, false},
		{"deleting master", func(f *operatorFixture) { f.pods[0].DeletionTimestamp = ptr.To(metav1.Now()) }, false},
		{"unready container", func(f *operatorFixture) { f.pods[0].Status.ContainerStatuses[0].Ready = false }, false},
		{"container not running", func(f *operatorFixture) { f.pods[0].Status.ContainerStatuses[0].State.Running = nil }, false},
		{"missing container status", func(f *operatorFixture) { f.pods[0].Status.ContainerStatuses = nil }, false},
		{"missing webhook backend", func(f *operatorFixture) { f.slices = nil }, false},
		{"unready webhook backend", func(f *operatorFixture) { f.slices[0].Endpoints[0].Conditions.Ready = ptr.To(false) }, false},
		{"unknown backend readiness", func(f *operatorFixture) { f.slices[0].Endpoints[0].Conditions.Ready = nil }, false},
		{"backend no longer serving", func(f *operatorFixture) { f.slices[0].Endpoints[0].Conditions.Serving = ptr.To(false) }, false},
		{"terminating backend", func(f *operatorFixture) { f.slices[0].Endpoints[0].Conditions.Terminating = ptr.To(true) }, false},
		{"empty backend address", func(f *operatorFixture) { f.slices[0].Endpoints[0].Addresses = nil }, false},
		{"backend belongs to replaced pod", func(f *operatorFixture) { f.slices[0].Endpoints[0].TargetRef.UID = "old-master" }, false},
		{"backend has no pod reference", func(f *operatorFixture) { f.slices[0].Endpoints[0].TargetRef = nil }, false},
		{"backend in another namespace", func(f *operatorFixture) { f.slices[0].Namespace = "other" }, false},
		{"ready old replica and pending current replica", func(f *operatorFixture) {
			current := f.replicaSets[0].DeepCopy()
			current.Name, current.UID = "new", "new-rs"
			f.replicaSets[0].Spec.Template.Spec.Containers[0].Image = "operator:old"
			f.pods[1].OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(current, appsv1.SchemeGroupVersion.WithKind("ReplicaSet"))}
			f.replicaSets = append(f.replicaSets, *current)
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newOperatorFixture()
			tt.mutate(&f)
			before := f.replicaSets[0].DeepCopy()
			require.Equal(t, tt.want, OperatorReady(f.deployment, f.replicaSets, f.pods, f.slices))
			require.Equal(t, before, &f.replicaSets[0], "readiness must not mutate the informer snapshot")
		})
	}
	require.False(t, OperatorReady(nil, nil, nil, nil))
}
