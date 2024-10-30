package subnet

import (
	"context"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

var NSXOperatorSA = "system:serviceaccount:vmware-system-nsx:ncp-svc-account"

// Create validator instead of using the existing one in controller-runtime because the existing one can't
// inspect admission.Request in Handle function.

// +kubebuilder:webhook:path=/validate-crd-nsx-vmware-com-v1alpha1-subnet,mutating=false,failurePolicy=fail,sideEffects=None,groups=crd.nsx.vmware.com,resources=subnets,verbs=delete,versions=v1alpha1,name=subnet.validating.crd.nsx.vmware.com,admissionReviewVersions=v1

type SubnetValidator struct {
	Client  client.Client
	decoder admission.Decoder
}

// Handle handles admission requests.
func (v *SubnetValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
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
	log.V(1).Info("Handling request", "user", req.UserInfo.Username, "operation", req.Operation)
	switch req.Operation {
	case admissionv1.Delete:
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
