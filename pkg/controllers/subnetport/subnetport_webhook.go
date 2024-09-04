package subnetport

import (
	"context"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

// log is for logging in this package.
var subnetportlog = logf.Log.WithName("subnetport-webhook")

type Validator struct {
	Client  client.Client
	decoder admission.Decoder
}

// Handle handles admission requests.
func (v *Validator) Handle(ctx context.Context, req admission.Request) admission.Response {
	subnetPort := &v1alpha1.SubnetPort{}
	err := v.decoder.Decode(req, subnetPort)
	if err != nil {
		subnetportlog.Error(err, "error while decoding subnetPort", "subnetPort", req.Namespace+"/"+req.Name)
		return admission.Errored(http.StatusBadRequest, err)
	}

	log.Info("handling SubnetPort admission requests", "SubnetPort", subnetPort)

	if subnetPort.Spec.Subnet != "" && subnetPort.Spec.SubnetSet != "" {
		return admission.Denied("subnet and subnetset should not be configured at the same time")
	}

	subnetportlog.Info("request user-info", "name", req.UserInfo.Username)
	switch req.Operation {
	case admissionv1.Create:
		if subnetPort.Spec.Subnet != "" {
			subnet := &v1alpha1.Subnet{}
			if err := v.Client.Get(ctx, types.NamespacedName{
				Namespace: subnetPort.Namespace,
				Name:      subnetPort.Name,
			}, subnet); err != nil {
				return admission.Denied("The subnet is not found")
			}
			if !subnet.DeletionTimestamp.IsZero() {
				return admission.Denied("The subnet is been deleting")
			}
		}

		if subnetPort.Spec.SubnetSet != "" {
			subnets := &v1alpha1.SubnetSet{}
			if err := v.Client.Get(ctx, types.NamespacedName{
				Namespace: subnetPort.Namespace,
				Name:      subnetPort.Name,
			}, subnets); err != nil {
				return admission.Denied("The subnetset is not found")
			}
			if !subnets.DeletionTimestamp.IsZero() {
				return admission.Denied("The subnetset is been deleting")
			}
		}
	}
	return admission.Allowed("")
}
