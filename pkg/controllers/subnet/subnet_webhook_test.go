package subnet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	mockClient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
)

func TestSubnetValidator_Handle(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	scheme := clientgoscheme.Scheme
	err := v1alpha1.AddToScheme(scheme)
	assert.NoError(t, err, "Failed to add v1alpha1 scheme")
	decoder := admission.NewDecoder(scheme)
	v := &SubnetValidator{
		Client:  k8sClient,
		decoder: decoder,
	}

	req1, _ := json.Marshal(&v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-1",
			Name:      "subnet-1",
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 16,
		},
	})
	req2, _ := json.Marshal(&v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-2",
			Name:      "subnet-2",
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 24,
		},
	})
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
			name: "DeleteSuccess",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Delete,
				OldObject: runtime.RawExtension{Raw: req1},
			}}},
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.SubnetPortList)
					a.Items = append(a.Items, v1alpha1.SubnetPort{
						ObjectMeta: metav1.ObjectMeta{Name: "subnetport-1", Namespace: "ns-1"},
						Spec: v1alpha1.SubnetPortSpec{
							Subnet: "subnet-2",
						},
					})
					return nil
				})
			},
			want: admission.Allowed(""),
		},
		{
			name: "DeleteDenied",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Delete,
				OldObject: runtime.RawExtension{Raw: req1},
			}}},
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.SubnetPortList)
					a.Items = append(a.Items, v1alpha1.SubnetPort{
						ObjectMeta: metav1.ObjectMeta{Name: "subnetport-1", Namespace: "ns-1"},
						Spec: v1alpha1.SubnetPortSpec{
							Subnet: "subnet-1",
						},
					})
					return nil
				})
			},
			want: admission.Denied("Subnet ns-1/subnet-1 with stale SubnetPorts cannot be deleted"),
		},
		{
			name: "ListSubnetPortFailure",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Delete,
				OldObject: runtime.RawExtension{Raw: req1},
			}}},
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("list failure"))
			},
			want: admission.Errored(http.StatusBadRequest, errors.New("failed to list SubnetPort: list failure")),
		},
		{
			name: "DecodeOldSubnetFailure",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Delete,
			}}},
			want: admission.Errored(http.StatusBadRequest, errors.New("there is no content to decode")),
		},
		{
			name: "DecodeSubnetFailure",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Create,
			}}},
			want: admission.Errored(http.StatusBadRequest, errors.New("there is no content to decode")),
		},
		{
			name: "CreateSubnet with invalid IPv4SubnetSize",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Create,
				Object:    runtime.RawExtension{Raw: req2},
			}}},
			want: admission.Denied("Subnet ns-2/subnet-2 has invalid size 24, which must be power of 2"),
		},
		{
			name: "CreateSubnet",
			args: args{req: admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Operation: admissionv1.Create,
				Object:    runtime.RawExtension{Raw: req1},
			}}},
			want: admission.Allowed(""),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prepareFunc != nil {
				tt.prepareFunc(t)
			}
			res := v.Handle(context.TODO(), tt.args.req)
			assert.Equal(t, tt.want, res)
		})
	}
}
