/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnet

import (
	"context"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

// +kubebuilder:webhook:path=/validate-crd-nsx-vmware-com-v1alpha1-subnetportsetting,mutating=false,failurePolicy=fail,sideEffects=None,groups=crd.nsx.vmware.com,resources=subnetportsettings;subnetportsettings/status,verbs=create;update;delete,versions=v1alpha1,name=subnetportsetting.validating.crd.nsx.vmware.com,admissionReviewVersions=v1

// SubnetPortSettingValidator protects the operator-managed mirrors of NSX port settings.
type SubnetPortSettingValidator struct {
	decoder admission.Decoder
}

func (v *SubnetPortSettingValidator) Handle(_ context.Context, req admission.Request) admission.Response {
	setting := &v1alpha1.SubnetPortSetting{}
	var err error
	switch req.Operation {
	case admissionv1.Create, admissionv1.Update:
		err = v.decoder.Decode(req, setting)
	case admissionv1.Delete:
		err = v.decoder.DecodeRaw(req.OldObject, setting)
	default:
		return admission.Denied("unsupported SubnetPortSetting operation")
	}
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	if req.SubResource != "" && req.SubResource != "status" {
		return admission.Denied("unsupported SubnetPortSetting subresource")
	}
	if req.SubResource == "status" && req.Operation != admissionv1.Update {
		return admission.Denied("SubnetPortSetting status only supports updates")
	}
	if req.UserInfo.Username == NSXOperatorSA {
		return admission.Allowed("")
	}

	// Exact controller identities preserve cascading and Namespace cleanup.
	// These exceptions do not grant users with system groups write access.
	if isPortSettingLifecycleController(req.UserInfo.Username) {
		if req.Operation == admissionv1.Delete && req.SubResource == "" {
			return admission.Allowed("")
		}
		if req.Operation == admissionv1.Update && req.SubResource == "" {
			oldSetting := &v1alpha1.SubnetPortSetting{}
			if err := v.decoder.DecodeRaw(req.OldObject, oldSetting); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			if portSettingLifecycleUpdateOnly(oldSetting, setting) {
				return admission.Allowed("")
			}
		}
	}
	return admission.Denied(fmt.Sprintf("SubnetPortSetting %s/%s is managed by NSX Operator and cannot be modified by users", req.Namespace, req.Name))
}

func isPortSettingLifecycleController(username string) bool {
	switch username {
	case "system:kube-controller-manager",
		"system:serviceaccount:kube-system:generic-garbage-collector",
		"system:serviceaccount:kube-system:namespace-controller":
		return true
	default:
		return false
	}
}

func portSettingLifecycleUpdateOnly(oldSetting, setting *v1alpha1.SubnetPortSetting) bool {
	oldCopy, newCopy := oldSetting.DeepCopy(), setting.DeepCopy()
	// GC may orphan dependents or remove its deletion finalizers. Everything
	// else, including source identity, spec and status, remains operator-owned.
	newCopy.OwnerReferences = oldCopy.OwnerReferences
	newCopy.Finalizers = oldCopy.Finalizers
	// Field ownership bookkeeping can be updated by the API server on a patch.
	oldCopy.ManagedFields, newCopy.ManagedFields = nil, nil
	return equality.Semantic.DeepEqual(oldCopy, newCopy)
}
