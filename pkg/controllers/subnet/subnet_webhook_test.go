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

	// Regular subnet
	req1, _ := json.Marshal(&v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-1",
			Name:      "subnet-1",
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 16,
		},
	})

	// Subnet with invalid IPv4SubnetSize
	req2, _ := json.Marshal(&v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-2",
			Name:      "subnet-2",
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 24,
		},
	})

	// Shared subnet with annotation
	sharedSubnet, _ := json.Marshal(&v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-3",
			Name:      "shared-subnet",
			Annotations: map[string]string{
				"nsx.vmware.com/associated-resource": "project1:vpc1:subnet1",
			},
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 16,
		},
	})

	// Subnet with VPCName set
	subnetWithVPCName, _ := json.Marshal(&v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-4",
			Name:      "subnet-with-vpc",
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 16,
			VPCName:        "vpc-1",
		},
	})

	// Subnet with VLANConnection set
	subnetWithVLANExt, _ := json.Marshal(&v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-5",
			Name:      "subnet-with-vlan",
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 16,
			VLANConnection: "/infra/distributed-vlan-connections/gatewayconnection-103",
		},
	})

	// For update tests
	oldSubnet, _ := json.Marshal(&v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-6",
			Name:      "subnet-to-update",
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 16,
			IPAddresses:    []string{"192.168.1.0/24"},
		},
	})

	// Updated subnet with changed VPCName
	updatedSubnetVPC, _ := json.Marshal(&v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-6",
			Name:      "subnet-to-update",
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 16,
			VPCName:        "vpc-1",
			IPAddresses:    []string{"192.168.1.0/24"},
		},
	})

	// Updated subnet with changed VLANConnection
	updatedSubnetVLAN, _ := json.Marshal(&v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-6",
			Name:      "subnet-to-update",
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 16,
			VLANConnection: "/infra/distributed-vlan-connections/gatewayconnection-103",
			IPAddresses:    []string{"192.168.1.0/24"},
		},
	})

	// Updated subnet with changed IPAddresses
	updatedSubnetIP, _ := json.Marshal(&v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-6",
			Name:      "subnet-to-update",
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 16,
			IPAddresses:    []string{"192.168.2.0/24"},
		},
	})

	// Old shared subnet for update test
	oldSharedSubnet, _ := json.Marshal(&v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-7",
			Name:      "shared-subnet-to-update",
			Annotations: map[string]string{
				"nsx.vmware.com/associated-resource": "project1:vpc1:subnet1",
			},
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 16,
		},
	})

	type testCase struct {
		name        string
		operation   admissionv1.Operation
		object      []byte
		oldObject   []byte
		user        string
		prepareFunc func(t *testing.T)
		want        admission.Response
	}

	tests := []testCase{
		{
			name:      "DeleteSuccess",
			operation: admissionv1.Delete,
			oldObject: req1,
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			},
			want: admission.Allowed(""),
		},
		{
			name:      "HasStaleSubnetPort",
			operation: admissionv1.Delete,
			oldObject: req1,
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
			name:      "HasStaleSubnetIPReservation",
			operation: admissionv1.Delete,
			oldObject: req1,
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.SubnetIPReservationList)
					a.Items = append(a.Items, v1alpha1.SubnetIPReservation{
						ObjectMeta: metav1.ObjectMeta{Name: "subnetport-1", Namespace: "ns-1"},
						Spec: v1alpha1.SubnetIPReservationSpec{
							Subnet:      "subnet-1",
							NumberOfIPs: 10,
						},
					})
					return nil
				})
			},
			want: admission.Denied("Subnet ns-1/subnet-1 with stale SubnetIPReservations cannot be deleted"),
		},
		{
			name:      "ListSubnetPortFailure",
			operation: admissionv1.Delete,
			oldObject: req1,
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("list failure"))
			},
			want: admission.Errored(http.StatusBadRequest, errors.New("failed to list SubnetPort: list failure")),
		},
		{
			name:      "DecodeOldSubnetFailure",
			operation: admissionv1.Delete,
			want:      admission.Errored(http.StatusBadRequest, errors.New("there is no content to decode")),
		},
		{
			name:      "DecodeSubnetFailure",
			operation: admissionv1.Create,
			want:      admission.Errored(http.StatusBadRequest, errors.New("there is no content to decode")),
		},
		{
			name:      "CreateSubnet with invalid IPv4SubnetSize",
			operation: admissionv1.Create,
			object:    req2,
			want:      admission.Denied("Subnet ns-2/subnet-2 has invalid size 24, which must be power of 2"),
		},
		{
			name:      "CreateSubnet",
			operation: admissionv1.Create,
			object:    req1,
			want:      admission.Allowed(""),
		},
		{
			name:      "Create shared subnet by non-NSX Operator",
			operation: admissionv1.Create,
			object:    sharedSubnet,
			user:      "non-nsx-operator",
			want:      admission.Denied("Shared Subnet ns-3/shared-subnet can only be created by NSX Operator"),
		},
		{
			name:      "Create shared subnet by NSX Operator",
			operation: admissionv1.Create,
			object:    sharedSubnet,
			user:      NSXOperatorSA,
			want:      admission.Allowed(""),
		},
		{
			name:      "Create subnet with VPCName by non-NSX Operator",
			operation: admissionv1.Create,
			object:    subnetWithVPCName,
			user:      "non-nsx-operator",
			want:      admission.Denied("Subnet ns-4/subnet-with-vpc: spec.vpcName can only be set by NSX Operator"),
		},
		{
			name:      "Create subnet with VPCName by NSX Operator",
			operation: admissionv1.Create,
			object:    subnetWithVPCName,
			user:      NSXOperatorSA,
			want:      admission.Allowed(""),
		},
		{
			name:      "Create subnet with VLANConnection by non-NSX Operator",
			operation: admissionv1.Create,
			object:    subnetWithVLANExt,
			user:      "non-nsx-operator",
			want:      admission.Denied("Subnet ns-5/subnet-with-vlan: spec.vlanConnection can only be set by NSX Operator"),
		},
		{
			name:      "Create subnet with VLANConnection by NSX Operator",
			operation: admissionv1.Create,
			object:    subnetWithVLANExt,
			user:      NSXOperatorSA,
			want:      admission.Allowed(""),
		},
		{
			name:      "Update subnet with changed VPCName by non-NSX Operator",
			operation: admissionv1.Update,
			object:    updatedSubnetVPC,
			oldObject: oldSubnet,
			user:      "non-nsx-operator",
			want:      admission.Denied("Subnet ns-6/subnet-to-update: spec.vpcName can only be updated by NSX Operator"),
		},
		{
			name:      "Update subnet with changed VPCName by NSX Operator",
			operation: admissionv1.Update,
			object:    updatedSubnetVPC,
			oldObject: oldSubnet,
			user:      NSXOperatorSA,
			want:      admission.Allowed(""),
		},
		{
			name:      "Update subnet with changed VLANConnection by non-NSX Operator",
			operation: admissionv1.Update,
			object:    updatedSubnetVLAN,
			oldObject: oldSubnet,
			user:      "non-nsx-operator",
			want:      admission.Denied("Subnet ns-6/subnet-to-update: spec.vlanConnection can only be updated by NSX Operator"),
		},
		{
			name:      "Update subnet with changed VLANConnection by NSX Operator",
			operation: admissionv1.Update,
			object:    updatedSubnetVLAN,
			oldObject: oldSubnet,
			user:      NSXOperatorSA,
			want:      admission.Allowed(""),
		},
		{
			name:      "Update subnet with changed IPAddresses",
			operation: admissionv1.Update,
			object:    updatedSubnetIP,
			oldObject: oldSubnet,
			user:      "non-nsx-operator",
			want:      admission.Denied("ipAddresses is immutable"),
		},
		{
			name:      "Update shared subnet by non-NSX Operator",
			operation: admissionv1.Update,
			object:    oldSharedSubnet, // Using same content, just testing the check
			oldObject: oldSharedSubnet,
			user:      "non-nsx-operator",
			want:      admission.Denied("Shared Subnet ns-7/shared-subnet-to-update can only be updated by NSX Operator"),
		},
		{
			name:      "Update shared subnet by NSX Operator",
			operation: admissionv1.Update,
			object:    oldSharedSubnet, // Using same content, just testing the check
			oldObject: oldSharedSubnet,
			user:      NSXOperatorSA,
			want:      admission.Allowed(""),
		},
		{
			name:      "Delete shared subnet by non-NSX Operator",
			operation: admissionv1.Delete,
			oldObject: oldSharedSubnet,
			user:      "non-nsx-operator",
			want:      admission.Denied("Shared Subnet ns-7/shared-subnet-to-update can only be deleted by NSX Operator"),
		},
		{
			name:      "Delete shared subnet by NSX Operator",
			operation: admissionv1.Delete,
			oldObject: oldSharedSubnet,
			user:      NSXOperatorSA,
			want:      admission.Allowed(""),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prepareFunc != nil {
				tt.prepareFunc(t)
			}

			// Create a new request for each test
			req := admission.Request{}

			// Set the operation
			req.Operation = tt.operation

			// Set the object if provided
			if tt.object != nil {
				req.Object.Raw = tt.object
			}

			// Set the old object if provided
			if tt.oldObject != nil {
				req.OldObject.Raw = tt.oldObject
			}

			// Set the user if provided
			if tt.user != "" {
				req.UserInfo.Username = tt.user
			}

			// Call the handler
			res := v.Handle(context.TODO(), req)
			assert.Equal(t, tt.want, res)
		})
	}
}
