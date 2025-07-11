package ipaddressallocation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func TestIPAddressAllocationValidator_Handle(t *testing.T) {
	indexFunc := func(obj client.Object) []string {
		if ab, ok := obj.(*v1alpha1.AddressBinding); !ok {
			log.Info("Invalid object", "type", reflect.TypeOf(obj))
			return []string{}
		} else {
			return []string{fmt.Sprintf("%s", ab.Spec.IPAddressAllocationName)}
		}
	}
	reqDelete, _ := json.Marshal(&v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns1",
			Name:      "ip1",
		},
		Spec: v1alpha1.IPAddressAllocationSpec{
			IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityExternal,
			AllocationIPs:            "10.0.0.8",
		},
	})
	reqCreate, _ := json.Marshal(&v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns1",
			Name:      "ip2",
		},
		Spec: v1alpha1.IPAddressAllocationSpec{
			IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityExternal,
			AllocationIPs:            "10.0.0.9",
		},
	})
	reqUpdate, _ := json.Marshal(&v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns1",
			Name:      "ip3",
		},
		Spec: v1alpha1.IPAddressAllocationSpec{
			IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityExternal,
			AllocationIPs:            "10.0.0.10",
		},
	})
	type args struct {
		req admission.Request
	}
	tests := []struct {
		name        string
		args        args
		prepareFunc func(*testing.T, client.Client, context.Context) *gomonkey.Patches
		want        admission.Response
	}{
		{
			name: "delete with existing address binding",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Delete,
				OldObject: runtime.RawExtension{Raw: reqDelete},
			}}},
			prepareFunc: func(t *testing.T, client client.Client, ctx context.Context) *gomonkey.Patches {
				client.Create(ctx, &v1alpha1.AddressBinding{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "ab1"},
					Spec: v1alpha1.AddressBindingSpec{
						IPAddressAllocationName: "ip1",
					},
				})
				return nil
			},
			want: admission.Denied("IPAddressAllocation ip1 is used by AddressBinding ab1"),
		},
		{
			name: "delete without address binding",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Delete,
				OldObject: runtime.RawExtension{Raw: reqDelete},
			}}},
			want: admission.Allowed(""),
		},
		{
			name: "create decode error",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Create,
			}}},
			want: admission.Errored(http.StatusBadRequest, fmt.Errorf("there is no content to decode")),
		},
		{
			name: "create success",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Create,
				Object:    runtime.RawExtension{Raw: reqCreate},
			}}},
			want: admission.Allowed(""),
		},
		{
			name: "update decode error",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Update,
			}}},
			want: admission.Errored(http.StatusBadRequest, fmt.Errorf("there is no content to decode")),
		},
		{
			name: "update success",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Update,
				Object:    runtime.RawExtension{Raw: reqUpdate},
				OldObject: runtime.RawExtension{Raw: reqDelete},
			}}},
			want: admission.Allowed(""),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := clientgoscheme.Scheme
			v1alpha1.AddToScheme(scheme)
			client := fake.NewClientBuilder().WithScheme(scheme).WithIndex(&v1alpha1.AddressBinding{}, util.AddressBindingIPAddressAllocationNameIndexKey, indexFunc).Build()
			decoder := admission.NewDecoder(scheme)
			ctx := context.TODO()
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc(t, client, ctx)
				if patches != nil {
					defer patches.Reset()
				}
			}
			v := &IPAddressAllocationValidator{
				Client:  client,
				decoder: decoder,
			}
			assert.Equalf(t, tt.want, v.Handle(ctx, tt.args.req), "Handle()")
		})
	}
}

func TestIfIPUsed(t *testing.T) {
	validator := &IPAddressAllocationValidator{}

	tests := []struct {
		name           string
		loadBalancerIP string
		ipRange        string
		want           bool
	}{
		{
			name:           "IP in CIDR range",
			loadBalancerIP: "192.168.1.5",
			ipRange:        "192.168.1.0/24",
			want:           true,
		},
		{
			name:           "IP not in CIDR range",
			loadBalancerIP: "192.168.2.5",
			ipRange:        "192.168.1.0/24",
			want:           false,
		},
		{
			name:           "IP equals single IP",
			loadBalancerIP: "10.0.0.1",
			ipRange:        "10.0.0.1",
			want:           true,
		},
		{
			name:           "IP not equals single IP",
			loadBalancerIP: "10.0.0.2",
			ipRange:        "10.0.0.1",
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validator.ifIPUsed(tt.loadBalancerIP, tt.ipRange)
			if got != tt.want {
				t.Errorf("ifIPUsed(%q, %q) = %v, want %v", tt.loadBalancerIP, tt.ipRange, got, tt.want)
			}
		})
	}
}

