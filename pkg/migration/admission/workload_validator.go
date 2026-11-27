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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

const WorkloadValidationPath = "/validate-migration-workload"

const (
	wcpSharedT1Annotation        = "vmware-system-shared-t1"
	sharedVPCNamespaceAnnotation = "nsx.vmware.com/shared_vpc_namespace"
)

const (
	NCPControllerUser         = "system:serviceaccount:vmware-system-nsx:ncp-svc-account"
	NetOperatorControllerUser = "system:serviceaccount:vmware-system-netop:vmware-system-netop-controller-manager"
	VMOperatorControllerUser  = "system:serviceaccount:vmware-system-vmop:vmware-system-vmop-vmoperator-service-account"
	AKOControllerUser         = "system:serviceaccount:vmware-system-ako:vmware-system-ako-controller-manager"
)

// WorkloadCutoverValidator blocks workload mutations in namespace batches and
// system namespaces with unresolved infrastructure Execute. Reads use the
// direct API reader so protection is not based on a stale manager cache.
type WorkloadCutoverValidator struct {
	Reader client.Reader
	// ExpectedVMUpdate resolves the VM state authorized by trusted migration
	// state. Implementations receive object identity only, never the incoming
	// object, so they cannot use the requested values as their own expectation.
	// A nil resolver or nil result fails closed.
	ExpectedVMUpdate func(ctx context.Context, namespace, name string, uid types.UID, transition string) (*ExpectedVMUpdate, error)
}

// ExpectedVMUpdate contains the only VM fields Net Operator may change during
// a protected migration update. NetworkInterfaces must be supplied from
// trusted transition state; a nil slice means no trusted expectation exists.
type ExpectedVMUpdate struct {
	NetworkInterfaces    []interface{}
	TransitionAnnotation *string
}

func (v WorkloadCutoverValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	if req.SubResource != "" || (req.Operation != admissionv1.Create && req.Operation != admissionv1.Update && req.Operation != admissionv1.Delete) {
		return admission.Allowed("not a workload mutation")
	}
	resource, relevant := workloadResource(req)
	if !relevant {
		return admission.Allowed("resource is outside workload cutover protection")
	}
	if req.Namespace == "" {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("workload namespace is required"))
	}
	if v.Reader == nil {
		return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("workload protection reader is not configured"))
	}

	oldObject, newObject, err := decodeWorkloadObjects(req)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	if resource == "services" && !loadBalancerService(oldObject) && !loadBalancerService(newObject) {
		return admission.Allowed("Service is not or was not type LoadBalancer")
	}

	protection, err := findWorkloadProtection(ctx, v.Reader, req.Namespace)
	if err != nil {
		return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("checking workload migration protection: %w", err))
	}
	if protection == nil {
		return admission.Allowed("namespace is outside unresolved migration scope")
	}
	if req.Operation == admissionv1.Update && protection.controllerMayChange &&
		isWorkloadController(req.UserInfo.Username) && oldObject.GetUID() != "" &&
		oldObject.GetUID() == newObject.GetUID() {
		var expected *ExpectedVMUpdate
		if resource == "virtualmachines" && req.UserInfo.Username == NetOperatorControllerUser && v.ExpectedVMUpdate != nil {
			var resolveErr error
			expected, resolveErr = v.ExpectedVMUpdate(ctx, req.Namespace, oldObject.GetName(), oldObject.GetUID(), protection.name)
			if resolveErr != nil {
				return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("resolving expected VM migration update: %w", resolveErr))
			}
		}
		if expected != nil && onlyAllowedWorkloadFieldsChanged(req.UserInfo.Username, resource, oldObject, newObject, expected) {
			return admission.Allowed("checkpoint-authorized controller workload update")
		}
	}
	return admission.Denied(fmt.Sprintf("%s mutation in namespace %q is blocked by unresolved migration transition %s", resource, req.Namespace, protection.name))
}

