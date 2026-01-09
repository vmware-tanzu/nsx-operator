package subnet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	controllercommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	mockClient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

func TestSubnetValidator_Handle(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	scheme := clientgoscheme.Scheme
	err := v1alpha1.AddToScheme(scheme)
	assert.NoError(t, err, "Failed to add v1alpha1 scheme")
	decoder := admission.NewDecoder(scheme)
	nsxClient := &nsx.Client{}
	cluster, _ := nsx.NewCluster(&nsx.Config{})
	nsxClient.Cluster = cluster
	v := &SubnetValidator{
		Client:    k8sClient,
		decoder:   decoder,
		nsxClient: nsxClient,
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

	// Subnet with l2 only access mode
	req3, _ := json.Marshal(&v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-3",
			Name:      "subnet-3",
		},
		Spec: v1alpha1.SubnetSpec{
			AccessMode: v1alpha1.AccessMode(v1alpha1.AccessModeL2Only),
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

	// Subnet with VLANConnectionName set
	subnetWithVLANExt, _ := json.Marshal(&v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-5",
			Name:      "subnet-with-vlan",
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize:     16,
			VLANConnectionName: "gatewayconnection-103",
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

	// Updated subnet with changed VLANConnectionName
	updatedSubnetVLAN, _ := json.Marshal(&v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-6",
			Name:      "subnet-to-update",
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize:     16,
			VLANConnectionName: "gatewayconnection-103",
			IPAddresses:        []string{"192.168.1.0/24"},
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

	// Subnet with invalid IPv4SubnetSize
	req8, _ := json.Marshal(&v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-8",
			Name:      "subnet-with-min-size",
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 8,
		},
	})

	type testCase struct {
		name            string
		operation       admissionv1.Operation
		object          []byte
		oldObject       []byte
		user            string
		prepareFunc     func(t *testing.T)
		want            admission.Response
		accessModeCheck bool
	}

	tests := []testCase{
		{
			name:      "DeleteSuccess",
			operation: admissionv1.Delete,
			oldObject: req1,
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(3)
			},
			want:            admission.Allowed(""),
			accessModeCheck: true,
		},
		{
			name:      "HasStaleSubnetSet",
			operation: admissionv1.Delete,
			oldObject: req1,
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.SubnetSetList)
					a.Items = append(a.Items, v1alpha1.SubnetSet{
						ObjectMeta: metav1.ObjectMeta{Name: "subnetport-1", Namespace: "ns-1"},
						Spec: v1alpha1.SubnetSetSpec{
							SubnetNames: []string{"subnet-1"},
						},
					})
					return nil
				})
			},
			want: admission.Denied("Subnet ns-1/subnet-1 used by SubnetSet cannot be deleted"),
		},
		{
			name:      "HasStaleSubnetPort",
			operation: admissionv1.Delete,
			oldObject: req1,
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
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
			want:            admission.Denied("Subnet ns-1/subnet-1 with stale SubnetPorts cannot be deleted"),
			accessModeCheck: true,
		},
		{
			name:      "HasStaleSubnetIPReservation",
			operation: admissionv1.Delete,
			oldObject: req1,
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
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
			want:            admission.Denied("Subnet ns-1/subnet-1 with stale SubnetIPReservations cannot be deleted"),
			accessModeCheck: true,
		},
		{
			name:      "ListSubnetPortFailure",
			operation: admissionv1.Delete,
			oldObject: req1,
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("list failure"))
			},
			want: admission.Errored(http.StatusBadRequest, errors.New("failed to list SubnetPort: list failure")),
		},
		{
			name:      "ListSubnetSetFailure",
			operation: admissionv1.Delete,
			oldObject: req1,
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("list failure"))
			},
			want: admission.Errored(http.StatusBadRequest, errors.New("failed to list SubnetSet: list failure")),
		},
		{
			name:            "DecodeOldSubnetFailure",
			operation:       admissionv1.Delete,
			want:            admission.Errored(http.StatusBadRequest, errors.New("there is no content to decode")),
			accessModeCheck: true,
		},
		{
			name:            "DecodeSubnetFailure",
			operation:       admissionv1.Create,
			want:            admission.Errored(http.StatusBadRequest, errors.New("there is no content to decode")),
			accessModeCheck: true,
		},
		{
			name:            "CreateSubnet with invalid IPv4SubnetSize",
			operation:       admissionv1.Create,
			object:          req2,
			want:            admission.Denied("Subnet ns-2/subnet-2 has invalid size 24: Subnet size must be a power of 2"),
			accessModeCheck: true,
		},
		{
			name:            "CreateSubnet with invalid IPv4SubnetSize 8",
			operation:       admissionv1.Create,
			object:          req8,
			want:            admission.Denied("Subnet ns-8/subnet-with-min-size has invalid size 8: Subnet size must be greater than or equal to 16"),
			accessModeCheck: true,
		},
		{
			name:            "CreateSubnet with invalid AccessMode",
			operation:       admissionv1.Create,
			object:          req3,
			want:            admission.Denied("Subnet ns-3/subnet-3: spec.accessMode L2Only is not supported"),
			accessModeCheck: true,
		},
		{
			name:            "CreateSubnet",
			operation:       admissionv1.Create,
			object:          req1,
			want:            admission.Allowed(""),
			accessModeCheck: true,
		},
		{
			name:            "CreateSubnet with acccessMode private in VLANBacked VPC",
			operation:       admissionv1.Create,
			object:          req1,
			want:            admission.Denied("AccessMode other than Public is not supported VLANBackedVPC VPC"),
			accessModeCheck: false,
		},
		{
			name:            "Create shared subnet by non-NSX Operator",
			operation:       admissionv1.Create,
			object:          sharedSubnet,
			user:            "non-nsx-operator",
			want:            admission.Denied("Shared Subnet ns-3/shared-subnet can only be created by NSX Operator"),
			accessModeCheck: true,
		},
		{
			name:            "Create shared subnet by NSX Operator",
			operation:       admissionv1.Create,
			object:          sharedSubnet,
			user:            NSXOperatorSA,
			want:            admission.Allowed(""),
			accessModeCheck: true,
		},
		{
			name:            "Create subnet with VPCName by non-NSX Operator",
			operation:       admissionv1.Create,
			object:          subnetWithVPCName,
			user:            "non-nsx-operator",
			want:            admission.Denied("Subnet ns-4/subnet-with-vpc: spec.vpcName can only be set by NSX Operator"),
			accessModeCheck: true,
		},
		{
			name:            "Create subnet with VPCName by NSX Operator",
			operation:       admissionv1.Create,
			object:          subnetWithVPCName,
			user:            NSXOperatorSA,
			want:            admission.Allowed(""),
			accessModeCheck: true,
		},
		{
			name:            "Create subnet with VLANConnectionName non-NSX Operator",
			operation:       admissionv1.Create,
			object:          subnetWithVLANExt,
			user:            "non-nsx-operator",
			want:            admission.Denied("Subnet ns-5/subnet-with-vlan: spec.vlanConnectionName can only be set by NSX Operator"),
			accessModeCheck: true,
		},
		{
			name:            "Create subnet with VLANConnectionName by NSX Operator",
			operation:       admissionv1.Create,
			object:          subnetWithVLANExt,
			user:            NSXOperatorSA,
			want:            admission.Allowed(""),
			accessModeCheck: true,
		},
		{
			name:            "Update subnet with changed VPCName by non-NSX Operator",
			operation:       admissionv1.Update,
			object:          updatedSubnetVPC,
			oldObject:       oldSubnet,
			user:            "non-nsx-operator",
			want:            admission.Denied("Subnet ns-6/subnet-to-update: spec.vpcName can only be updated by NSX Operator"),
			accessModeCheck: true,
		},
		{
			name:            "Update subnet with changed VPCName by NSX Operator",
			operation:       admissionv1.Update,
			object:          updatedSubnetVPC,
			oldObject:       oldSubnet,
			user:            NSXOperatorSA,
			want:            admission.Allowed(""),
			accessModeCheck: true,
		},

		{
			name:            "Update subnet with changed VLANConnectionName by non-NSX Operator",
			operation:       admissionv1.Update,
			object:          updatedSubnetVLAN,
			oldObject:       oldSubnet,
			user:            "non-nsx-operator",
			want:            admission.Denied("Subnet ns-6/subnet-to-update: spec.vlanConnectionName can only be updated by NSX Operator"),
			accessModeCheck: true,
		},
		{
			name:            "Update subnet with changed VLANConnectionName by NSX Operator",
			operation:       admissionv1.Update,
			object:          updatedSubnetVLAN,
			oldObject:       oldSubnet,
			user:            NSXOperatorSA,
			want:            admission.Allowed(""),
			accessModeCheck: true,
		},
		{
			name:            "Update subnet with changed IPAddresses",
			operation:       admissionv1.Update,
			object:          updatedSubnetIP,
			oldObject:       oldSubnet,
			user:            "non-nsx-operator",
			want:            admission.Denied("ipAddresses is immutable"),
			accessModeCheck: true,
		},
		{
			name:            "Update shared subnet by non-NSX Operator",
			operation:       admissionv1.Update,
			object:          oldSharedSubnet, // Using same content, just testing the check
			oldObject:       oldSharedSubnet,
			user:            "non-nsx-operator",
			want:            admission.Denied("Shared Subnet ns-7/shared-subnet-to-update can only be updated by NSX Operator"),
			accessModeCheck: true,
		},
		{
			name:            "Update shared subnet by NSX Operator",
			operation:       admissionv1.Update,
			object:          oldSharedSubnet, // Using same content, just testing the check
			oldObject:       oldSharedSubnet,
			user:            NSXOperatorSA,
			want:            admission.Allowed(""),
			accessModeCheck: true,
		},
		{
			name:            "Delete shared subnet by non-NSX Operator",
			operation:       admissionv1.Delete,
			oldObject:       oldSharedSubnet,
			user:            "non-nsx-operator",
			want:            admission.Denied("Shared Subnet ns-7/shared-subnet-to-update can only be deleted by NSX Operator"),
			accessModeCheck: true,
		},
		{
			name:      "Delete shared subnet by NSX Operator",
			operation: admissionv1.Delete,
			oldObject: oldSharedSubnet,
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			},
			user:            NSXOperatorSA,
			want:            admission.Allowed(""),
			accessModeCheck: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prepareFunc != nil {
				tt.prepareFunc(t)
			}
			patches := gomonkey.ApplyMethod(reflect.TypeOf(v.nsxClient.Cluster), "GetVersion", func(_ *nsx.Cluster) (*nsx.NsxVersion, error) {
				return &nsx.NsxVersion{
					NodeVersion: "9.0.0.0.12345",
				}, nil
			})
			patches.ApplyFunc(controllercommon.CheckAccessModeOrVisibility, func(_ client.Client, ctx context.Context, ns string, accessMode string, resourceType string) error {
				if tt.accessModeCheck {
					return nil
				} else {
					return errors.New("AccessMode other than Public is not supported VLANBackedVPC VPC")
				}
			})
			defer patches.Reset()
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
