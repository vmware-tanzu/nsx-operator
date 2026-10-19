package networkinfo

import (
	"context"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// NCP and nsx-operator share this ServiceAccount in the Supervisor.
const lbProviderWriter = "system:serviceaccount:vmware-system-nsx:ncp-svc-account"

// +kubebuilder:webhook:path=/validate-crd-nsx-vmware-com-v1alpha1-vpcnetworkconfiguration,mutating=false,failurePolicy=fail,sideEffects=None,groups=crd.nsx.vmware.com,resources=vpcnetworkconfigurations,verbs=create;update,versions=v1alpha1,name=vpcnetworkconfiguration.validating.crd.nsx.vmware.com,admissionReviewVersions=v1

type VPCNetworkConfigurationValidator struct {
	decoder admission.Decoder
}

func (v *VPCNetworkConfigurationValidator) Handle(_ context.Context, req admission.Request) admission.Response {
	if req.UserInfo.Username == lbProviderWriter {
		return admission.Allowed("")
	}
	nc := &v1alpha1.VPCNetworkConfiguration{}
	if err := v.decoder.Decode(req, nc); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	if nc.Name != common.SystemVPCNetworkConfigurationName {
		return admission.Allowed("")
	}
	value, exists := nc.Annotations[common.AnnotationLBProvider]
	switch req.Operation {
	case admissionv1.Create:
		if exists {
			return admission.Denied("only nsx-operator and NCP may set the persisted LB provider")
		}
	case admissionv1.Update:
		old := &v1alpha1.VPCNetworkConfiguration{}
		if err := v.decoder.DecodeRaw(req.OldObject, old); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		oldValue, oldExists := old.Annotations[common.AnnotationLBProvider]
		if exists != oldExists || value != oldValue {
			return admission.Denied("only nsx-operator and NCP may modify the persisted LB provider")
		}
	}
	return admission.Allowed("")
}
