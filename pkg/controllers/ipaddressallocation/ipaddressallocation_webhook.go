package ipaddressallocation

import (
	"context"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// Create validator instead of using the existing one in controller-runtime because the existing one can't
// inspect admission.Request in Handle function.

//+kubebuilder:webhook:path=/validate-crd-nsx-vmware-com-v1alpha1-ipaddressallocation,mutating=false,failurePolicy=fail,sideEffects=None,groups=crd.nsx.vmware.com,resources=ipaddressallocations,verbs=create;update,versions=v1alpha1,name=ipaddressallocation.validating.crd.nsx.vmware.com,admissionReviewVersions=v1

type IPAddressAllocationValidator struct {
	Client  client.Client
	decoder admission.Decoder
}

// Handle handles admission requests.
func (v *IPAddressAllocationValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	ipAddressAllocation := &v1alpha1.IPAddressAllocation{}
	var err error
	if req.Operation == admissionv1.Delete {
		err = v.decoder.DecodeRaw(req.OldObject, ipAddressAllocation)
	} else {
		err = v.decoder.Decode(req, ipAddressAllocation)
	}
	if err != nil {
		log.Error(err, "error while decoding IPAddressAllocation", "IPAddressAllocation", req.Namespace+"/"+req.Name)
		return admission.Errored(http.StatusBadRequest, err)
	}
	switch req.Operation {
	case admissionv1.Delete:
		existingAddressBindingList := &v1alpha1.AddressBindingList{}
		if err := v.Client.List(context.TODO(), existingAddressBindingList, client.InNamespace(ipAddressAllocation.Namespace), client.MatchingFields{util.AddressBindingIPAddressAllocationNameIndexKey: ipAddressAllocation.Name}); err != nil {
			log.Error(err, "failed to list AddressBindings", "Namespace", ipAddressAllocation.Namespace)
			return admission.Errored(http.StatusBadRequest, err)
		}
		if len(existingAddressBindingList.Items) > 0 {
			return admission.Denied(fmt.Sprintf("IPAddressAllocation %s is used by AddressBinding %s", ipAddressAllocation.Name, existingAddressBindingList.Items[0].Name))
		}
	}
	return admission.Allowed("")
}
