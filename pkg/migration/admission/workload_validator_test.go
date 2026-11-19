/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package admission

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

func TestWorkloadCutoverProtection(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	transition := &api.NamespaceNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: 2},
		Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"tenant"}},
	}
	makeValidator := func(objects ...client.Object) WorkloadCutoverValidator {
		return WorkloadCutoverValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()}
	}
	makeRequest := func(resource, namespace string, operation admissionv1.Operation, old, current string) admission.Request {
		group := ""
		if resource == "virtualmachines" {
			group = "vmoperator.vmware.com"
		}
		return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Namespace: namespace, Operation: operation,
			Resource:  metav1.GroupVersionResource{Group: group, Version: "v1", Resource: resource},
			OldObject: runtime.RawExtension{Raw: []byte(old)}, Object: runtime.RawExtension{Raw: []byte(current)}}}
	}
	pod := `{"metadata":{"generateName":"pod-"}}`
	lb := `{"spec":{"type":"LoadBalancer"}}`
	clusterIP := `{"spec":{"type":"ClusterIP"}}`
	tests := []struct {
		name    string
		req     admission.Request
		allowed bool
	}{
		{"generated pod name is protected", makeRequest("pods", "tenant", admissionv1.Create, "", pod), false},
		{"unrelated namespace", makeRequest("pods", "other", admissionv1.Create, "", pod), true},
		{"vm deletion", makeRequest("virtualmachines", "tenant", admissionv1.Delete, pod, ""), false},
		{"service becomes load balancer", makeRequest("services", "tenant", admissionv1.Update, clusterIP, lb), false},
		{"service stops being load balancer", makeRequest("services", "tenant", admissionv1.Update, lb, clusterIP), false},
		{"other service", makeRequest("services", "tenant", admissionv1.Update, clusterIP, clusterIP), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.allowed, makeValidator(transition).Handle(context.Background(), tc.req).Allowed)
		})
	}
	t.Run("restart and failed rollback retain protection", func(t *testing.T) {
		restored := transition.DeepCopy()
		restored.Spec.Action = api.ActionRollback
		restored.Status = &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: api.ActionRollback, ActionState: api.ActionStateFailed, Phase: api.PhaseFailed, ObservedGeneration: 2, ActionGeneration: 2}}
		require.False(t, makeValidator(restored).Handle(context.Background(), makeRequest("pods", "tenant", admissionv1.Create, "", pod)).Allowed)
		restored.Status.ActionState = api.ActionStateSucceeded
		restored.Status.Phase = api.PhaseRolledBack
		require.True(t, makeValidator(restored).Handle(context.Background(), makeRequest("pods", "tenant", admissionv1.Create, "", pod)).Allowed)
	})
	t.Run("multiple protecting transitions fail closed", func(t *testing.T) {
		other := transition.DeepCopy()
		other.Name = "other"
		response := makeValidator(transition, other).Handle(context.Background(), makeRequest("pods", "tenant", admissionv1.Create, "", pod))
		require.False(t, response.Allowed)
		require.EqualValues(t, 503, response.Result.Code)
	})
}

