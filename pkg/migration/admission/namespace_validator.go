/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package admission

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

const (
	NamespaceValidationPath           = "/validate-migration-namespace"
	SupervisorNamespaceGroup          = "iaas.vmware.com"
	MigrationControllerServiceAccount = "ncp-svc-account"
)

// NamespaceLifecycleValidator applies Stage 1/Stage 2 namespace admission
// policy and protects namespaces in an unresolved namespace batch. Reads use
// the API reader so admission decisions do not depend on a stale controller cache.
type NamespaceLifecycleValidator struct {
	Reader         client.Reader
	ControllerUser string
}

func (v NamespaceLifecycleValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	if req.SubResource != "" || (req.Operation != admissionv1.Create && req.Operation != admissionv1.Update && req.Operation != admissionv1.Delete) {
		return admission.Allowed("not a SupervisorNamespace lifecycle request")
	}
	isNamespace := req.Resource.Group == "" && req.Resource.Resource == "namespaces"
	if !isNamespace && (req.Resource.Group != SupervisorNamespaceGroup || req.Resource.Resource != "supervisornamespaces") {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("unexpected namespace API resource %s", req.Resource.String()))
	}
	if isNamespace && req.Operation == admissionv1.Update {
		return admission.Allowed("not a Kubernetes Namespace create or delete request")
	}
	if v.Reader == nil {
		return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("namespace lifecycle reader is not configured"))
	}
	var object, oldObject *unstructured.Unstructured
	if req.Operation == admissionv1.Create || req.Operation == admissionv1.Update {
		object = &unstructured.Unstructured{}
		if err := json.Unmarshal(req.Object.Raw, &object.Object); err != nil {
			return admission.Errored(http.StatusBadRequest, fmt.Errorf("decoding SupervisorNamespace: %w", err))
		}
	}
	if req.Operation == admissionv1.Update || req.Operation == admissionv1.Delete {
		oldObject = &unstructured.Unstructured{}
		if err := json.Unmarshal(req.OldObject.Raw, &oldObject.Object); err != nil {
			return admission.Errored(http.StatusBadRequest, fmt.Errorf("decoding old SupervisorNamespace: %w", err))
		}
	}

	name := req.Name
	if name == "" && object != nil {
		name = object.GetName()
	}
	if name == "" && oldObject != nil {
		name = oldObject.GetName()
	}
	// A generated name cannot be matched against a transition's explicit namespace
	// list before the API server assigns the final name. Continue with cluster-wide
	// Stage 1/provider checks for this new object.
	hasGeneratedName := req.Operation == admissionv1.Create && object != nil && object.GetGenerateName() != ""
	if name == "" && !hasGeneratedName {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("SupervisorNamespace name is required"))
	}
	if req.Operation == admissionv1.Create || req.Operation == admissionv1.Delete {
		if name != "" {
			protection, err := findNamespaceProtectionFor(ctx, v.Reader, name, namespaceTransitionPinsNamespace)
			if err != nil {
				return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("checking namespace lifecycle protection: %w", err))
			}
			if protection != nil {
				return admission.Denied(fmt.Sprintf("namespace %q cannot be created or deleted while transition %s remains uncommitted", name, protection.name))
			}
		}
		if isNamespace || req.Operation == admissionv1.Delete {
			return admission.Allowed("namespace is outside uncommitted migration scope")
		}
	}

	var protectedBy *namespaceProtection
	if name != "" {
		var err error
		protectedBy, err = v.protectingTransition(ctx, name)
		if err != nil {
			return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("checking namespace migration protection: %w", err))
		}
	}
	if protectedBy != nil {
		if req.Operation == admissionv1.Create {
			return admission.Denied(fmt.Sprintf("SupervisorNamespace %q is protected by unresolved migration transition %s", name, protectedBy.name))
		}
		if req.UserInfo.Username != v.ControllerUser || v.ControllerUser == "" || !protectedBy.controllerMayChange ||
			oldObject.GetUID() == "" || oldObject.GetUID() != object.GetUID() ||
			!oldObject.GetDeletionTimestamp().IsZero() || !object.GetDeletionTimestamp().IsZero() ||
			!onlyVPCNetworkConfigChanged(oldObject, object) {
			return admission.Denied(fmt.Sprintf("SupervisorNamespace %q is protected by unresolved migration transition %s; only its migration controller may change the network provider configuration", name, protectedBy.name))
		}
		return admission.Allowed("designated migration controller changed only network provider configuration")
	}

	if req.Operation == admissionv1.Create {
		requestsVDS := explicitlyRequestsVDS(object)
		requestsVPC := hasVPCNetworkConfig(object) || explicitlyRequestsVPC(object)

		coexistence, err := v.inCoexistenceMode(ctx)
		if err != nil {
			return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("checking Stage 1 completion: %w", err))
		}
		if !coexistence && requestsVPC {
			return admission.Denied("Stage 1 is not complete; new namespaces must use VDS networking")
		}
		if coexistence && requestsVDS {
			return admission.Denied("Cluster is in NSX VPC migration coexistence mode; new namespaces must use VPC network configuration")
		}
		if coexistence {
			return admission.Allowed("new namespace defaults to VPC in coexistence mode")
		}
		return admission.Allowed("new namespace defaults to VDS during Stage 1")
	}
	if req.Operation == admissionv1.Update && namespaceProviderConfigurationChanged(oldObject, object) {
		coexistence, err := v.inCoexistenceMode(ctx)
		if err != nil {
			return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("checking namespace provider update: %w", err))
		}
		oldVPC := hasVPCNetworkConfig(oldObject) || explicitlyRequestsVPC(oldObject)
		newVPC := hasVPCNetworkConfig(object) || explicitlyRequestsVPC(object)
		oldVDS := explicitlyRequestsVDS(oldObject)
		newVDS := explicitlyRequestsVDS(object)
		if !coexistence && newVPC {
			return admission.Denied("Stage 1 is not complete; namespaces cannot switch to VPC networking")
		}
		if coexistence && (oldVDS || newVDS || (!oldVPC && newVPC)) {
			return admission.Denied("namespace provider changes during coexistence must be performed by an active NamespaceNetworkTransition")
		}
	}
	// Existing, unprotected namespaces remain usable. The migration-scope guard
	// above blocks conflicting edits only for namespaces in unresolved batches.
	return admission.Allowed("namespace is not in an unresolved migration scope")
}

