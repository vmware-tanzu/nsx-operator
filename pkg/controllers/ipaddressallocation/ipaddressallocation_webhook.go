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
)

// +kubebuilder:webhook:path=/validate-crd-nsx-vmware-com-v1alpha1-ipaddressallocation,mutating=false,failurePolicy=fail,sideEffects=None,groups=crd.nsx.vmware.com,resources=ipaddressallocations,verbs=delete,versions=v1alpha1,name=ipaddressallocation.validating.crd.nsx.vmware.com,admissionReviewVersions=v1

type IPAddressAllocationValidator struct {
	Client  client.Client
	decoder admission.Decoder
}

// Handle handles admission requests.
func (v *IPAddressAllocationValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	log.Info("Handling ipaddressallocation request", "user", req.UserInfo.Username, "operation", req.Operation)
	// Only care about DELETE operations
	if req.Operation != admissionv1.Delete {
		return admission.Allowed("operation is not DELETE")
	}

	// Decode the old object from the request
	ipAlloc := &v1alpha1.IPAddressAllocation{}
	err := v.decoder.DecodeRaw(req.OldObject, ipAlloc)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// If conditions are missing or not Ready — allow delete
	if len(ipAlloc.Status.Conditions) == 0 || ipAlloc.Status.Conditions[0].Type != "Ready" {
		return admission.Allowed("allocation not ready, safe to delete")
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

	// No conflicting Service found — allow delete
	return admission.Allowed("no services using allocated IPs, safe to delete")
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
