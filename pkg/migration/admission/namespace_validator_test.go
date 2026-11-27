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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

func TestNamespaceProtectionStartsWithExecuteSpec(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	transition := &api.NamespaceNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: 3},
		Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"tenant"}},
		Status:     &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: api.ActionEvaluate, ActionState: api.ActionStateSucceeded, ActionGeneration: 2, ObservedGeneration: 2}},
	}
	validator := NamespaceLifecycleValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(transition).Build(), ControllerUser: "controller"}
	request := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Name: "tenant", Operation: admissionv1.Update,
		Resource:  metav1.GroupVersionResource{Group: SupervisorNamespaceGroup, Version: "v1alpha2", Resource: "supervisornamespaces"},
		UserInfo:  authenticationv1.UserInfo{Username: "ordinary-user"},
		OldObject: runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"tenant"},"spec":{"other":"before"}}`)},
		Object:    runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"tenant"},"spec":{"other":"after"}}`)},
	}}
	response := validator.Handle(context.Background(), request)
	require.False(t, response.Allowed, "a new Execute request must protect the Namespace before Running is checkpointed")

	request.Operation = admissionv1.Delete
	request.OldObject = runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"tenant"}}`)}
	response = validator.Handle(context.Background(), request)
	require.False(t, response.Allowed, "deletion is also blocked before Running is checkpointed")
}

func TestNamespaceProtectionRemainsUntilCommitCompletes(t *testing.T) {
	tests := []struct {
		name       string
		status     api.TransitionStatus
		generation int64
		want       bool
	}{
		{
			name: "commit requested after execute but before its checkpoint",
			status: api.TransitionStatus{
				LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded,
				ActionGeneration: 3, ObservedGeneration: 3, Phase: api.PhaseCompleted,
			},
			generation: 4,
			want:       true,
		},
		{
			name: "commit running",
			status: api.TransitionStatus{
				LastAction: api.ActionCommit, ActionState: api.ActionStateRunning,
				ActionGeneration: 4, ObservedGeneration: 4, Phase: api.PhaseCommitting,
				IrreversibleAction: string(api.ActionCommit),
			},
			generation: 4,
			want:       true,
		},
		{
			name: "commit failed",
			status: api.TransitionStatus{
				LastAction: api.ActionCommit, ActionState: api.ActionStateFailed,
				ActionGeneration: 4, ObservedGeneration: 4, Phase: api.PhaseCommitting,
				IrreversibleAction: string(api.ActionCommit),
			},
			generation: 4,
			want:       true,
		},
		{
			name: "commit completed",
			status: api.TransitionStatus{
				LastAction: api.ActionCommit, ActionState: api.ActionStateSucceeded,
				ActionGeneration: 4, ObservedGeneration: 4, Phase: api.PhaseCommitted,
				IrreversibleAction: string(api.ActionCommit),
			},
			generation: 4,
			want:       false,
		},
		{
			name: "committed phase without irreversible checkpoint",
			status: api.TransitionStatus{
				LastAction: api.ActionCommit, ActionState: api.ActionStateSucceeded,
				ActionGeneration: 4, ObservedGeneration: 4, Phase: api.PhaseCommitted,
			},
			generation: 4,
			want:       true,
		},
		{
			name: "stale commit checkpoint",
			status: api.TransitionStatus{
				LastAction: api.ActionCommit, ActionState: api.ActionStateSucceeded,
				ActionGeneration: 3, ObservedGeneration: 3, Phase: api.PhaseCommitted,
				IrreversibleAction: string(api.ActionCommit),
			},
			generation: 4,
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transition := &api.NamespaceNetworkTransition{
				ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: tt.generation},
				Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionCommit}},
				Status:     &api.NamespaceNetworkTransitionStatus{TransitionStatus: tt.status},
			}
			require.Equal(t, tt.want, namespaceTransitionProtects(transition))
		})
	}
}

func TestEmptyNamespaceControllerIdentityNeverMatches(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	transition := &api.NamespaceNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: 2},
		Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"tenant"}},
		Status:     &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: api.ActionExecute, ActionState: api.ActionStateRunning, ActionGeneration: 2, ObservedGeneration: 2}},
	}
	validator := NamespaceLifecycleValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(transition).Build(), ControllerUser: ""}
	request := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Name: "tenant", Operation: admissionv1.Update,
		Resource:  metav1.GroupVersionResource{Group: SupervisorNamespaceGroup, Version: "v1alpha2", Resource: "supervisornamespaces"},
		UserInfo:  authenticationv1.UserInfo{Username: ""},
		OldObject: runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"tenant"},"spec":{"vpcNetworkConfig":{"target":"old"}}}`)},
		Object:    runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"tenant"},"spec":{"vpcNetworkConfig":{"target":"new"}}}`)},
	}}
	require.False(t, validator.Handle(context.Background(), request).Allowed)
}

func TestNamespaceLifecycleValidatorSupportsV1Alpha2NetworkingUnion(t *testing.T) {
	t.Run("Stage 1 rejects nested VPC configuration", func(t *testing.T) {
		scheme := runtime.NewScheme()
		require.NoError(t, api.AddToScheme(scheme))
		validator := NamespaceLifecycleValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).Build()}
		request := supervisorNamespaceRequest(admissionv1.Create,
			`{"metadata":{"name":"tenant"},"spec":{"networking":{"vpcNetworkConfig":{}}}}`, "")
		response := validator.Handle(context.Background(), request)
		require.False(t, response.Allowed)
		require.Contains(t, response.Result.Message, "Stage 1 is not complete")

		request = supervisorNamespaceRequest(admissionv1.Create,
			`{"metadata":{"name":"tenant"},"spec":{"vpcNetworkConfig":{}}}`, "")
		response = validator.Handle(context.Background(), request)
		require.False(t, response.Allowed, "the legacy top-level VPC config remains protected")
	})

	t.Run("coexistence rejects explicitly empty nested VDS configuration", func(t *testing.T) {
		scheme := runtime.NewScheme()
		require.NoError(t, api.AddToScheme(scheme))
		infra := &api.ControlPlaneNetworkTransition{
			ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName},
			Status:     &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{Phase: api.PhaseInfraTransitionComplete}},
		}
		validator := NamespaceLifecycleValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(infra).Build()}
		request := supervisorNamespaceRequest(admissionv1.Create,
			`{"metadata":{"name":"tenant"},"spec":{"networking":{"vsphereNetworkConfig":{}}}}`, "")
		response := validator.Handle(context.Background(), request)
		require.False(t, response.Allowed)
		require.Contains(t, response.Result.Message, "coexistence mode")

		request = supervisorNamespaceRequest(admissionv1.Create,
			`{"metadata":{"name":"tenant"},"spec":{"vsphereNetworkConfig":{}}}`, "")
		response = validator.Handle(context.Background(), request)
		require.False(t, response.Allowed, "the legacy top-level VDS config remains protected")
	})

	t.Run("blocked Finalize retries preserve coexistence namespace policy", func(t *testing.T) {
		scheme := runtime.NewScheme()
		require.NoError(t, api.AddToScheme(scheme))
		infra := &api.ControlPlaneNetworkTransition{
			ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 5},
			Spec:       api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionFinalize}},
			Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
				LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded, ActionGeneration: 4, Phase: api.PhaseBlocked,
				Conditions: []metav1.Condition{{Type: api.ConditionInfraTransitionComplete, Status: metav1.ConditionTrue, ObservedGeneration: 4}},
			}},
		}
		validator := NamespaceLifecycleValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(infra).Build()}

		vpcRequest := supervisorNamespaceRequest(admissionv1.Create,
			`{"metadata":{"name":"tenant"},"spec":{"vpcNetworkConfig":{}}}`, "")
		require.True(t, validator.Handle(context.Background(), vpcRequest).Allowed,
			"the completed infrastructure milestone keeps VPC namespace creation available during Finalize retries")

		vdsRequest := supervisorNamespaceRequest(admissionv1.Create,
			`{"metadata":{"name":"tenant"},"spec":{"vsphereNetworkConfig":{}}}`, "")
		response := validator.Handle(context.Background(), vdsRequest)
		require.False(t, response.Allowed, "explicit VDS requests remain rejected during coexistence")
		require.Contains(t, response.Result.Message, "coexistence mode")
	})

	t.Run("controller may switch the nested provider union only", func(t *testing.T) {
		scheme := runtime.NewScheme()
		require.NoError(t, api.AddToScheme(scheme))
		transition := &api.NamespaceNetworkTransition{
			ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: 3},
			Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"tenant"}},
			Status:     &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: api.ActionExecute, ActionState: api.ActionStateRunning, ActionGeneration: 3, ObservedGeneration: 3}},
		}
		validator := NamespaceLifecycleValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(transition).Build(), ControllerUser: "controller"}
		oldObject := `{"metadata":{"name":"tenant"},"spec":{"networking":{"vsphereNetworkConfig":{}},"other":"same"}}`
		newObject := `{"metadata":{"name":"tenant"},"spec":{"networking":{"vpcNetworkConfig":{}},"other":"same"}}`
		request := supervisorNamespaceRequest(admissionv1.Update, newObject, "controller")
		request.OldObject = runtime.RawExtension{Raw: []byte(oldObject)}
		require.True(t, validator.Handle(context.Background(), request).Allowed)

		request.Object = runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"tenant"},"spec":{"networking":{"vpcNetworkConfig":{}},"other":"changed"}}`)}
		require.False(t, validator.Handle(context.Background(), request).Allowed)

		request.OldObject = runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"tenant"},"spec":{"vsphereNetworkConfig":{},"other":"same"}}`)}
		request.Object = runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"tenant"},"spec":{"vpcNetworkConfig":{},"other":"same"}}`)}
		require.True(t, validator.Handle(context.Background(), request).Allowed, "the legacy provider fields remain mutable by the migration controller")
	})
}

