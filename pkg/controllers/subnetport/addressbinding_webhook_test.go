package subnetport

import (
	"context"
	"fmt"
	"net/http"
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

func TestAddressBindingValidator_Handle(t *testing.T) {
	req1, _ := json.Marshal(&v1alpha1.AddressBinding{
		ObjectMeta: v1.ObjectMeta{
			Namespace: "ns1",
			Name:      "ab1",
		},
		Spec: v1alpha1.AddressBindingSpec{
			VMName:        "vm1",
			InterfaceName: "inf1",
		},
	})
	req1New, _ := json.Marshal(&v1alpha1.AddressBinding{
		ObjectMeta: v1.ObjectMeta{
			Namespace: "ns1",
			Name:      "ab1",
		},
		Spec: v1alpha1.AddressBindingSpec{
			VMName:        "vm1",
			InterfaceName: "inf1new",
		},
	})
	req2, _ := json.Marshal(&v1alpha1.AddressBinding{
		ObjectMeta: v1.ObjectMeta{
			Namespace: "ns1",
			Name:      "ab2",
		},
		Spec: v1alpha1.AddressBindingSpec{
			VMName:        "vm1",
			InterfaceName: "inf2",
		},
	})
	req3, _ := json.Marshal(&v1alpha1.AddressBinding{
		ObjectMeta: v1.ObjectMeta{
			Namespace: "ns1",
			Name:      "ab3",
		},
		Spec: v1alpha1.AddressBindingSpec{
			VMName:                  "vm1",
			InterfaceName:           "inf3",
			IPAddressAllocationName: "ip1",
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
			name: "delete",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Delete}}},
			want: admission.Allowed(""),
		},
		{
			name: "create decode error",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create}}},
			want: admission.Errored(http.StatusBadRequest, fmt.Errorf("there is no content to decode")),
		},
		{
			name: "create",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Object: runtime.RawExtension{Raw: req1}}}},
			want: admission.Allowed(""),
		},
		{
			name: "create list error",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Object: runtime.RawExtension{Raw: req1}}}},
			prepareFunc: func(t *testing.T, client client.Client, ctx context.Context) *gomonkey.Patches {
				return gomonkey.ApplyMethodSeq(client, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("mock error")},
					Times:  1,
				}})
			},
			want: admission.Errored(http.StatusInternalServerError, fmt.Errorf("mock error")),
		},
		{
			name: "create dup",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Object: runtime.RawExtension{Raw: req2}}}},
			want: admission.Denied("interface already has AddressBinding"),
		},
		{
			name: "update decode error",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update, Object: runtime.RawExtension{Raw: req1}}}},
			want: admission.Errored(http.StatusBadRequest, fmt.Errorf("there is no content to decode")),
		},
		{
			name: "update changed",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update, Object: runtime.RawExtension{Raw: req1New}, OldObject: runtime.RawExtension{Raw: req1}}}},
			want: admission.Denied("update AddressBinding vmName/interfaceName is not allowed"),
		},
		{
			name: "create with valid ip allocation 1",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Object: runtime.RawExtension{Raw: req3}}}},
			prepareFunc: func(t *testing.T, c client.Client, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(c, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				c.Create(context.TODO(), &v1alpha1.IPAddressAllocation{
					TypeMeta:   v1.TypeMeta{},
					ObjectMeta: v1.ObjectMeta{Namespace: "ns1", Name: "ip1"},
					Spec:       v1alpha1.IPAddressAllocationSpec{IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityExternal, AllocationIPs: "10.0.0.8"},
					Status:     v1alpha1.IPAddressAllocationStatus{},
				})
				return patches
			},
			want: admission.Allowed(""),
		},
		{
			name: "create with valid ip allocation 2",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Object: runtime.RawExtension{Raw: req3}}}},
			prepareFunc: func(t *testing.T, c client.Client, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(c, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				c.Create(context.TODO(), &v1alpha1.IPAddressAllocation{
					TypeMeta:   v1.TypeMeta{},
					ObjectMeta: v1.ObjectMeta{Namespace: "ns1", Name: "ip1"},
					Spec:       v1alpha1.IPAddressAllocationSpec{IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityExternal, AllocationSize: 1},
					Status:     v1alpha1.IPAddressAllocationStatus{},
				})
				return patches
			},
			want: admission.Allowed(""),
		},
		{
			name: "create with invalid ip allocation",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Object: runtime.RawExtension{Raw: req3}}}},
			prepareFunc: func(t *testing.T, client client.Client, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(client, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
			want: admission.Denied(fmt.Sprintf("IPAddressAllocation %s does not exist", "ip1")),
		},
		{
			name: "create with invalid visibility",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Object: runtime.RawExtension{Raw: req3}}}},
			prepareFunc: func(t *testing.T, c client.Client, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(c, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				c.Create(context.TODO(), &v1alpha1.IPAddressAllocation{
					TypeMeta:   v1.TypeMeta{},
					ObjectMeta: v1.ObjectMeta{Namespace: "ns1", Name: "ip1"},
					Spec:       v1alpha1.IPAddressAllocationSpec{IPAddressBlockVisibility: ""},
					Status:     v1alpha1.IPAddressAllocationStatus{AllocationIPs: "10.0.0.8"},
				})
				return patches
			},
			want: admission.Denied("IPBlock visibility of IPAddressAllocation must be \"External\""),
		},
		{
			name: "create with specified ip cidr",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Object: runtime.RawExtension{Raw: req3}}}},
			prepareFunc: func(t *testing.T, c client.Client, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(c, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				c.Create(context.TODO(), &v1alpha1.IPAddressAllocation{
					TypeMeta:   v1.TypeMeta{},
					ObjectMeta: v1.ObjectMeta{Namespace: "ns1", Name: "ip1"},
					Spec:       v1alpha1.IPAddressAllocationSpec{IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityExternal, AllocationIPs: "10.0.0.8/24"},
				})
				return patches
			},
			want: admission.Denied("IPAddressAllocation must be a single IP"),
		},
		{
			name: "create with specified ip cidr",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Object: runtime.RawExtension{Raw: req3}}}},
			prepareFunc: func(t *testing.T, c client.Client, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(c, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				c.Create(context.TODO(), &v1alpha1.IPAddressAllocation{
					TypeMeta:   v1.TypeMeta{},
					ObjectMeta: v1.ObjectMeta{Namespace: "ns1", Name: "ip1"},
					Spec:       v1alpha1.IPAddressAllocationSpec{IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityExternal, AllocationSize: 1, AllocationIPs: "10.0.0.8/24"},
				})
				return patches
			},
			want: admission.Denied("IPAddressAllocation must be a single IP"),
		},
		{
			name: "create with specified /32 ip cidr",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Object: runtime.RawExtension{Raw: req3}}}},
			prepareFunc: func(t *testing.T, c client.Client, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(c, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				c.Create(context.TODO(), &v1alpha1.IPAddressAllocation{
					TypeMeta:   v1.TypeMeta{},
					ObjectMeta: v1.ObjectMeta{Namespace: "ns1", Name: "ip1"},
					Spec:       v1alpha1.IPAddressAllocationSpec{IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityExternal, AllocationSize: 1, AllocationIPs: "10.0.0.8/32"},
				})
				return patches
			},
			want: admission.Denied("IPAddressAllocation must be a single IP"),
		},
		{
			name: "create with invalid ip allocation size",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Object: runtime.RawExtension{Raw: req3}}}},
			prepareFunc: func(t *testing.T, c client.Client, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(c, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				c.Create(context.TODO(), &v1alpha1.IPAddressAllocation{
					TypeMeta:   v1.TypeMeta{},
					ObjectMeta: v1.ObjectMeta{Namespace: "ns1", Name: "ip1"},
					Spec:       v1alpha1.IPAddressAllocationSpec{IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityExternal, AllocationSize: 0},
				})
				return patches
			},
			want: admission.Denied("IPAddressAllocation must be a single IP"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := clientgoscheme.Scheme
			v1alpha1.AddToScheme(scheme)
			client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.AddressBinding{}).WithIndex(&v1alpha1.AddressBinding{}, util.AddressBindingNamespaceVMIndexKey, addressBindingNamespaceVMIndexFunc).Build()
			decoder := admission.NewDecoder(scheme)
			ctx := context.TODO()
			client.Create(ctx, &v1alpha1.AddressBinding{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "ns1",
					Name:      "ab2a",
				},
				Spec: v1alpha1.AddressBindingSpec{
					VMName:        "vm1",
					InterfaceName: "inf2",
				},
			})
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc(t, client, ctx)
				defer patches.Reset()
			}
			v := &AddressBindingValidator{
				Client:  client,
				decoder: decoder,
			}
			assert.Equalf(t, tt.want, v.Handle(ctx, tt.args.req), "Handle()")
		})
	}
}
