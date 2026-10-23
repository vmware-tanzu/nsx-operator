package common

import (
	"context"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// NCP and nsx-operator share this ServiceAccount in the Supervisor.
const lbProviderWriter = "system:serviceaccount:vmware-system-nsx:ncp-svc-account"

// +kubebuilder:webhook:path=/validate-nsx-vmware-com-v1-ncpconfig,mutating=false,failurePolicy=fail,sideEffects=None,groups=nsx.vmware.com,resources=ncpconfigs,verbs=create;update,versions=v1,name=ncpconfig.validating.nsx.vmware.com,admissionReviewVersions=v1

type NCPConfigValidator struct {
	decoder admission.Decoder
}

func (v *NCPConfigValidator) Handle(_ context.Context, req admission.Request) admission.Response {
	nc := &unstructured.Unstructured{}
	if err := v.decoder.Decode(req, nc); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	if nc.GetName() != util.NSXRestoreStatus {
		return admission.Allowed("")
	}
	trustedWriter := req.UserInfo.Username == lbProviderWriter
	value, exists := nc.GetAnnotations()[servicecommon.AnnotationLBProvider]
	switch req.Operation {
	case admissionv1.Create:
		if exists {
			return admission.Denied("only nsx-operator and NCP may set the persisted LB provider")
		}
	case admissionv1.Update:
		old := &unstructured.Unstructured{}
		if err := v.decoder.DecodeRaw(req.OldObject, old); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		oldValue, oldExists := old.GetAnnotations()[servicecommon.AnnotationLBProvider]
		providerChanged := exists != oldExists || value != oldValue
		if !providerChanged {
			return admission.Allowed("")
		}
		if trustedWriter && !oldExists && exists {
			return admission.Allowed("")
		}
		return admission.Denied("the persisted LB provider is immutable once set")
	}
	return admission.Allowed("")
}