func supervisorNamespaceRequest(operation admissionv1.Operation, object, username string) admission.Request {
	return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Name: "tenant", Operation: operation,
		Resource: metav1.GroupVersionResource{Group: SupervisorNamespaceGroup, Version: "v1alpha2", Resource: "supervisornamespaces"},
		UserInfo: authenticationv1.UserInfo{Username: username},
		Object:   runtime.RawExtension{Raw: []byte(object)},
	}}
}

func TestNamespaceProviderDetectionHelpers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		object  *unstructured.Unstructured
		wantVDS bool
		wantVPC bool
	}{
		{name: "nil object"},
		{name: "VDS provider nested in spec", object: &unstructured.Unstructured{Object: map[string]interface{}{"spec": map[string]interface{}{"provider": []interface{}{"other", map[string]interface{}{"networkProvider": "VSPHERE_NETWORK"}}}}}, wantVDS: true},
		{name: "VPC provider nested in spec", object: &unstructured.Unstructured{Object: map[string]interface{}{"spec": map[string]interface{}{"provider": map[string]interface{}{"name": "VPC_LITE"}}}}, wantVPC: true},
		{name: "VDS portgroup", object: &unstructured.Unstructured{Object: map[string]interface{}{"spec": map[string]interface{}{"networking": map[string]interface{}{"PortGroupName": "dvpg-a"}}}}, wantVDS: true},
		{name: "empty VDS fields do not count", object: &unstructured.Unstructured{Object: map[string]interface{}{"spec": map[string]interface{}{"network": "", "networks": []interface{}{}, "portgroup": nil}}}},
		{name: "network nested under VPC config is ignored", object: &unstructured.Unstructured{Object: map[string]interface{}{"spec": map[string]interface{}{"vpcNetworkConfig": map[string]interface{}{"network": "vpc-segment"}}}}},
		{name: "legacy provider config", object: &unstructured.Unstructured{Object: map[string]interface{}{"spec": map[string]interface{}{"vsphereNetworkConfig": map[string]interface{}{}}}}, wantVDS: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wantVDS, explicitlyRequestsVDS(tc.object))
			require.Equal(t, tc.wantVPC, explicitlyRequestsVPC(tc.object))
		})
	}
	require.False(t, hasVPCNetworkConfig(nil))
	require.True(t, hasVPCNetworkConfig(&unstructured.Unstructured{Object: map[string]interface{}{"spec": map[string]interface{}{"networking": map[string]interface{}{"vpcNetworkConfig": map[string]interface{}{}}}}}))
	require.True(t, specContainsNetworkProvider(&unstructured.Unstructured{Object: map[string]interface{}{"spec": "VPC_LITE"}}, "VPC_LITE"))
	require.False(t, specContainsNetworkProvider(&unstructured.Unstructured{Object: map[string]interface{}{}}, "VPC_LITE"))
}

