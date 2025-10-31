/* Copyright © 2025 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ipaddressallocation

import (
	"context"
	"fmt"
	"net"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// Create validator instead of using the existing one in controller-runtime because the existing one can't
// inspect admission.Request in Handle function.

//+kubebuilder:webhook:path=/validate-crd-nsx-vmware-com-v1alpha1-ipaddressallocation,mutating=false,failurePolicy=fail,sideEffects=None,groups=crd.nsx.vmware.com,resources=ipaddressallocations,verbs=create;update;delete,versions=v1alpha1,name=ipaddressallocation.validating.crd.nsx.vmware.com,admissionReviewVersions=v1

type IPAddressAllocationValidator struct {
	Client  client.Client
	decoder admission.Decoder
}

// Handle handles admission requests.
func (v *IPAddressAllocationValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	log.Info("Handling ipaddressallocation request", "user", req.UserInfo.Username, "operation", req.Operation)
	ipAddressAllocation := &v1alpha1.IPAddressAllocation{}
	var err error
	if req.Operation == admissionv1.Delete {
		err = v.decoder.DecodeRaw(req.OldObject, ipAddressAllocation)
	} else {
		err = v.decoder.Decode(req, ipAddressAllocation)
	}
	if err != nil {
		log.Error(err, "Error while decoding IPAddressAllocation", "IPAddressAllocation", req.Namespace+"/"+req.Name)
		return admission.Errored(http.StatusBadRequest, err)
	}
	switch req.Operation {
	case admissionv1.Delete:
		existingAddressBindingList := &v1alpha1.AddressBindingList{}
		if err := v.Client.List(context.TODO(), existingAddressBindingList, client.InNamespace(ipAddressAllocation.Namespace), client.MatchingFields{util.AddressBindingIPAddressAllocationNameIndexKey: ipAddressAllocation.Name}); err != nil {
			log.Error(err, "Failed to list AddressBindings", "Namespace", ipAddressAllocation.Namespace)
			return admission.Errored(http.StatusBadRequest, err)
		}
		if len(existingAddressBindingList.Items) > 0 {
			return admission.Denied(fmt.Sprintf("IPAddressAllocation %s is used by AddressBinding %s", ipAddressAllocation.Name, existingAddressBindingList.Items[0].Name))
		}
		return v.validateServiceVIP(ctx, req, ipAddressAllocation)
	}
	return admission.Allowed("")
}

func (v *IPAddressAllocationValidator) validateServiceVIP(ctx context.Context, req admission.Request, ipAlloc *v1alpha1.IPAddressAllocation) admission.Response {
	// If conditions are missing or not Ready — allow delete
	if len(ipAlloc.Status.Conditions) == 0 || ipAlloc.Status.Conditions[0].Type != "Ready" {
		return admission.Allowed("")
	}

	// Parse allocation IPs — assuming comma-separated or space-separated string
	allocationIPs := ipAlloc.Status.AllocationIPs

	// List Services in the same namespace
	var svcList corev1.ServiceList
	if err := v.Client.List(ctx, &svcList, client.InNamespace(req.Namespace)); err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	// Check if any Service uses one of the allocated IPs
	for _, svc := range svcList.Items {
		if svc.Spec.LoadBalancerIP != "" {
			if v.ifIPUsed(svc.Spec.LoadBalancerIP, allocationIPs) { // IP in use — reject delete
				msg := fmt.Sprintf("cannot delete IPAddressAllocation %s: IP %s is still in use by Service %s", ipAlloc.Name, svc.Spec.LoadBalancerIP, svc.Name)
				return admission.Denied(msg)
			}
		}
	}
	return admission.Allowed("")
}

func (v *IPAddressAllocationValidator) ifIPUsed(loadBalancerIP string, ipRange string) bool {
	ip := net.ParseIP(loadBalancerIP)
	if ip == nil {
		return false // invalid input
	}
	// Try parsing ipRange as CIDR first
	if _, ipNet, err := net.ParseCIDR(ipRange); err == nil {
		return ipNet.Contains(ip)
	}

	// If not CIDR, try parsing as single IP
	rangeIP := net.ParseIP(ipRange)
	if rangeIP == nil {
		return false // invalid input
	}

	return ip.Equal(rangeIP)
}