func TestControllerIdentityWorkloadUpdateException(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	transition := &api.NamespaceNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: 2},
		Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"tenant"}},
		Status:     &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: api.ActionExecute, ActionState: api.ActionStateRunning, ObservedGeneration: 2, ActionGeneration: 2}},
	}
	identity := "system:serviceaccount:vmware-system-netop:vmware-system-netop-controller-manager"
	validator := WorkloadCutoverValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(transition).Build(), ControllerUsers: []string{identity}}
	old := `{"metadata":{"uid":"vm-uid"},"spec":{"cpu":2}}`
	expected := `{"metadata":{"uid":"vm-uid","annotations":{"migration.vmoperator.vmware.com/in-transition":"true"}},"spec":{"cpu":2}}`
	for _, tc := range []struct {
		name, user, object string
		op                 admissionv1.Operation
		allow              bool
	}{
		{"exact annotation", identity, expected, admissionv1.Update, true},
		{"wrong identity", "someone-else", expected, admissionv1.Update, false},
		{"extra spec change", identity, `{"metadata":{"uid":"vm-uid","annotations":{"migration.vmoperator.vmware.com/in-transition":"true"}},"spec":{"cpu":4}}`, admissionv1.Update, true},
		{"other annotation", identity, `{"metadata":{"uid":"vm-uid","annotations":{"other":"true"}},"spec":{"cpu":2}}`, admissionv1.Update, true},
		{"changed uid", identity, `{"metadata":{"uid":"other","annotations":{"migration.vmoperator.vmware.com/in-transition":"true"}},"spec":{"cpu":2}}`, admissionv1.Update, false},
		{"delete is forbidden", identity, "", admissionv1.Delete, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Namespace: "tenant", Operation: tc.op, Resource: metav1.GroupVersionResource{Group: "vmoperator.vmware.com", Version: "v1", Resource: "virtualmachines"}, OldObject: runtime.RawExtension{Raw: []byte(old)}, Object: runtime.RawExtension{Raw: []byte(tc.object)}}}
			req.UserInfo.Username = tc.user
			require.Equal(t, tc.allow, validator.Handle(context.Background(), req).Allowed)
		})
	}
	validator.ControllerUsers = []string{""}
	req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Namespace: "tenant", Operation: admissionv1.Update, Resource: metav1.GroupVersionResource{Group: "vmoperator.vmware.com", Version: "v1", Resource: "virtualmachines"}, OldObject: runtime.RawExtension{Raw: []byte(old)}, Object: runtime.RawExtension{Raw: []byte(expected)}}}
	req.UserInfo.Username = ""
	require.False(t, validator.Handle(context.Background(), req).Allowed, "an empty identity never matches, even an empty username")
}

func TestControlPlaneWorkloadProtection(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	transition := &api.ControlPlaneNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 4},
		Spec:       api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}},
	}
	systemNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "system", Annotations: map[string]string{"vmware-system-shared-t1": "true"}}}
	sharedVPCNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "system-vpc", Annotations: map[string]string{"nsx.vmware.com/shared_vpc_namespace": "kube-system"}}}
	makeValidator := func(objects ...client.Object) WorkloadCutoverValidator {
		return WorkloadCutoverValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build(), ControllerUsers: []string{NetOperatorControllerUser}}
	}
	makeRequest := func(namespace string, operation admissionv1.Operation, user string, old, current string) admission.Request {
		req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
			Namespace: namespace, Operation: operation,
			Resource:  metav1.GroupVersionResource{Group: "vmoperator.vmware.com", Version: "v1", Resource: "virtualmachines"},
			OldObject: runtime.RawExtension{Raw: []byte(old)}, Object: runtime.RawExtension{Raw: []byte(current)},
		}}
		req.UserInfo.Username = user
		return req
	}
	oldVM := `{"metadata":{"uid":"vm-uid"}}`
	updatedVM := `{"metadata":{"uid":"vm-uid","annotations":{"migration.vmoperator.vmware.com/in-transition":"true"}}}`

	t.Run("protects VDS and VPC system namespaces from user writes", func(t *testing.T) {
		validator := makeValidator(transition, systemNamespace, sharedVPCNamespace)
		for _, namespace := range []string{"system", "system-vpc", "kube-system"} {
			require.False(t, validator.Handle(context.Background(), makeRequest(namespace, admissionv1.Update, "user", oldVM, updatedVM)).Allowed, namespace)
			require.False(t, validator.Handle(context.Background(), makeRequest(namespace, admissionv1.Delete, NetOperatorControllerUser, oldVM, "")).Allowed, namespace)
		}
		require.True(t, validator.Handle(context.Background(), makeRequest("tenant", admissionv1.Update, "user", oldVM, updatedVM)).Allowed)
	})

	t.Run("controller updates are allowed only after current Execute checkpoint", func(t *testing.T) {
		current := transition.DeepCopy()
		current.Status = &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
			LastAction: api.ActionExecute, ActionState: api.ActionStateRunning, ActionGeneration: 4, ObservedGeneration: 4,
		}}
		validator := makeValidator(current, systemNamespace)
		require.True(t, validator.Handle(context.Background(), makeRequest("system", admissionv1.Update, NetOperatorControllerUser, oldVM, updatedVM)).Allowed)
		require.False(t, validator.Handle(context.Background(), makeRequest("system", admissionv1.Update, "user", oldVM, updatedVM)).Allowed)

		stale := current.DeepCopy()
		stale.Status.ActionGeneration = 3
		validator = makeValidator(stale, systemNamespace)
		require.False(t, validator.Handle(context.Background(), makeRequest("system", admissionv1.Update, NetOperatorControllerUser, oldVM, updatedVM)).Allowed)
	})

	t.Run("only verified infrastructure completion releases protection", func(t *testing.T) {
		completed := transition.DeepCopy()
		completed.Status = &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
			LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded, ActionGeneration: 4, ObservedGeneration: 4, Phase: api.PhaseInfraTransitionComplete,
		}}
		validator := makeValidator(completed, systemNamespace)
		require.True(t, validator.Handle(context.Background(), makeRequest("system", admissionv1.Update, "user", oldVM, updatedVM)).Allowed)

		stale := completed.DeepCopy()
		stale.Status.ObservedGeneration = 3
		validator = makeValidator(stale, systemNamespace)
		require.False(t, validator.Handle(context.Background(), makeRequest("system", admissionv1.Update, "user", oldVM, updatedVM)).Allowed)
	})
}