func namespaceProviderConfigurationChanged(oldObject, newObject *unstructured.Unstructured) bool {
	oldVPC := hasVPCNetworkConfig(oldObject) || explicitlyRequestsVPC(oldObject)
	newVPC := hasVPCNetworkConfig(newObject) || explicitlyRequestsVPC(newObject)
	oldVDS := explicitlyRequestsVDS(oldObject)
	newVDS := explicitlyRequestsVDS(newObject)
	return networkProviderConfigChanged(oldObject, newObject) || oldVPC != newVPC || oldVDS != newVDS
}

type namespaceProtection struct {
	name                string
	controllerMayChange bool
	transition          *api.NamespaceNetworkTransition
}

func (v NamespaceLifecycleValidator) protectingTransition(ctx context.Context, namespace string) (*namespaceProtection, error) {
	return findNamespaceProtection(ctx, v.Reader, namespace)
}

func namespaceTransitionProtects(transition *api.NamespaceNetworkTransition) bool {
	if !namespaceTransitionPinsNamespace(transition) {
		return false
	}
	if transition == nil || transition.DeletionTimestamp != nil {
		return transition != nil
	}
	status := transition.Status
	if status != nil {
		// A new Execute or Rollback always reactivates protection, including
		// the period before its controller has recorded the new checkpoint.
		if transition.Spec.Action == api.ActionCommit {
			if status.LastAction == api.ActionCommit && status.ActionState == api.ActionStateSucceeded &&
				status.ActionGeneration > 0 && status.ActionGeneration == transition.Generation &&
				status.ObservedGeneration == transition.Generation && status.Phase == api.PhaseCommitted &&
				status.IrreversibleAction == string(api.ActionCommit) {
				return false
			}
			return true
		}
		if transition.Spec.Action == api.ActionEvaluate && status.RollbackComplete &&
			status.LastAction == api.ActionEvaluate && status.ActionGeneration == transition.Generation &&
			status.ObservedGeneration == transition.Generation {
			return false
		}
		checkpointGenerationMatches := status.ActionGeneration > 0 && status.ObservedGeneration == status.ActionGeneration &&
			(status.ActionGeneration == transition.Generation ||
				(status.ActionGeneration+1 == transition.Generation &&
					((transition.Spec.Action == api.ActionCommit && status.LastAction == api.ActionExecute) ||
						(transition.Spec.Action == api.ActionEvaluate && status.LastAction == api.ActionRollback))))
		currentSuccess := checkpointGenerationMatches && status.ActionState == api.ActionStateSucceeded
		switch status.LastAction {
		case api.ActionExecute:
			return transition.Spec.Action != api.ActionExecute || !currentSuccess || status.Phase != api.PhaseCompleted
		case api.ActionRollback:
			return (transition.Spec.Action != api.ActionRollback && transition.Spec.Action != api.ActionEvaluate) || !currentSuccess || status.Phase != api.PhaseRolledBack
		case api.ActionCommit:
			return true
		}
	}
	// The requested action is persisted before the controller's Running status
	// checkpoint, so Execute/Rollback protect the namespace during that gap too.
	switch transition.Spec.Action {
	case api.ActionExecute:
		return true
	case api.ActionRollback:
		return true
	case api.ActionCommit:
		return true
	default:
		return false
	}
}

