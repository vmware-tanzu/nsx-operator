/* Copyright © 2024 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetset

import (
	"context"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var NSXOperatorSA = "system:serviceaccount:vmware-system-nsx:ncp-svc-account"

// Create validator instead of using the existing one in controller-runtime because the existing one can't
// inspect admission.Request in Handle function.

// +kubebuilder:webhook:path=/validate-crd-nsx-vmware-com-v1alpha1-subnetset,mutating=false,failurePolicy=fail,sideEffects=None,
// groups=crd.nsx.vmware.com,resources=subnetsets,verbs=create;update;delete,versions=v1alpha1,
// name=subnetset.validating.crd.nsx.vmware.com,admissionReviewVersions=v1

type SubnetSetValidator struct {
	Client  client.Client
	decoder admission.Decoder
}

func defaultSubnetSetLabelChanged(oldSubnetSet, subnetSet *v1alpha1.SubnetSet) bool {
	var oldValue, value string
	oldValue, oldExists := oldSubnetSet.ObjectMeta.Labels[common.LabelDefaultSubnetSet]
	value, exists := subnetSet.ObjectMeta.Labels[common.LabelDefaultSubnetSet]
	// add or remove "default-subnetset-for" label
	// update "default-subnetset-for" label
	return oldExists != exists || oldValue != value
}

func isDefaultSubnetSet(s *v1alpha1.SubnetSet) bool {
	if _, ok := s.Labels[common.LabelDefaultSubnetSet]; ok {
		return true
	}
	return s.Name == common.DefaultVMSubnetSet || s.Name == common.DefaultPodSubnetSet
}

// Handle handles admission requests.
func (v *SubnetSetValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	subnetSet := &v1alpha1.SubnetSet{}
	var err error
	if req.Operation == admissionv1.Delete {
		err = v.decoder.DecodeRaw(req.OldObject, subnetSet)
	} else {
		err = v.decoder.Decode(req, subnetSet)
	}
	if err != nil {
		log.Error(err, "Failed to decode SubnetSet", "SubnetSet", req.Namespace+"/"+req.Name)
		return admission.Errored(http.StatusBadRequest, err)
	}

	log.V(1).Info("Handling request", "user", req.UserInfo.Username, "operation", req.Operation)
	switch req.Operation {
	case admissionv1.Create:
		if subnetSet.Spec.IPv4SubnetSize != 0 && !util.IsPowerOfTwo(subnetSet.Spec.IPv4SubnetSize) {
			return admission.Denied(fmt.Sprintf("SubnetSet %s/%s has invalid size %d, which must be power of 2", subnetSet.Namespace, subnetSet.Name, subnetSet.Spec.IPv4SubnetSize))
		}
		if isDefaultSubnetSet(subnetSet) && req.UserInfo.Username != NSXOperatorSA {
			return admission.Denied("default SubnetSet only can be created by nsx-operator")
		}
	case admissionv1.Update:
		oldSubnetSet := &v1alpha1.SubnetSet{}
		if err := v.decoder.DecodeRaw(req.OldObject, oldSubnetSet); err != nil {
			log.Error(err, "Failed to decode old SubnetSet", "SubnetSet", req.Namespace+"/"+req.Name)
			return admission.Errored(http.StatusBadRequest, err)
		}
		if defaultSubnetSetLabelChanged(oldSubnetSet, subnetSet) {
			return admission.Denied(fmt.Sprintf("SubnetSet label %s only can't be updated", common.LabelDefaultSubnetSet))
		}
	case admissionv1.Delete:
		if isDefaultSubnetSet(subnetSet) && req.UserInfo.Username != NSXOperatorSA {
			return admission.Denied("default SubnetSet only can be deleted by nsx-operator")
		}
		if req.UserInfo.Username != NSXOperatorSA {
			hasSubnetPort, err := v.checkSubnetPort(ctx, subnetSet.Namespace, subnetSet.Name)
			if err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			if hasSubnetPort {
				return admission.Denied(fmt.Sprintf("SubnetSet %s/%s with stale SubnetPorts cannot be deleted", subnetSet.Namespace, subnetSet.Name))
			}
		}
	}
	return admission.Allowed("")
}

func (v *SubnetSetValidator) checkSubnetPort(ctx context.Context, ns string, subnetSetName string) (bool, error) {
	crdSubnetPorts := &v1alpha1.SubnetPortList{}
	err := v.Client.List(ctx, crdSubnetPorts, client.InNamespace(ns))
	if err != nil {
		return false, fmt.Errorf("failed to list SubnetPort: %v", err)
	}
	for _, crdSubnetPort := range crdSubnetPorts.Items {
		if crdSubnetPort.Spec.SubnetSet == subnetSetName {
			return true, nil
		}
	}
	return false, nil
}