// Only the Net Operator VM update described here has a field-level exception.
// Other workload-controller writes remain denied until their exact fields and
// values are sourced from trusted migration state.
func onlyAllowedWorkloadFieldsChanged(username, resource string, oldObject, newObject *unstructured.Unstructured, expected *ExpectedVMUpdate) bool {
	if username != NetOperatorControllerUser || resource != "virtualmachines" {
		return false
	}
	if expected == nil || expected.NetworkInterfaces == nil {
		return false
	}
	interfaces, found, err := unstructured.NestedSlice(newObject.Object, "spec", "networkInterfaces")
	if err != nil || !found || !reflect.DeepEqual(interfaces, expected.NetworkInterfaces) {
		return false
	}
	value, found, err := unstructured.NestedString(newObject.Object, "metadata", "annotations", "migration.vmoperator.vmware.com/in-transition")
	if err != nil {
		return false
	}
	if expected.TransitionAnnotation == nil {
		if found {
			return false
		}
	} else if !found || value != *expected.TransitionAnnotation {
		return false
	}

	oldValue, newValue := oldObject.DeepCopy().Object, newObject.DeepCopy().Object
	for _, object := range []map[string]interface{}{oldValue, newValue} {
		// The API server increments generation when spec.networkInterfaces changes.
		// Generation is bookkeeping here; the allowlist below already compares the
		// requested network interfaces with trusted migration state.
		unstructured.RemoveNestedField(object, "metadata", "generation")
		unstructured.RemoveNestedField(object, "metadata", "annotations", "migration.vmoperator.vmware.com/in-transition")
		// Removing the only annotation can leave an empty map on one side.
		if annotations, found, _ := unstructured.NestedMap(object, "metadata", "annotations"); found && len(annotations) == 0 {
			unstructured.RemoveNestedField(object, "metadata", "annotations")
		}
		unstructured.RemoveNestedField(object, "spec", "networkInterfaces")
	}
	return reflect.DeepEqual(oldValue, newValue)
}

func isWorkloadController(username string) bool {
	switch username {
	case NCPControllerUser, NetOperatorControllerUser, VMOperatorControllerUser, AKOControllerUser:
		return true
	default:
		return false
	}
}

func workloadResource(req admission.Request) (resource string, relevant bool) {
	switch {
	case req.Resource.Group == "" && req.Resource.Version == "v1" && req.Resource.Resource == "pods":
		return "pods", true
	case req.Resource.Group == "" && req.Resource.Version == "v1" && req.Resource.Resource == "services":
		return "services", true
	case req.Resource.Group == "vmoperator.vmware.com" && req.Resource.Resource == "virtualmachines":
		return "virtualmachines", true
	default:
		return "", false
	}
}

func decodeWorkloadObjects(req admission.Request) (oldObject, newObject *unstructured.Unstructured, err error) {
	if req.Operation == admissionv1.Update || req.Operation == admissionv1.Delete {
		if len(req.OldObject.Raw) == 0 {
			return nil, nil, fmt.Errorf("admission request is missing old workload object")
		}
		oldObject = &unstructured.Unstructured{}
		if err := json.Unmarshal(req.OldObject.Raw, &oldObject.Object); err != nil {
			return nil, nil, fmt.Errorf("decoding old workload object: %w", err)
		}
	}
	if req.Operation == admissionv1.Create || req.Operation == admissionv1.Update {
		if len(req.Object.Raw) == 0 {
			return nil, nil, fmt.Errorf("admission request is missing workload object")
		}
		newObject = &unstructured.Unstructured{}
		if err := json.Unmarshal(req.Object.Raw, &newObject.Object); err != nil {
			return nil, nil, fmt.Errorf("decoding workload object: %w", err)
		}
	}
	return oldObject, newObject, nil
}

func loadBalancerService(object *unstructured.Unstructured) bool {
	if object == nil {
		return false
	}
	serviceType, found, _ := unstructured.NestedString(object.Object, "spec", "type")
	return found && serviceType == "LoadBalancer"
}

func findWorkloadProtection(ctx context.Context, reader client.Reader, namespace string) (*namespaceProtection, error) {
	namespaceProtection, err := findNamespaceProtection(ctx, reader, namespace)
	if err != nil {
		return nil, err
	}

	infrastructureProtection, err := findControlPlaneWorkloadProtection(ctx, reader, namespace)
	if err != nil {
		return nil, err
	}
	if namespaceProtection != nil && infrastructureProtection != nil {
		return nil, fmt.Errorf("namespace %q is covered by unresolved transitions %s and %s", namespace, namespaceProtection.name, infrastructureProtection.name)
	}
	if namespaceProtection != nil {
		return namespaceProtection, nil
	}
	return infrastructureProtection, nil
}

