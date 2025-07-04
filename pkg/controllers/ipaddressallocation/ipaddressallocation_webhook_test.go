package ipaddressallocation

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"
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
		ObjectMeta: v1.ObjectMeta{
			Namespace: "ns1",
			Name:      "ip1",
		},
		Spec: v1alpha1.IPAddressAllocationSpec{
			IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityExternal,
			AllocationIPs:            "10.0.0.8",
		},
	})
	reqCreate, _ := json.Marshal(&v1alpha1.IPAddressAllocation{
		ObjectMeta: v1.ObjectMeta{
			Namespace: "ns1",
			Name:      "ip2",
		},
		Spec: v1alpha1.IPAddressAllocationSpec{
			IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityExternal,
			AllocationIPs:            "10.0.0.9",
		},
	})
	reqUpdate, _ := json.Marshal(&v1alpha1.IPAddressAllocation{
		ObjectMeta: v1.ObjectMeta{
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
					ObjectMeta: v1.ObjectMeta{Namespace: "ns1", Name: "ab1"},
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