// Namespace existence stays protected even after Execute or Rollback completes:
// both can be followed by more work on the same immutable namespace batch.
// Historical committed records no longer pin namespaces, including during CR deletion.
func namespaceTransitionPinsNamespace(batch *api.NamespaceNetworkTransition) bool {
	return batch != nil && (batch.Spec.Action != api.ActionCommit || batch.Status == nil ||
		!currentSuccessfulCheckpoint(&batch.Status.TransitionStatus, batch.Generation, api.ActionCommit) ||
		batch.Status.ObservedGeneration != batch.Generation || batch.Status.Phase != api.PhaseCommitted ||
		batch.Status.IrreversibleAction != string(api.ActionCommit))
}

func namespaceControllerMayChange(transition *api.NamespaceNetworkTransition) bool {
	// Controller writes begin only after the matching action checkpoint is stored.
	if transition == nil || transition.Status == nil || transition.Status.ActionGeneration != transition.Generation ||
		transition.Status.ObservedGeneration != transition.Generation {
		return false
	}
	status := &transition.Status.TransitionStatus
	return status.LastAction == transition.Spec.Action &&
		(transition.Spec.Action == api.ActionExecute || transition.Spec.Action == api.ActionRollback) &&
		(status.ActionState == api.ActionStateRunning || status.ActionState == api.ActionStateFailed)
}

func (v NamespaceLifecycleValidator) inCoexistenceMode(ctx context.Context) (bool, error) {
	transition := &api.ControlPlaneNetworkTransition{}
	err := v.Reader.Get(ctx, types.NamespacedName{Name: api.ControlPlaneNetworkTransitionName}, transition)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if transition.Status == nil {
		return false, nil
	}
	status := &transition.Status.TransitionStatus
	if status.Phase == api.PhaseInfraTransitionComplete || status.Phase == api.PhaseFinalizing || status.Phase == api.PhaseFinalized {
		return true, nil
	}
	if status.Phase == api.PhaseFailed && status.LastAction == api.ActionFinalize {
		for _, condition := range status.Conditions {
			if condition.Type == api.ConditionInfraTransitionComplete && condition.Status == "True" {
				return true, nil
			}
		}
	}
	return false, nil
}

func containsNamespace(namespaces []string, name string) bool {
	for _, candidate := range namespaces {
		if candidate == name {
			return true
		}
	}
	return false
}

func explicitlyRequestsVDS(object *unstructured.Unstructured) bool {
	if object == nil {
		return false
	}
	if hasNonNilField(object, "spec", "vsphereNetworkConfig") ||
		hasNonNilField(object, "spec", "networking", "vsphereNetworkConfig") {
		return true
	}
	if specContainsNetworkProvider(object, "vsphere-distributed", "VSPHERE_NETWORK") {
		return true
	}
	// SupervisorNamespace versions represent explicit VDS backing as a
	// non-empty top-level network/portgroup selection. Ignore networks nested
	// inside vpcNetworkConfig, which is the VPC desired-configuration field.
	spec, found, _ := unstructured.NestedMap(object.Object, "spec")
	if !found {
		return false
	}
	return containsVDSNetworkField(spec)
}

