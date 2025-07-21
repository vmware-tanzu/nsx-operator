package namespace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/golang/mock/gomock"

	mockClient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
)

func TestNamespaceValidator_Handle(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	scheme := clientgoscheme.Scheme
	decoder := admission.NewDecoder(scheme)
	v := &NamespaceValidator{
		Client:  k8sClient,
		decoder: decoder,
	}

	// Create namespace objects for testing
	nsWithAnnotation := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-ns-1",
			Annotations: map[string]string{
				AnnotationVPCNetworkConfig: "vpc-config-1",
			},
		},
	}
	nsWithAnnotationRaw, _ := json.Marshal(nsWithAnnotation)

	nsWithModifiedAnnotation := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-ns-1",
			Annotations: map[string]string{
				AnnotationVPCNetworkConfig: "vpc-config-2", // Changed value
			},
		},
	}
	nsWithModifiedAnnotationRaw, _ := json.Marshal(nsWithModifiedAnnotation)

	nsWithoutAnnotation := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-ns-1",
			Annotations: map[string]string{
				"some-other-annotation": "some-value",
			},
		},
	}
	nsWithoutAnnotationRaw, _ := json.Marshal(nsWithoutAnnotation)

	nsWithNoAnnotations := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-ns-1",
		},
	}
	nsWithNoAnnotationsRaw, _ := json.Marshal(nsWithNoAnnotations)

	invalidJSON := []byte("invalid json")

	tests := []struct {
		name      string
		operation admissionv1.Operation
		object    []byte
		oldObject []byte
		username  string
		nsName    string
		want      admission.Response
	}{
		{
			name:      "Allow request from NSX Operator service account",
			operation: admissionv1.Update,
			object:    nsWithModifiedAnnotationRaw,
			oldObject: nsWithAnnotationRaw,
			username:  NSXOperatorSA,
			nsName:    "test-ns-1",
			want:      admission.Allowed(""),
		},
		{
			name:      "Allow namespace creation",
			operation: admissionv1.Create,
			object:    nsWithAnnotationRaw,
			username:  "regular-user",
			nsName:    "test-ns-1",
			want:      admission.Allowed(""),
		},
		{
			name:      "Allow update without modifying vpc_network_config annotation",
			operation: admissionv1.Update,
			object:    nsWithAnnotationRaw,
			oldObject: nsWithAnnotationRaw,
			username:  "regular-user",
			nsName:    "test-ns-1",
			want:      admission.Allowed(""),
		},
		{
			name:      "Deny removal of vpc_network_config annotation",
			operation: admissionv1.Update,
			object:    nsWithoutAnnotationRaw,
			oldObject: nsWithAnnotationRaw,
			username:  "regular-user",
			nsName:    "test-ns-1",
			want:      admission.Denied("Namespace test-ns-1: annotation nsx.vmware.com/vpc_network_config cannot be removed once set"),
		},
		{
			name:      "Deny modification of vpc_network_config annotation",
			operation: admissionv1.Update,
			object:    nsWithModifiedAnnotationRaw,
			oldObject: nsWithAnnotationRaw,
			username:  "regular-user",
			nsName:    "test-ns-1",
			want:      admission.Denied("Namespace test-ns-1: annotation nsx.vmware.com/vpc_network_config cannot be modified once set"),
		},
		{
			name:      "Allow update when old namespace has no annotations",
			operation: admissionv1.Update,
			object:    nsWithAnnotationRaw,
			oldObject: nsWithNoAnnotationsRaw,
			username:  "regular-user",
			nsName:    "test-ns-1",
			want:      admission.Allowed(""),
		},
		{
			name:      "Error decoding namespace",
			operation: admissionv1.Update,
			object:    invalidJSON,
			username:  "regular-user",
			nsName:    "test-ns-1",
			want:      admission.Errored(http.StatusBadRequest, errors.New("couldn't get version/kind; json parse error: json: cannot unmarshal string into Go value of type struct { APIVersion string \"json:\\\"apiVersion,omitempty\\\"\"; Kind string \"json:\\\"kind,omitempty\\\"\" }")),
		},
		{
			name:      "Error decoding old namespace",
			operation: admissionv1.Update,
			object:    nsWithAnnotationRaw,
			oldObject: invalidJSON,
			username:  "regular-user",
			nsName:    "test-ns-1",
			want:      admission.Errored(http.StatusBadRequest, errors.New("couldn't get version/kind; json parse error: json: cannot unmarshal string into Go value of type struct { APIVersion string \"json:\\\"apiVersion,omitempty\\\"\"; Kind string \"json:\\\"kind,omitempty\\\"\" }")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation: tt.operation,
					Name:      tt.nsName,
					Object:    runtime.RawExtension{Raw: tt.object},
				},
			}

			if tt.oldObject != nil {
				req.OldObject = runtime.RawExtension{Raw: tt.oldObject}
			}

			req.UserInfo.Username = tt.username

			res := v.Handle(context.TODO(), req)

			assert.Equal(t, tt.want.Allowed, res.Allowed)
			if !res.Allowed && res.Result != nil && tt.want.Result != nil {
				assert.Equal(t, tt.want.Result.Message, res.Result.Message)
			}
		})
	}
}
