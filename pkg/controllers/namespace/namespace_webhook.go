package namespace

import (
	"context"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log variable is defined in namespace_controller.go at the package level

const (
	// AnnotationVPCNetworkConfig is the annotation key for vpc network config
	AnnotationVPCNetworkConfig = "nsx.vmware.com/vpc_network_config"
)

var NSXOperatorSA = "system:serviceaccount:vmware-system-nsx:ncp-svc-account"

// +kubebuilder:webhook:path=/validate-v1-namespace,mutating=false,failurePolicy=fail,sideEffects=None,groups=core,resources=namespaces,verbs=create;update,versions=v1,name=namespace.validating.nsx.vmware.com,admissionReviewVersions=v1

// NamespaceValidator validates Namespace updates to prevent modification of vpc_network_config annotation
type NamespaceValidator struct {
	Client  client.Client
	decoder admission.Decoder
}

// Handle handles admission requests.
func (v *NamespaceValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	log.Info("Handling namespace validation request", "user", req.UserInfo.Username, "operation", req.Operation)

	// Skip validation for NSX Operator service account
	if req.UserInfo.Username == NSXOperatorSA {
		return admission.Allowed("")
	}

	namespace := &corev1.Namespace{}
	err := v.decoder.Decode(req, namespace)
	if err != nil {
		log.Error(err, "error while decoding Namespace", "Namespace", req.Name)
		return admission.Errored(http.StatusBadRequest, err)
	}

	// For update operations, check if vpc_network_config annotation is being modified
	if req.Operation == admissionv1.Update {
		oldNamespace := &corev1.Namespace{}
		if err := v.decoder.DecodeRaw(req.OldObject, oldNamespace); err != nil {
			log.Error(err, "Failed to decode old Namespace", "Namespace", req.Name)
			return admission.Errored(http.StatusBadRequest, err)
		}

		// Get annotations from old and new namespace
		oldAnnotations := oldNamespace.GetAnnotations()
		newAnnotations := namespace.GetAnnotations()

		// Check if vpc_network_config annotation exists in the old namespace
		oldVPCNetworkConfig, oldHasAnnotation := oldAnnotations[AnnotationVPCNetworkConfig]
		if oldHasAnnotation {
			// Check if the vpc_network_config annotation is being modified or removed
			newVPCNetworkConfig, newHasAnnotation := newAnnotations[AnnotationVPCNetworkConfig]
			if !newHasAnnotation {
				// Annotation is being removed
				log.Info("Denying removal of vpc_network_config annotation", "Namespace", req.Name)
				return admission.Denied(fmt.Sprintf("Namespace %s: annotation %s cannot be removed once set", req.Name, AnnotationVPCNetworkConfig))
			} else if oldVPCNetworkConfig != newVPCNetworkConfig {
				// Annotation value is being changed
				log.Info("Denying modification of vpc_network_config annotation",
					"Namespace", req.Name,
					"oldValue", oldVPCNetworkConfig,
					"newValue", newVPCNetworkConfig)
				return admission.Denied(fmt.Sprintf("Namespace %s: annotation %s cannot be modified once set", req.Name, AnnotationVPCNetworkConfig))
			}
		}
	}

	return admission.Allowed("")
}