func TestNamespaceConfiguredNetworkFieldHelpers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value interface{}
		want  bool
	}{
		{name: "nil"},
		{name: "empty string", value: ""},
		{name: "configured string", value: "network-a", want: true},
		{name: "empty list", value: []interface{}{}},
		{name: "configured list", value: []interface{}{"network-a"}, want: true},
		{name: "empty map", value: map[string]interface{}{}},
		{name: "configured map", value: map[string]interface{}{"name": "network-a"}, want: true},
		{name: "scalar", value: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, hasConfiguredValue(tc.value)) })
	}
	for _, tc := range []struct {
		name string
		key  string
		want bool
	}{
		{name: "case insensitive network", key: "Networks", want: true},
		{name: "portgroup", key: "portgroup", want: true},
		{name: "portgroup name", key: "portgroupName", want: true},
		{name: "distributed portgroup", key: "distributedPortGroup", want: true},
		{name: "dvpg", key: "dvpg", want: true},
		{name: "unrelated key", key: "segment"},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, isVDSNetworkField(tc.key)) })
	}
	require.True(t, containsVDSNetworkField([]interface{}{map[string]interface{}{"network": "dvpg-a"}}))
	require.False(t, containsVDSNetworkField(map[string]interface{}{"VPCNETWORKCONFIG": map[string]interface{}{"network": "vpc-a"}}))
}

