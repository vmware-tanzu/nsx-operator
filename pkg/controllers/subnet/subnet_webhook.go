package subnet

import (
	"context"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var NSXOperatorSA = "system:serviceaccount:vmware-system-nsx:ncp-svc-account"

// Create a validator instead of using the existing one in controller-runtime because the existing one can't
// inspect admission.Request in Handle function.

// +kubebuilder:webhook:path=/validate-crd-nsx-vmware-com-v1alpha1-subnet,mutating=false,failurePolicy=fail,sideEffects=None,groups=crd.nsx.vmware.com,resources=subnets,verbs=create;update;delete,versions=v1alpha1,name=subnet.validating.crd.nsx.vmware.com,admissionReviewVersions=v1

type SubnetValidator struct {
	Client    client.Client
	decoder   admission.Decoder
	nsxClient *nsx.Client
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
		log.Error(err, "Error while decoding Subnet", "Subnet", req.Namespace+"/"+req.Name)
		return admission.Errored(http.StatusBadRequest, err)
	}
	switch req.Operation {
	case admissionv1.Create:
		valid, msg := util.ValidateSubnetSize(v.nsxClient, subnet.Spec.IPv4SubnetSize)
		if !valid {
			return admission.Denied(fmt.Sprintf("Subnet %s/%s has invalid size %d: %s", subnet.Namespace, subnet.Name, subnet.Spec.IPv4SubnetSize, msg))
		}
		// Shared Subnet can only be updated by NSX Operator
		if (common.IsSharedSubnet(subnet)) && req.UserInfo.Username != NSXOperatorSA {
			return admission.Denied(fmt.Sprintf("Shared Subnet %s/%s can only be created by NSX Operator", subnet.Namespace, subnet.Name))
		}

		// Prevent users from setting spec.vpcName and spec.vlanConnectionName
		if req.UserInfo.Username != NSXOperatorSA {
			if subnet.Spec.VPCName != "" {
				return admission.Denied(fmt.Sprintf("Subnet %s/%s: spec.vpcName can only be set by NSX Operator", subnet.Namespace, subnet.Name))
			}
			if subnet.Spec.VLANConnectionName != "" {
				return admission.Denied(fmt.Sprintf("Subnet %s/%s: spec.vlanConnectionName can only be set by NSX Operator", subnet.Namespace, subnet.Name))
			}
			if subnet.Spec.AccessMode == v1alpha1.AccessMode(v1alpha1.AccessModeL2Only) {
				return admission.Denied(fmt.Sprintf("Subnet %s/%s: spec.accessMode L2Only is not supported", subnet.Namespace, subnet.Name))
			}
		}
	case admissionv1.Update:
		oldSubnet := &v1alpha1.Subnet{}
		if err := v.decoder.DecodeRaw(req.OldObject, oldSubnet); err != nil {
			log.Error(err, "Failed to decode old Subnet", "Subnet", req.Namespace+"/"+req.Name)
			return admission.Errored(http.StatusBadRequest, err)
		}

		log.Trace("Decoded old Subnet", "oldSubnet", oldSubnet)
		log.Trace("Decoded new Subnet", "subnet", subnet)
		log.Trace("User info", "username", req.UserInfo.Username, "isNSXOperator", req.UserInfo.Username == NSXOperatorSA)
		log.Trace("VPCName comparison", "oldVPCName", oldSubnet.Spec.VPCName, "newVPCName", subnet.Spec.VPCName, "isEqual", oldSubnet.Spec.VPCName == subnet.Spec.VPCName)

		// Shared Subnet can only be updated by NSX Operator
		if (common.IsSharedSubnet(oldSubnet) || common.IsSharedSubnet(subnet)) && req.UserInfo.Username != NSXOperatorSA {
			return admission.Denied(fmt.Sprintf("Shared Subnet %s/%s can only be updated by NSX Operator", subnet.Namespace, subnet.Name))
		}

		// Prevent users from updating spec.vpcName and spec.vlanConnectionName
		if req.UserInfo.Username != NSXOperatorSA {
			// Check if vpcName is being added or changed
			if oldSubnet.Spec.VPCName != subnet.Spec.VPCName {
				log.Trace("Denying update to vpcName", "oldVPCName", oldSubnet.Spec.VPCName, "newVPCName", subnet.Spec.VPCName)
				return admission.Denied(fmt.Sprintf("Subnet %s/%s: spec.vpcName can only be updated by NSX Operator", subnet.Namespace, subnet.Name))
			}
			if oldSubnet.Spec.VLANConnectionName != subnet.Spec.VLANConnectionName {
				return admission.Denied(fmt.Sprintf("Subnet %s/%s: spec.vlanConnectionName can only be updated by NSX Operator", subnet.Namespace, subnet.Name))
			}
			if !nsxutil.CompareArraysWithoutOrder(oldSubnet.Spec.IPAddresses, subnet.Spec.IPAddresses) {
				return admission.Denied("ipAddresses is immutable")
			}
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
			hasSubnetIPReservation, err := v.checkSubnetIPReservation(ctx, subnet.Namespace, subnet.Name)
			if err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			if hasSubnetIPReservation {
				return admission.Denied(fmt.Sprintf("Subnet %s/%s with stale SubnetIPReservations cannot be deleted", subnet.Namespace, subnet.Name))
			}
		}
	}
	return admission.Allowed("")
}

func (v *SubnetValidator) checkSubnetPort(ctx context.Context, ns string, subnetName string) (bool, error) {
	crdSubnetPorts := &v1alpha1.SubnetPortList{}
	err := v.Client.List(ctx, crdSubnetPorts, client.InNamespace(ns), client.MatchingFields{"spec.subnet": subnetName})
	if err != nil {
		return false, fmt.Errorf("failed to list SubnetPort: %v", err)
	}
	if len(crdSubnetPorts.Items) > 0 {
		return true, nil
	}
	return false, nil
}

func (v *SubnetValidator) checkSubnetIPReservation(ctx context.Context, ns string, subnetName string) (bool, error) {
	crdSubnetIPReservations := &v1alpha1.SubnetIPReservationList{}
	err := v.Client.List(ctx, crdSubnetIPReservations, client.InNamespace(ns), client.MatchingFields{"spec.subnet": subnetName})
	if err != nil {
		return false, fmt.Errorf("failed to list SubnetIPReservations: %v", err)
	}
	if len(crdSubnetIPReservations.Items) > 0 {
		return true, nil
	}
	return false, nil
}