func TestValidateServiceVIP(t *testing.T) {
	scheme := clientgoscheme.Scheme
	_ = v1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name           string
		ipAlloc        *v1alpha1.IPAddressAllocation
		services       []corev1.Service
		expectAllowed  bool
		expectDenied   bool
		expectError    bool
		expectedReason string
	}{
		{
			name: "not ready condition allows delete",
			ipAlloc: &v1alpha1.IPAddressAllocation{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "ipa1"},
				Status:     v1alpha1.IPAddressAllocationStatus{Conditions: nil},
			},
			expectAllowed: true,
		},
		{
			name: "ready, no services, allows delete",
			ipAlloc: &v1alpha1.IPAddressAllocation{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "ipa2"},
				Status: v1alpha1.IPAddressAllocationStatus{
					Conditions:    []v1alpha1.Condition{{Type: "Ready"}},
					AllocationIPs: "10.0.0.1",
				},
			},
			expectAllowed: true,
		},
		{
			name: "ready, service uses allocated IP, denies delete",
			ipAlloc: &v1alpha1.IPAddressAllocation{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "ipa3"},
				Status: v1alpha1.IPAddressAllocationStatus{
					Conditions:    []v1alpha1.Condition{{Type: "Ready"}},
					AllocationIPs: "10.0.0.5",
				},
			},
			services: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "svc1", Namespace: "ns1"},
					Spec:       corev1.ServiceSpec{LoadBalancerIP: "10.0.0.5"},
				},
			},
			expectDenied:   true,
			expectedReason: "cannot delete IPAddressAllocation ipa3: IP 10.0.0.5 is still in use by Service svc1",
		},
		{
			name: "ready, service does not use allocated IP, allows delete",
			ipAlloc: &v1alpha1.IPAddressAllocation{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "ipa4"},
				Status: v1alpha1.IPAddressAllocationStatus{
					Conditions:    []v1alpha1.Condition{{Type: "Ready"}},
					AllocationIPs: "10.0.0.10",
				},
			},
			services: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "svc2", Namespace: "ns1"},
					Spec:       corev1.ServiceSpec{LoadBalancerIP: "10.0.0.11"},
				},
			},
			expectAllowed: true,
		},
		{
			name: "client list error returns errored response",
			ipAlloc: &v1alpha1.IPAddressAllocation{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "ipa5"},
				Status: v1alpha1.IPAddressAllocationStatus{
					Conditions:    []v1alpha1.Condition{{Type: "Ready"}},
					AllocationIPs: "10.0.0.20",
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var clientBuilder *fake.ClientBuilder
			if tt.expectError {
				// Use a fake client that always errors on List
				clientBuilder = fake.NewClientBuilder().WithScheme(scheme)
				// Patch the List method to return error
				cli := clientBuilder.Build()
				v := &IPAddressAllocationValidator{Client: &errorListClient{cli}, decoder: nil}
				resp := v.validateServiceVIP(context.TODO(), admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Namespace: "ns1"}}, tt.ipAlloc)
				if !resp.Allowed && resp.Result != nil && resp.Result.Code == http.StatusInternalServerError {
					return
				}
				t.Errorf("expected error response, got %+v", resp)
				return
			}
			clientBuilder = fake.NewClientBuilder().WithScheme(scheme)
			for _, svc := range tt.services {
				clientBuilder = clientBuilder.WithObjects(&svc)
			}
			v := &IPAddressAllocationValidator{Client: clientBuilder.Build(), decoder: nil}
			resp := v.validateServiceVIP(context.TODO(), admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Namespace: "ns1"}}, tt.ipAlloc)
			if tt.expectAllowed && !resp.Allowed {
				t.Errorf("expected allowed, got denied: %+v", resp)
			}
			if tt.expectDenied {
				if resp.Allowed {
					t.Errorf("expected denied, got allowed")
				}
				if tt.expectedReason != "" && resp.Result != nil && resp.Result.Message != tt.expectedReason {
					t.Errorf("expected reason %q, got %q", tt.expectedReason, resp.Result.Message)
				}
			}
		})
	}
}

// errorListClient wraps a client.Client and always returns error on List.
type errorListClient struct {
	client.Client
}

func (e *errorListClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return errors.New("mock list error")
}