func TestNamespaceCoexistenceReaderOutcomes(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	ctx := context.Background()
	validator := NamespaceLifecycleValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).Build()}
	coexistence, err := validator.inCoexistenceMode(ctx)
	require.NoError(t, err)
	require.False(t, coexistence, "a missing infrastructure transition means Stage 1 is incomplete")

	for _, tc := range []struct {
		name       string
		phase      string
		action     api.Action
		lastAction api.Action
		conditions []metav1.Condition
		want       bool
	}{
		{name: "infra complete", phase: api.PhaseInfraTransitionComplete, want: true},
		{name: "finalizing with completion condition", phase: api.PhaseFinalizing, conditions: []metav1.Condition{{Type: api.ConditionInfraTransitionComplete, Status: metav1.ConditionTrue}}, want: true},
		{name: "blocked before finalize checkpoint retains coexistence", phase: api.PhaseBlocked, action: api.ActionFinalize, lastAction: api.ActionExecute, conditions: []metav1.Condition{{Type: api.ConditionInfraTransitionComplete, Status: metav1.ConditionTrue}}, want: true},
		{name: "blocked finalize retry retains coexistence", phase: api.PhaseBlocked, action: api.ActionFinalize, lastAction: api.ActionFinalize, conditions: []metav1.Condition{{Type: api.ConditionInfraTransitionComplete, Status: metav1.ConditionTrue}}, want: true},
		{name: "failed finalization with completion condition", phase: api.PhaseFailed, action: api.ActionFinalize, lastAction: api.ActionFinalize, conditions: []metav1.Condition{{Type: api.ConditionInfraTransitionComplete, Status: metav1.ConditionTrue}}, want: true},
		{name: "blocked finalize without completion condition", phase: api.PhaseBlocked, action: api.ActionFinalize, lastAction: api.ActionFinalize, conditions: []metav1.Condition{{Type: api.ConditionInfraTransitionComplete, Status: metav1.ConditionFalse}}},
		{name: "blocked non-finalize with stale completion condition", phase: api.PhaseBlocked, action: api.ActionEvaluate, lastAction: api.ActionEvaluate, conditions: []metav1.Condition{{Type: api.ConditionInfraTransitionComplete, Status: metav1.ConditionTrue}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transition := &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName}, Spec: api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: tc.action}}, Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{Phase: tc.phase, LastAction: tc.lastAction, Conditions: tc.conditions}}}
			validator := NamespaceLifecycleValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(transition).Build()}
			got, err := validator.inCoexistenceMode(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
	reader := getErrorReader{Reader: validator.Reader, err: errors.New("reader unavailable")}
	_, err = (NamespaceLifecycleValidator{Reader: reader}).inCoexistenceMode(ctx)
	require.ErrorContains(t, err, "reader unavailable")
}