func TestNamespaceProtectionActionHandoffs(t *testing.T) {
	status := api.TransitionStatus{LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded, ActionGeneration: 2, ObservedGeneration: 2, Phase: api.PhaseCompleted}
	transition := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Generation: 3}, Spec: api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionCommit}}, Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: status}}
	require.True(t, namespaceTransitionProtects(transition), "completed Execute does not release protection while Commit is pending")
	transition.Status.ActionGeneration = 1
	require.True(t, namespaceTransitionProtects(transition), "stale Execute cannot release")
	transition.Status.TransitionStatus = api.TransitionStatus{LastAction: api.ActionCommit, ActionState: api.ActionStateRunning, ActionGeneration: 3, ObservedGeneration: 3, Phase: api.PhaseCommitting}
	require.True(t, namespaceTransitionProtects(transition), "Commit remains protected while running")
	transition.Spec.Action = api.ActionEvaluate
	transition.Status = &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: api.ActionEvaluate, ActionState: api.ActionStateRunning, ActionGeneration: 3, ObservedGeneration: 3}, RollbackComplete: true}
	require.False(t, namespaceTransitionProtects(transition), "durable rollback remains released after Evaluate starts")
	transition.Spec.Action = api.ActionExecute
	transition.Generation++
	require.True(t, namespaceTransitionProtects(transition), "new Execute reactivates protection")
}

func TestWorkloadRequestDecodingFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		op   admissionv1.Operation
		old  string
		new  string
	}{
		{name: "create missing object", op: admissionv1.Create},
		{name: "create malformed object", op: admissionv1.Create, new: "{"},
		{name: "update missing old object", op: admissionv1.Update, new: `{}`},
		{name: "update malformed old object", op: admissionv1.Update, old: "{", new: `{}`},
		{name: "update missing new object", op: admissionv1.Update, old: `{}`},
		{name: "update malformed new object", op: admissionv1.Update, old: `{}`, new: "{"},
		{name: "delete missing old object", op: admissionv1.Delete},
		{name: "delete malformed old object", op: admissionv1.Delete, old: "{"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := decodeWorkloadObjects(admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: tc.op, OldObject: runtime.RawExtension{Raw: []byte(tc.old)}, Object: runtime.RawExtension{Raw: []byte(tc.new)},
			}})
			require.Error(t, err)
		})
	}
	old, current, err := decodeWorkloadObjects(admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Operation: admissionv1.Update, OldObject: runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"old"}}`)},
		Object: runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"new"}}`)},
	}})
	require.NoError(t, err)
	require.Equal(t, "old", old.GetName())
	require.Equal(t, "new", current.GetName())
	require.False(t, loadBalancerService(nil))
}

