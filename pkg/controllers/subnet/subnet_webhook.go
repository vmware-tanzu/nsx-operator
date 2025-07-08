package subnet

import (
	"context"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var NSXOperatorSA = "system:serviceaccount:vmware-system-nsx:ncp-svc-account"

// Create a validator instead of using the existing one in controller-runtime because the existing one can't
// inspect admission.Request in Handle function.

// +kubebuilder:webhook:path=/validate-crd-nsx-vmware-com-v1alpha1-subnet,mutating=false,failurePolicy=fail,sideEffects=None,groups=crd.nsx.vmware.com,resources=subnets,verbs=create;update;delete,versions=v1alpha1,name=subnet.validating.crd.nsx.vmware.com,admissionReviewVersions=v1

type SubnetValidator struct {
	Client  client.Client
	decoder admission.Decoder
}

// Handle handles admission requests.
func (v *SubnetValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	log.Info("Handling request", "user", req.UserInfo.Username, "operation", req.Operation)
	subnet := &v1alpha1.Subnet{}

	var err error
	if req.Operation == admissionv1.Delete {
		err = v.decoder.DecodeRaw(req.OldObject, subnet)
	} else {
		err = v.decoder.Decode(req, subnet)
	}
	if err != nil {
		log.Error(err, "error while decoding Subnet", "Subnet", req.Namespace+"/"+req.Name)
		return admission.Errored(http.StatusBadRequest, err)
	}
	switch req.Operation {
	case admissionv1.Create:
		if subnet.Spec.IPv4SubnetSize != 0 && !util.IsPowerOfTwo(subnet.Spec.IPv4SubnetSize) {
			return admission.Denied(fmt.Sprintf("Subnet %s/%s has invalid size %d, which must be power of 2", subnet.Namespace, subnet.Name, subnet.Spec.IPv4SubnetSize))
		}

		// Shared Subnet can only be updated by NSX Operator
		if (common.IsSharedSubnet(subnet)) && req.UserInfo.Username != NSXOperatorSA {
			return admission.Denied(fmt.Sprintf("Shared Subnet %s/%s can only be created by NSX Operator", subnet.Namespace, subnet.Name))
		}

		// Prevent users from setting spec.vpcName and spec.enableVLANExtension
		if req.UserInfo.Username != NSXOperatorSA {
			if subnet.Spec.VPCName != "" {
				return admission.Denied(fmt.Sprintf("Subnet %s/%s: spec.vpcName can only be set by NSX Operator", subnet.Namespace, subnet.Name))
			}
			if subnet.Spec.AdvancedConfig.EnableVLANExtension {
				return admission.Denied(fmt.Sprintf("Subnet %s/%s: spec.enableVLANExtension can only be set by NSX Operator", subnet.Namespace, subnet.Name))
			}
		}
	case admissionv1.Update:
		oldSubnet := &v1alpha1.Subnet{}
		if err := v.decoder.DecodeRaw(req.OldObject, oldSubnet); err != nil {
			log.Error(err, "Failed to decode old Subnet", "Subnet", req.Namespace+"/"+req.Name)
			return admission.Errored(http.StatusBadRequest, err)
		}

		log.V(2).Info("Decoded old Subnet", "oldSubnet", oldSubnet)
		log.V(2).Info("Decoded new Subnet", "subnet", subnet)
		log.V(2).Info("User info", "username", req.UserInfo.Username, "isNSXOperator", req.UserInfo.Username == NSXOperatorSA)
		log.V(2).Info("VPCName comparison", "oldVPCName", oldSubnet.Spec.VPCName, "newVPCName", subnet.Spec.VPCName, "isEqual", oldSubnet.Spec.VPCName == subnet.Spec.VPCName)

		// Shared Subnet can only be updated by NSX Operator
		if (common.IsSharedSubnet(oldSubnet) || common.IsSharedSubnet(subnet)) && req.UserInfo.Username != NSXOperatorSA {
			return admission.Denied(fmt.Sprintf("Shared Subnet %s/%s can only be updated by NSX Operator", subnet.Namespace, subnet.Name))
		}

		// Prevent users from updating spec.vpcName and spec.enableVLANExtension
		if req.UserInfo.Username != NSXOperatorSA {
			// Check if vpcName is being added or changed
			if oldSubnet.Spec.VPCName != subnet.Spec.VPCName {
				log.V(2).Info("Denying update to vpcName", "oldVPCName", oldSubnet.Spec.VPCName, "newVPCName", subnet.Spec.VPCName)
				return admission.Denied(fmt.Sprintf("Subnet %s/%s: spec.vpcName can only be updated by NSX Operator", subnet.Namespace, subnet.Name))
			}
			if oldSubnet.Spec.AdvancedConfig.EnableVLANExtension != subnet.Spec.AdvancedConfig.EnableVLANExtension {
				return admission.Denied(fmt.Sprintf("Subnet %s/%s: spec.enableVLANExtension can only be updated by NSX Operator", subnet.Namespace, subnet.Name))
			}
		}
		if !nsxutil.CompareArraysWithoutOrder(oldSubnet.Spec.IPAddresses, subnet.Spec.IPAddresses) {
			return admission.Denied("ipAddresses is immutable")
		}
	case admissionv1.Delete:
		oldSubnet := &v1alpha1.Subnet{}
		if err := v.decoder.DecodeRaw(req.OldObject, oldSubnet); err != nil {
			log.Error(err, "Failed to decode old Subnet", "Subnet", req.Namespace+"/"+req.Name)
			return admission.Errored(http.StatusBadRequest, err)
		}

		// Shared Subnet can only be deleted by NSX Operator
		if (common.IsSharedSubnet(oldSubnet) || common.IsSharedSubnet(subnet)) && req.UserInfo.Username != NSXOperatorSA {
			return admission.Denied(fmt.Sprintf("Shared Subnet %s/%s can only be deleted by NSX Operator", subnet.Namespace, subnet.Name))
		}

		if req.UserInfo.Username != NSXOperatorSA {
			hasSubnetPort, err := v.checkSubnetPort(ctx, subnet.Namespace, subnet.Name)
			if err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			if hasSubnetPort {
				return admission.Denied(fmt.Sprintf("Subnet %s/%s with stale SubnetPorts cannot be deleted", subnet.Namespace, subnet.Name))
			}
		}
	}
	return admission.Allowed("")
}

func (v *SubnetValidator) checkSubnetPort(ctx context.Context, ns string, subnetName string) (bool, error) {
	crdSubnetPorts := &v1alpha1.SubnetPortList{}
	err := v.Client.List(ctx, crdSubnetPorts, client.InNamespace(ns))
	if err != nil {
		return false, fmt.Errorf("failed to list SubnetPort: %v", err)
	}
	for _, crdSubnetPort := range crdSubnetPorts.Items {
		if crdSubnetPort.Spec.Subnet == subnetName {
			return true, nil
		}
	}
	return false, nil
}