func TestNamespaceLifecycleHandleValidationAndUnprotectedPaths(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	reader := fake.NewClientBuilder().WithScheme(scheme).Build()
	validator := NamespaceLifecycleValidator{Reader: reader}
	request := supervisorNamespaceRequest(admissionv1.Create, `{"metadata":{"name":"tenant"},"spec":{}}`, "")

	request.SubResource = "status"
	require.True(t, validator.Handle(context.Background(), request).Allowed)
	request.SubResource = ""
	request.Resource.Group = "wrong.example.com"
	require.EqualValues(t, 400, validator.Handle(context.Background(), request).Result.Code)
	request.Resource.Group = SupervisorNamespaceGroup
	request.Name = ""
	require.EqualValues(t, 400, validator.Handle(context.Background(), request).Result.Code)
	request.Name = "tenant"
	require.EqualValues(t, 503, (NamespaceLifecycleValidator{}).Handle(context.Background(), request).Result.Code)

	request.Operation = admissionv1.Update
	request.OldObject = runtime.RawExtension{Raw: []byte("{")}
	require.EqualValues(t, 400, validator.Handle(context.Background(), request).Result.Code)
	request.Operation = admissionv1.Create
	request.Object = runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"tenant"},"spec":{"network":"dvpg-a"}}`)}
	require.True(t, validator.Handle(context.Background(), request).Allowed, "unprotected Stage 1 namespaces can use VDS")
	request.Object = runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"tenant"},"spec":{"networkProvider":"vpc"}}`)}
	require.False(t, validator.Handle(context.Background(), request).Allowed, "unprotected namespaces cannot request VPC before Stage 1 completes")

	request.Operation = admissionv1.Delete
	require.True(t, validator.Handle(context.Background(), request).Allowed, "unprotected namespaces may be deleted")
}

func TestNamespaceLifecycleAcceptsVersionsForTheSupervisorNamespaceResource(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	validator := NamespaceLifecycleValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).Build()}
	request := supervisorNamespaceRequest(admissionv1.Create, `{"metadata":{"name":"tenant"},"spec":{}}`, "")
	request.Resource.Version = "v1beta1"
	response := validator.Handle(context.Background(), request)
	require.True(t, response.Allowed, "the API version is not part of the stable group/resource identity")
}