func explicitlyRequestsVPC(object *unstructured.Unstructured) bool {
	return specContainsNetworkProvider(object, "vpc", "NSX_VPC", "VPC_LITE")
}

func specContainsNetworkProvider(object *unstructured.Unstructured, providers ...string) bool {
	if object == nil {
		return false
	}
	spec, found, _ := unstructured.NestedFieldNoCopy(object.Object, "spec")
	if !found {
		return false
	}
	return containsProviderValue(spec, providers)
}

func hasVPCNetworkConfig(object *unstructured.Unstructured) bool {
	if object == nil {
		return false
	}
	return hasNonNilField(object, "spec", "vpcNetworkConfig") ||
		hasNonNilField(object, "spec", "networking", "vpcNetworkConfig")
}

func hasNonNilField(object *unstructured.Unstructured, fields ...string) bool {
	value, found, _ := unstructured.NestedFieldNoCopy(object.Object, fields...)
	return found && value != nil
}

func containsProviderValue(value interface{}, providers []string) bool {
	switch typed := value.(type) {
	case string:
		for _, provider := range providers {
			if typed == provider {
				return true
			}
		}
	case map[string]interface{}:
		for _, nested := range typed {
			if containsProviderValue(nested, providers) {
				return true
			}
		}
	case []interface{}:
		for _, nested := range typed {
			if containsProviderValue(nested, providers) {
				return true
			}
		}
	}
	return false
}

func isVDSNetworkField(key string) bool {
	switch strings.ToLower(key) {
	case "networks", "network", "portgroup", "portgroupname", "distributedportgroup", "dvpg":
		return true
	default:
		return false
	}
}

func containsVDSNetworkField(value interface{}) bool {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, nested := range typed {
			if strings.EqualFold(key, "vpcNetworkConfig") {
				continue
			}
			if isVDSNetworkField(key) && hasConfiguredValue(nested) {
				return true
			}
			if containsVDSNetworkField(nested) {
				return true
			}
		}
	case []interface{}:
		for _, nested := range typed {
			if containsVDSNetworkField(nested) {
				return true
			}
		}
	}
	return false
}

func hasConfiguredValue(value interface{}) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return typed != ""
	case []interface{}:
		return len(typed) != 0
	case map[string]interface{}:
		return len(typed) != 0
	default:
		return true
	}
}

func onlyVPCNetworkConfigChanged(oldObject, newObject *unstructured.Unstructured) bool {
	if oldObject == nil || newObject == nil {
		return false
	}
	if !networkProviderConfigChanged(oldObject, newObject) {
		return false
	}
	oldValue := oldObject.DeepCopy().Object
	newValue := newObject.DeepCopy().Object
	for _, object := range []map[string]interface{}{oldValue, newValue} {
		metadata, found, _ := unstructured.NestedMap(object, "metadata")
		if found {
			for _, key := range []string{"resourceVersion", "generation", "managedFields", "creationTimestamp"} {
				delete(metadata, key)
			}
			object["metadata"] = metadata
		}
		spec, found, _ := unstructured.NestedMap(object, "spec")
		if found {
			delete(spec, "vpcNetworkConfig")
			delete(spec, "vsphereNetworkConfig")
			if networking, ok := spec["networking"].(map[string]interface{}); ok {
				delete(networking, "vpcNetworkConfig")
				delete(networking, "vsphereNetworkConfig")
			}
			object["spec"] = spec
		}
	}
	return reflect.DeepEqual(oldValue, newValue)
}

func networkProviderConfigChanged(oldObject, newObject *unstructured.Unstructured) bool {
	for _, path := range [][]string{
		{"spec", "vpcNetworkConfig"},
		{"spec", "vsphereNetworkConfig"},
		{"spec", "networking", "vpcNetworkConfig"},
		{"spec", "networking", "vsphereNetworkConfig"},
	} {
		oldConfig, oldFound, _ := unstructured.NestedFieldNoCopy(oldObject.Object, path...)
		newConfig, newFound, _ := unstructured.NestedFieldNoCopy(newObject.Object, path...)
		if oldFound != newFound || !reflect.DeepEqual(oldConfig, newConfig) {
			return true
		}
	}
	return false
}