func findControlPlaneWorkloadProtection(ctx context.Context, reader client.Reader, namespace string) (*namespaceProtection, error) {
	transition := &api.ControlPlaneNetworkTransition{}
	if err := reader.Get(ctx, types.NamespacedName{Name: api.ControlPlaneNetworkTransitionName}, transition); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if !controlPlaneTransitionProtectsWorkloads(transition) {
		return nil, nil
	}

	isSystemNamespace, err := isControlPlaneSystemNamespace(ctx, reader, namespace)
	if err != nil {
		return nil, err
	}
	if !isSystemNamespace {
		return nil, nil
	}

	return &namespaceProtection{
		name:                "controlplanenetworktransitions/" + transition.Name,
		controllerMayChange: controlPlaneControllerMayChange(transition),
	}, nil
}

func controlPlaneControllerMayChange(transition *api.ControlPlaneNetworkTransition) bool {
	if transition == nil || transition.Status == nil || transition.Spec.Action != api.ActionExecute {
		return false
	}
	status := transition.Status
	return status.ActionGeneration > 0 && status.ActionGeneration == transition.Generation &&
		status.ObservedGeneration == transition.Generation && status.LastAction == api.ActionExecute &&
		(status.ActionState == api.ActionStateRunning || status.ActionState == api.ActionStateFailed)
}

func controlPlaneTransitionProtectsWorkloads(transition *api.ControlPlaneNetworkTransition) bool {
	if transition == nil {
		return false
	}
	status := transition.Status
	if status != nil && status.LastAction == api.ActionExecute &&
		status.ActionState == api.ActionStateSucceeded && status.Phase == api.PhaseInfraTransitionComplete &&
		status.ActionGeneration > 0 && status.ObservedGeneration == status.ActionGeneration &&
		(status.ActionGeneration == transition.Generation ||
			(transition.Spec.Action == api.ActionFinalize && status.ActionGeneration+1 == transition.Generation)) {
		return false
	}

	// Persisting Execute starts protection, even before Running is recorded.
	// Any unverified Execute checkpoint keeps protection active through failure
	// and action changes until the InfraTransitionComplete checkpoint is current.
	return transition.Spec.Action == api.ActionExecute || (status != nil && status.LastAction == api.ActionExecute)
}

func isControlPlaneSystemNamespace(ctx context.Context, reader client.Reader, namespace string) (bool, error) {
	if namespace == "kube-system" {
		return true, nil
	}
	object := &corev1.Namespace{}
	if err := reader.Get(ctx, types.NamespacedName{Name: namespace}, object); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if strings.EqualFold(object.Annotations[wcpSharedT1Annotation], "true") ||
		object.Annotations[sharedVPCNamespaceAnnotation] == "kube-system" {
		return true, nil
	}
	return false, nil
}

func findNamespaceProtection(ctx context.Context, reader client.Reader, namespace string) (*namespaceProtection, error) {
	return findNamespaceProtectionFor(ctx, reader, namespace, namespaceTransitionProtects)
}

func findNamespaceProtectionFor(ctx context.Context, reader client.Reader, namespace string, protects func(*api.NamespaceNetworkTransition) bool) (*namespaceProtection, error) {
	transitions := &api.NamespaceNetworkTransitionList{}
	if err := reader.List(ctx, transitions); err != nil {
		return nil, err
	}
	var found *namespaceProtection
	for i := range transitions.Items {
		transition := &transitions.Items[i]
		if !containsNamespace(transition.Spec.Namespaces, namespace) || !protects(transition) {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("namespace %q is covered by multiple unresolved transitions %s and %s", namespace, found.transition.Name, transition.Name)
		}
		transitionCopy := transition.DeepCopy()
		var identity *api.NamespaceIdentityStatus
		if transitionCopy.Status != nil {
			for j := range transitionCopy.Status.NamespaceIdentities {
				if transitionCopy.Status.NamespaceIdentities[j].Namespace == namespace {
					identity = &transitionCopy.Status.NamespaceIdentities[j]
					break
				}
			}
		}
		found = &namespaceProtection{
			name:                transition.Name,
			controllerMayChange: namespaceControllerMayChange(transition),
			transition:          transitionCopy,
			identity:            identity,
		}
	}
	return found, nil
}
