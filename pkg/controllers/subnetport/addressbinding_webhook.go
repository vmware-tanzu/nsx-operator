package subnetport

import (
	"context"
	"fmt"
	"net/http"
	"reflect"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// Create validator instead of using the existing one in controller-runtime because the existing one can't
// inspect admission.Request in Handle function.

//+kubebuilder:webhook:path=/validate-crd-nsx-vmware-com-v1alpha1-addressbinding,mutating=false,failurePolicy=fail,sideEffects=None,groups=crd.nsx.vmware.com,resources=addressbindings,verbs=create;update,versions=v1alpha1,name=addressbinding.validating.crd.nsx.vmware.com,admissionReviewVersions=v1

type AddressBindingValidator struct {
	Client  client.Client
	decoder admission.Decoder
}

// Handle handles admission requests.
func (v *AddressBindingValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	ab := &v1alpha1.AddressBinding{}
	if req.Operation == admissionv1.Delete {
		return admission.Allowed("")
	} else {
		err := v.decoder.Decode(req, ab)
		if err != nil {
			log.Error(err, "error while decoding AddressBinding", "AddressBinding", req.Namespace+"/"+req.Name)
			return admission.Errored(http.StatusBadRequest, err)
		}
	}
	switch req.Operation {
	case admissionv1.Create:
		existingAddressBindingList := &v1alpha1.AddressBindingList{}
		abIndexValue := fmt.Sprintf("%s/%s", ab.Namespace, ab.Spec.VMName)
		err := v.Client.List(context.TODO(), existingAddressBindingList, client.MatchingFields{util.AddressBindingNamespaceVMIndexKey: abIndexValue})
		if err != nil {
			log.Error(err, "failed to list AddressBinding from cache", "indexValue", abIndexValue)
			return admission.Errored(http.StatusInternalServerError, err)
		}
		for _, existingAddressBinding := range existingAddressBindingList.Items {
			if ab.Name != existingAddressBinding.Name && ab.Spec.InterfaceName == existingAddressBinding.Spec.InterfaceName {
				return admission.Denied("interface already has AddressBinding")
			}
		}
	case admissionv1.Update:
		oldAddressBinding := &v1alpha1.AddressBinding{}
		if err := v.decoder.DecodeRaw(req.OldObject, oldAddressBinding); err != nil {
			log.Error(err, "error while decoding AddressBinding", "AddressBinding", req.Namespace+"/"+req.Name)
			return admission.Errored(http.StatusBadRequest, err)
		}
		if !reflect.DeepEqual(ab.Spec, oldAddressBinding.Spec) {
			return admission.Denied("update AddressBinding is not allowed")
		}
	}
	return admission.Allowed("")
}