func TestWorkloadResourceClassificationAndSystemNamespaceRead(t *testing.T) {
	for _, tc := range []struct {
		name     string
		group    string
		version  string
		resource string
		wantName string
		want     bool
	}{
		{name: "core pod", version: "v1", resource: "pods", wantName: "pods", want: true},
		{name: "core service", version: "v1", resource: "services", wantName: "services", want: true},
		{name: "virtual machine", group: "vmoperator.vmware.com", version: "v1", resource: "virtualmachines", wantName: "virtualmachines", want: true},
		{name: "wrong core version", version: "v2", resource: "pods"},
		{name: "wrong workload group", group: "other.example.com", version: "v1", resource: "virtualmachines"},
		{name: "unrelated resource", version: "v1", resource: "configmaps"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, relevant := workloadResource(admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Resource: metav1.GroupVersionResource{Group: tc.group, Version: tc.version, Resource: tc.resource}}})
			require.Equal(t, tc.wantName, name)
			require.Equal(t, tc.want, relevant)
		})
	}

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	reader := fake.NewClientBuilder().WithScheme(scheme).Build()
	ctx := context.Background()
	isSystem, err := isControlPlaneSystemNamespace(ctx, reader, "missing")
	require.NoError(t, err)
	require.False(t, isSystem)
	isSystem, err = isControlPlaneSystemNamespace(ctx, reader, "kube-system")
	require.NoError(t, err)
	require.True(t, isSystem)

	brokenReader := getErrorReader{Reader: reader, err: errors.New("namespace API unavailable")}
	_, err = isControlPlaneSystemNamespace(ctx, brokenReader, "custom-system")
	require.ErrorContains(t, err, "namespace API unavailable")
}

func TestWorkloadHandleValidationAndReaderFailure(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	reader := fake.NewClientBuilder().WithScheme(scheme).Build()
	validator := WorkloadCutoverValidator{Reader: reader}
	request := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Namespace: "tenant", Operation: admissionv1.Create,
		Resource: metav1.GroupVersionResource{Version: "v1", Resource: "pods"},
		Object:   runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"pod"}}`)},
	}}
	request.SubResource = "status"
	require.True(t, validator.Handle(context.Background(), request).Allowed)
	request.SubResource = ""
	request.Resource.Resource = "configmaps"
	require.True(t, validator.Handle(context.Background(), request).Allowed)
	request.Resource.Resource = "pods"
	request.Namespace = ""
	require.EqualValues(t, 400, validator.Handle(context.Background(), request).Result.Code)
	request.Namespace = "tenant"
	require.EqualValues(t, 503, (WorkloadCutoverValidator{}).Handle(context.Background(), request).Result.Code)

	request.Object = runtime.RawExtension{Raw: []byte("{")}
	require.EqualValues(t, 400, validator.Handle(context.Background(), request).Result.Code)
	request.Object = runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"pod"}}`)}
	brokenReader := listErrorReader{Reader: reader, err: errors.New("transition list unavailable")}
	response := (WorkloadCutoverValidator{Reader: brokenReader}).Handle(context.Background(), request)
	require.EqualValues(t, 503, response.Result.Code)
	require.Contains(t, response.Result.Message, "transition list unavailable")
}
