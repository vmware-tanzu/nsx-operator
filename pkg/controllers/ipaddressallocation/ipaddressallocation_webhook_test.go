package ipaddressallocation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	mockClient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
)

func TestIPAddressAllocationValidator_Handle(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	scheme := clientgoscheme.Scheme
	_ = v1alpha1.AddToScheme(scheme)
	decoder := admission.NewDecoder(scheme)
	v := &IPAddressAllocationValidator{
		Client:  k8sClient,
		decoder: decoder,
	}

	// Ready allocation with IPs
	ipAlloc := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-1",
			Name:      "alloc-1",
		},
		Status: v1alpha1.IPAddressAllocationStatus{
			Conditions:    []v1alpha1.Condition{{Type: "Ready"}},
			AllocationIPs: "192.168.1.2/32",
		},
	}
	rawAlloc, _ := json.Marshal(ipAlloc)

	type args struct {
		req admission.Request
	}
	tests := []struct {
		name        string
		args        args
		prepareFunc func(t *testing.T)
		want        admission.Response
	}{

		{
			name: "DeleteAllowed_NotReady",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Delete,
				OldObject: runtime.RawExtension{Raw: func() []byte {
					alloc := *ipAlloc
					alloc.Status.Conditions = nil
					b, _ := json.Marshal(&alloc)
					return b
				}()},
			}}},

			want: admission.Allowed("allocation not ready, safe to delete"),
		},

		{
			name: "DeleteDenied_IPInUse",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Delete,
				OldObject: runtime.RawExtension{Raw: rawAlloc},
			}}},
			prepareFunc: func(t *testing.T) {
				ipAlloc.Status.Conditions = []v1alpha1.Condition{{Type: "Ready"}}
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					svcList := list.(*corev1.ServiceList)
					svcList.Items = append(svcList.Items, corev1.Service{
						ObjectMeta: metav1.ObjectMeta{Name: "svc-1"},
						Spec: corev1.ServiceSpec{
							LoadBalancerIP: "192.168.1.2",
						},
					})
					return nil
				})
			},
			want: admission.Denied(fmt.Sprintf("cannot delete IPAddressAllocation %s: IP %s is still in use by Service %s", ipAlloc.Name, "192.168.1.2", "svc-1")),
		},
		{
			name: "DeleteAllowed_NoIPInUse",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Delete,
				OldObject: runtime.RawExtension{Raw: rawAlloc},
			}}},
			prepareFunc: func(t *testing.T) {
				ipAlloc.Status.Conditions = []v1alpha1.Condition{{Type: "Ready"}}
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					svcList := list.(*corev1.ServiceList)
					svcList.Items = append(svcList.Items, corev1.Service{
						ObjectMeta: metav1.ObjectMeta{Name: "svc-1"},
						Spec: corev1.ServiceSpec{
							LoadBalancerIP: "10.0.0.1",
						},
					})
					return nil
				})
			},
			want: admission.Allowed("no services using allocated IPs, safe to delete"),
		},
		{
			name: "Delete_ListServiceError",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Delete,
				OldObject: runtime.RawExtension{Raw: rawAlloc},
			}}},
			prepareFunc: func(t *testing.T) {
				ipAlloc.Status.Conditions = []v1alpha1.Condition{{Type: "Ready"}}
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("list error"))
			},
			want: admission.Errored(http.StatusInternalServerError, errors.New("list error")),
		},
		{
			name: "NotDeleteOperation",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Create,
			}}},
			want: admission.Allowed("operation is not DELETE"),
		},
		{
			name: "DecodeOldObjectError",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Delete,
				OldObject: runtime.RawExtension{Raw: []byte("invalid")},
			}}},
			want: admission.Response{AdmissionResponse: admissionv1.AdmissionResponse{
				Allowed: false,
				Result: &metav1.Status{
					Code: http.StatusBadRequest,
				},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prepareFunc != nil {
				tt.prepareFunc(t)
			}
			res := v.Handle(context.TODO(), tt.args.req)
			if tt.want.Result != nil && tt.want.Result.Code != 0 {
				assert.Equal(t, tt.want.Result.Code, res.Result.Code)
			} else {
				assert.Equal(t, tt.want, res)
			}
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