func TestNamespaceLifecycleProtectsUnmanagedProviderUpdates(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))

	t.Run("adding VPC config before Stage 1 completes is denied", func(t *testing.T) {
		validator := NamespaceLifecycleValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).Build()}
		request := supervisorNamespaceRequest(admissionv1.Update,
			`{"metadata":{"name":"tenant"},"spec":{"vpcNetworkConfig":{"target":"vpc-a"}}}`, "user")
		request.OldObject = runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"tenant"},"spec":{"network":"dvpg-a"}}`)}
		response := validator.Handle(context.Background(), request)
		require.False(t, response.Allowed)
		require.Contains(t, response.Result.Message, "Stage 1 is not complete")
	})

	t.Run("metadata updates do not revalidate an unchanged provider config", func(t *testing.T) {
		validator := NamespaceLifecycleValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).Build()}
		oldObject := `{"metadata":{"name":"tenant"},"spec":{"vpcNetworkConfig":{"target":"vpc-a"}}}`
		newObject := `{"metadata":{"name":"tenant","labels":{"owner":"team-a"}},"spec":{"vpcNetworkConfig":{"target":"vpc-a"}}}`
		request := supervisorNamespaceRequest(admissionv1.Update, newObject, "user")
		request.OldObject = runtime.RawExtension{Raw: []byte(oldObject)}
		require.True(t, validator.Handle(context.Background(), request).Allowed)
	})

	t.Run("unmigrated VDS cannot switch to VPC outside a transition", func(t *testing.T) {
		validator := NamespaceLifecycleValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(readyInfrastructureTransition()).Build()}
		request := supervisorNamespaceRequest(admissionv1.Update,
			`{"metadata":{"name":"tenant"},"spec":{"vpcNetworkConfig":{"target":"vpc-a"}}}`, "user")
		request.OldObject = runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"tenant"},"spec":{"vsphereNetworkConfig":{"network":"dvpg-a"}}}`)}
		response := validator.Handle(context.Background(), request)
		require.False(t, response.Allowed)
		require.Contains(t, response.Result.Message, "active NamespaceNetworkTransition")
	})

	t.Run("implicit Stage 1 VDS cannot switch to VPC outside a transition", func(t *testing.T) {
		validator := NamespaceLifecycleValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(readyInfrastructureTransition()).Build()}
		request := supervisorNamespaceRequest(admissionv1.Update,
			`{"metadata":{"name":"tenant"},"spec":{"vpcNetworkConfig":{"target":"vpc-a"}}}`, "user")
		request.OldObject = runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"tenant"},"spec":{}}`)}
		response := validator.Handle(context.Background(), request)
		require.False(t, response.Allowed)
		require.Contains(t, response.Result.Message, "active NamespaceNetworkTransition")
	})

	t.Run("existing VPC namespace may update its VPC configuration", func(t *testing.T) {
		validator := NamespaceLifecycleValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(readyInfrastructureTransition()).Build()}
		request := supervisorNamespaceRequest(admissionv1.Update,
			`{"metadata":{"name":"tenant"},"spec":{"vpcNetworkConfig":{"target":"vpc-b"}}}`, "user")
		request.OldObject = runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"tenant"},"spec":{"vpcNetworkConfig":{"target":"vpc-a"}}}`)}
		require.True(t, validator.Handle(context.Background(), request).Allowed)
	})
}

func TestNamespaceLifecycleHandleProtectionAndReaderFailure(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	transition := &api.NamespaceNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: 2},
		Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"tenant"}},
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(transition).Build()
	validator := NamespaceLifecycleValidator{Reader: reader, ControllerUser: "controller"}
	request := supervisorNamespaceRequest(admissionv1.Create, `{"metadata":{"name":"tenant"},"spec":{}}`, "user")
	require.False(t, validator.Handle(context.Background(), request).Allowed, "creating into an unresolved batch is forbidden")
	request.Operation = admissionv1.Delete
	require.False(t, validator.Handle(context.Background(), request).Allowed, "deleting from an unresolved batch is forbidden")
	request.Operation = admissionv1.Update
	request.OldObject = runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"tenant"},"spec":{"other":"before"}}`)}
	request.Object = runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"tenant"},"spec":{"other":"after"}}`)}
	require.False(t, validator.Handle(context.Background(), request).Allowed, "ordinary users cannot change a protected namespace")

	brokenReader := listErrorReader{Reader: reader, err: errors.New("namespace list unavailable")}
	response := (NamespaceLifecycleValidator{Reader: brokenReader}).Handle(context.Background(), request)
	require.EqualValues(t, 503, response.Result.Code)
	require.Contains(t, response.Result.Message, "namespace list unavailable")
}

type listErrorReader struct {
	client.Reader
	err error
}

func (r listErrorReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return r.err
}
