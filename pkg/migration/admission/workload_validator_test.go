/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package admission

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
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

func TestControlPlaneWorkloadProtection(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	transition := &api.ControlPlaneNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 4},
		Spec:       api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}},
	}
	systemNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "system", UID: "system-ns-uid", Annotations: map[string]string{"vmware-system-shared-t1": "true"}}}
	sharedVPCNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "system-vpc", UID: "system-vpc-ns-uid", Annotations: map[string]string{"nsx.vmware.com/shared_vpc_namespace": "kube-system"}}}
	makeValidator := func(objects ...client.Object) WorkloadCutoverValidator {
		return WorkloadCutoverValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()}
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
	oldVM := `{"metadata":{"name":"workload","uid":"vm-uid"}}`
	updatedVM := `{"metadata":{"name":"workload","uid":"vm-uid","annotations":{"migration.vmoperator.vmware.com/in-transition":"true"}}}`

	t.Run("protects VDS and VPC system namespaces from user writes", func(t *testing.T) {
		validator := makeValidator(transition, systemNamespace, sharedVPCNamespace)
		for _, namespace := range []string{"system", "system-vpc", "kube-system"} {
			require.False(t, validator.Handle(context.Background(), makeRequest(namespace, admissionv1.Update, "user", oldVM, updatedVM)).Allowed, namespace)
			require.False(t, validator.Handle(context.Background(), makeRequest(namespace, admissionv1.Update, NetOperatorControllerUser, oldVM, updatedVM)).Allowed, namespace)
			require.False(t, validator.Handle(context.Background(), makeRequest(namespace, admissionv1.Delete, NetOperatorControllerUser, oldVM, "")).Allowed, namespace)
		}
		require.True(t, validator.Handle(context.Background(), makeRequest("vmware-system-nsx", admissionv1.Update, "user", oldVM, updatedVM)).Allowed,
			"the NSX operator namespace is not a control-plane system namespace by name alone")
		require.True(t, validator.Handle(context.Background(), makeRequest("tenant", admissionv1.Update, "user", oldVM, updatedVM)).Allowed)
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

func TestDesignatedControllerWorkloadOperations(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	transition := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "batch"}, Spec: api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"tenant"}}}
	validator := WorkloadCutoverValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(transition).Build()}
	for _, resource := range []struct{ group, name, object string }{
		{"", "pods", `{"metadata":{"name":"item","uid":"old"}}`},
		{"vmoperator.vmware.com", "virtualmachines", `{"metadata":{"name":"item","uid":"old"}}`},
		{"", "services", `{"metadata":{"name":"item","uid":"old"},"spec":{"type":"LoadBalancer"}}`},
	} {
		for _, user := range []string{NCPControllerUser, NetOperatorControllerUser, VMOperatorControllerUser, AKOControllerUser, "ordinary-user", ""} {
			for _, op := range []admissionv1.Operation{admissionv1.Create, admissionv1.Update, admissionv1.Delete} {
				req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Namespace: "tenant", Operation: op, Resource: metav1.GroupVersionResource{Group: resource.group, Version: "v1", Resource: resource.name}, OldObject: runtime.RawExtension{Raw: []byte(resource.object)}, Object: runtime.RawExtension{Raw: []byte(resource.object)}}}
				req.UserInfo.Username = user
				require.False(t, validator.Handle(context.Background(), req).Allowed, "%s %s %s", resource.name, user, op)
			}
		}
	}
	req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Namespace: "tenant", Operation: admissionv1.Update, Resource: metav1.GroupVersionResource{Version: "v1", Resource: "pods"}, OldObject: runtime.RawExtension{Raw: []byte(`{"metadata":{"uid":"old"}}`)}, Object: runtime.RawExtension{Raw: []byte(`{"metadata":{"uid":"new"}}`)}}}
	req.UserInfo.Username = NCPControllerUser
	require.False(t, validator.Handle(context.Background(), req).Allowed)
}

func TestWorkloadControllerCheckpointAndFields(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	old := `{"metadata":{"name":"vm","uid":"uid"},"spec":{"networkInterfaces":[{"name":"old"}]}}`
	allowed := `{"metadata":{"name":"vm","uid":"uid","annotations":{"migration.vmoperator.vmware.com/in-transition":"true"}},"spec":{"networkInterfaces":[{"name":"new"}]}}`
	otherAnnotation := `{"metadata":{"name":"vm","uid":"uid","annotations":{"other":"value"}},"spec":{"networkInterfaces":[{"name":"new"}]}}`
	otherSpec := `{"metadata":{"name":"vm","uid":"uid"},"spec":{"networkInterfaces":[{"name":"new"}],"powerState":"off"}}`
	request := func(user, current string, op admissionv1.Operation) admission.Request {
		return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
			Namespace: "kube-system", Operation: op, Resource: metav1.GroupVersionResource{Group: "vmoperator.vmware.com", Version: "v1", Resource: "virtualmachines"},
			UserInfo: authenticationv1.UserInfo{Username: user}, OldObject: runtime.RawExtension{Raw: []byte(old)}, Object: runtime.RawExtension{Raw: []byte(current)},
		}}
	}
	for _, stage := range []string{"namespace", "control-plane"} {
		for _, state := range []api.ActionState{api.ActionStateRunning, api.ActionStateFailed} {
			t.Run(stage+"/"+string(state), func(t *testing.T) {
				status := api.TransitionStatus{LastAction: api.ActionExecute, ActionState: state, ActionGeneration: 2, ObservedGeneration: 2}
				var transition client.Object
				if stage == "namespace" {
					transition = &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: 2}, Spec: api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"kube-system"}}, Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: status}}
				} else {
					transition = &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 2}, Spec: api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}}, Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: status}}
				}
				validator := WorkloadCutoverValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(transition).Build()}
				check := func(user, object string, op admissionv1.Operation, want bool) {
					t.Helper()
					require.Equal(t, want, validator.Handle(context.Background(), request(user, object, op)).Allowed)
				}
				check(NetOperatorControllerUser, allowed, admissionv1.Update, true)
				check(NetOperatorControllerUser, otherAnnotation, admissionv1.Update, false)
				check(NetOperatorControllerUser, otherSpec, admissionv1.Update, false)
				check(VMOperatorControllerUser, allowed, admissionv1.Update, false)
				check(NetOperatorControllerUser, allowed, admissionv1.Create, false)
				check(NetOperatorControllerUser, allowed, admissionv1.Delete, false)
			})
		}
	}
}

func TestWorkloadControllerCheckpointGuards(t *testing.T) {
	status := api.TransitionStatus{LastAction: api.ActionExecute, ActionState: api.ActionStateRunning, ActionGeneration: 2, ObservedGeneration: 2}
	infra := &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Generation: 2}, Spec: api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}}, Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: status}}
	batch := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Generation: 2}, Spec: api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}}, Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: status}}
	require.True(t, controlPlaneControllerMayChange(infra))
	require.True(t, namespaceControllerMayChange(batch))
	infra.Status.ObservedGeneration = 1
	batch.Status.ObservedGeneration = 1
	require.False(t, controlPlaneControllerMayChange(infra))
	require.False(t, namespaceControllerMayChange(batch))
	infra.Status.ObservedGeneration = 2
	batch.Status.ObservedGeneration = 2
	infra.Status.ActionState = api.ActionStateSucceeded
	batch.Status.ActionState = api.ActionStateSucceeded
	require.False(t, controlPlaneControllerMayChange(infra))
	require.False(t, namespaceControllerMayChange(batch))
	batch.Spec.Action = api.ActionRollback
	batch.Status.LastAction = api.ActionRollback
	batch.Status.ActionState = api.ActionStateRunning
	require.True(t, namespaceControllerMayChange(batch))
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

func TestWorkloadProtectionErrorAndNonSystemPaths(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	ctx := context.Background()
	reader := fake.NewClientBuilder().WithScheme(scheme).Build()
	isControlPlaneNamespace, err := isControlPlaneSystemNamespace(ctx, reader, "vmware-system-nsx")
	require.NoError(t, err)
	require.False(t, isControlPlaneNamespace, "the NSX operator namespace is not a control-plane namespace by name alone")

	t.Run("overlapping transition kinds fail closed", func(t *testing.T) {
		namespaceTransition := &api.NamespaceNetworkTransition{
			ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: 2},
			Spec: api.NamespaceNetworkTransitionSpec{
				TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"kube-system"},
			},
		}
		infraTransition := &api.ControlPlaneNetworkTransition{
			ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName},
			Spec:       api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}},
		}
		reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(namespaceTransition, infraTransition).Build()
		_, err := findWorkloadProtection(ctx, reader, "kube-system")
		require.ErrorContains(t, err, "covered by unresolved transitions")
	})

	t.Run("control plane reader errors are returned", func(t *testing.T) {
		reader := fake.NewClientBuilder().WithScheme(scheme).Build()
		broken := getErrorReader{Reader: reader, err: errors.New("control plane read unavailable")}
		_, err := findWorkloadProtection(ctx, broken, "kube-system")
		require.ErrorContains(t, err, "control plane read unavailable")
	})

	t.Run("ordinary namespace is not a control plane system namespace", func(t *testing.T) {
		reader := fake.NewClientBuilder().WithScheme(scheme).Build()
		isSystem, err := isControlPlaneSystemNamespace(ctx, reader, "tenant")
		require.NoError(t, err)
		require.False(t, isSystem)
	})

	require.False(t, controlPlaneTransitionProtectsWorkloads(nil))
}
