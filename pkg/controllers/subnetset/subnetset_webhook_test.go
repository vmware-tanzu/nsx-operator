package subnetset

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

func TestSubnetSetValidator(t *testing.T) {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).Build()
	nsxClient := &nsx.Client{}
	cluster, _ := nsx.NewCluster(&nsx.Config{})
	nsxClient.Cluster = cluster
	validator := &SubnetSetValidator{
		Client:    fakeClient,
		decoder:   admission.NewDecoder(newScheme),
		nsxClient: nsxClient,
		vpcService: &vpc.VPCService{
			Service: common.Service{},
		},
	}

	defaultSubnetSet := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:   common.DefaultVMSubnetSet,
			Labels: map[string]string{common.LabelDefaultNetwork: common.DefaultVMNetwork},
		},
	}

	defaultSubnetSet1 := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "fake-subnetset",
			Labels: map[string]string{common.LabelDefaultNetwork: "true"},
		},
	}

	oldDefaultSubnetSet := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:   common.LabelDefaultSubnetSet,
			Labels: map[string]string{common.LabelDefaultSubnetSet: common.DefaultVMSubnetSet},
		},
	}
	precreatedSubnetSet := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:   common.LabelDefaultSubnetSet,
			Labels: map[string]string{common.LabelDefaultSubnetSet: common.DefaultVMSubnetSet},
		},
		Spec: v1alpha1.SubnetSetSpec{
			SubnetNames: &[]string{"subnet-1"},
		},
	}

	invalidSubnetSet := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fake-subnetset",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetSetSpec{
			IPv4SubnetSize: 24,
		},
	}

	subnetSet := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "fake-subnetset",
		},
		Spec: v1alpha1.SubnetSetSpec{
			IPv4SubnetSize: 32,
		},
	}

	conflictSubnetSet := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fake-subnetset",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetSetSpec{
			IPv4SubnetSize: 32,
			SubnetNames:    &[]string{"subnet1", "subnet2"},
		},
	}

	subnetSetWithStalePorts := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "subnetset-1",
			Namespace: "ns-1",
		},
	}

	fakeClient.Create(context.TODO(), &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{Name: "subnetport-1", Namespace: "ns-1"},
		Spec: v1alpha1.SubnetPortSpec{
			SubnetSet: "subnetset-1",
		},
	})
	fakeClient.Create(context.TODO(), &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "subnet-1",
			Namespace:   "ns-1",
			Annotations: map[string]string{common.AnnotationAssociatedResource: "default:ns-1:subnet-1"},
		},
	})
	fakeClient.Create(context.TODO(), &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "subnet-2",
			Namespace:   "ns-1",
			Annotations: map[string]string{common.AnnotationAssociatedResource: "default:ns-2:subnet-2"},
		},
	})
	fakeClient.Create(context.TODO(), &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "subnet-3",
			Namespace: "ns-1",
		},
	})

	patches := gomonkey.ApplyMethod(reflect.TypeOf(validator.vpcService), "ListVPCInfo", func(_ common.VPCServiceProvider, ns string) []common.VPCResourceInfo {
		return []common.VPCResourceInfo{{OrgID: "default", ProjectID: "default", VPCID: "ns-1"}}
	})
	defer patches.Reset()

	testcases := []struct {
		name         string
		op           admissionv1.Operation
		oldSubnetSet *v1alpha1.SubnetSet
		subnetSet    *v1alpha1.SubnetSet
		user         string
		isAllowed    bool
		msg          string
	}{
		{
			name:      "Create default SubnetSet with NSXOperatorSA user",
			op:        admissionv1.Create,
			subnetSet: defaultSubnetSet,
			user:      NSXOperatorSA,
			isAllowed: true,
		},
		{
			name:      "Create default SubnetSet(with default label) with NSXOperatorSA user",
			op:        admissionv1.Create,
			subnetSet: defaultSubnetSet1,
			user:      NSXOperatorSA,
			isAllowed: true,
		},
		{
			name:      "Create default SubnetSet without NSXOperatorSA user",
			op:        admissionv1.Create,
			subnetSet: defaultSubnetSet,
			user:      "fake-user",
			isAllowed: false,
			msg:       "default SubnetSet only can be created by nsx-operator",
		},
		{
			name:      "Create default SubnetSet with invalid IPv4SubnetSize",
			op:        admissionv1.Create,
			subnetSet: invalidSubnetSet,
			user:      NSXOperatorSA,
			isAllowed: false,
			msg:       "SubnetSet ns-1/fake-subnetset has invalid size 24: Subnet size must be a power of 2",
		},
		{
			name: "Create SubnetSet with Subnets belong to the same VPC",
			op:   admissionv1.Create,
			subnetSet: &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fake-subnetset",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetSetSpec{
					SubnetNames: &[]string{"subnet-1", "subnet-3"},
				},
			},
			user:      "fake-user",
			isAllowed: true,
		},
		{
			name: "Create SubnetSet with Subnets belong to 2 VPCs",
			op:   admissionv1.Create,
			subnetSet: &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fake-subnetset",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetSetSpec{
					SubnetNames: &[]string{"subnet-1", "subnet-2"},
				},
			},
			user:      "fake-user",
			isAllowed: false,
		},
		{
			name: "Update SubnetSet with Subnets belong to 2 VPCs",
			op:   admissionv1.Update,
			oldSubnetSet: &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fake-subnetset",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetSetSpec{
					SubnetNames: &[]string{"subnet-1", "subnet-3"},
				},
			},
			subnetSet: &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fake-subnetset",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetSetSpec{
					SubnetNames: &[]string{"subnet-1", "subnet-2", "subnet-3"},
				},
			},
			user:      "fake-user",
			isAllowed: false,
		},
		{
			name:      "Create normal SubnetSet",
			op:        admissionv1.Create,
			subnetSet: subnetSet,
			user:      "fake-user",
			isAllowed: true,
		},
		{
			name:         "Delete default SubnetSet with NSXOperatorSA user",
			op:           admissionv1.Delete,
			oldSubnetSet: defaultSubnetSet,
			user:         NSXOperatorSA,
			isAllowed:    true,
		},
		{
			name:         "Delete default SubnetSet(with default label) with NSXOperatorSA user",
			op:           admissionv1.Delete,
			oldSubnetSet: defaultSubnetSet1,
			user:         NSXOperatorSA,
			isAllowed:    true,
		},
		{
			name:         "Delete default SubnetSet without NSXOperatorSA user",
			op:           admissionv1.Delete,
			oldSubnetSet: defaultSubnetSet,
			user:         "fake-user",
			isAllowed:    false,
			msg:          "default SubnetSet only can be deleted by nsx-operator",
		},
		{
			name:         "Delete normal SubnetSet",
			op:           admissionv1.Delete,
			oldSubnetSet: subnetSet,
			user:         "fake-user",
			isAllowed:    true,
		},
		{
			name:         "Delete SubnetSet with stale SubnetPort",
			op:           admissionv1.Delete,
			oldSubnetSet: subnetSetWithStalePorts,
			user:         "fake-user",
			isAllowed:    false,
			msg:          "SubnetSet ns-1/subnetset-1 with stale SubnetPorts cannot be deleted",
		},
		{
			name:         "Delete SubnetSet with stale SubnetPort by nsx-operator",
			op:           admissionv1.Delete,
			oldSubnetSet: subnetSetWithStalePorts,
			user:         NSXOperatorSA,
			isAllowed:    true,
		},
		{
			name:         "Update normal SubnetSet",
			op:           admissionv1.Update,
			oldSubnetSet: subnetSet,
			subnetSet:    subnetSet,
			user:         "fake-user",
			isAllowed:    true,
		},
		{
			name:         "Update default SubnetSet",
			op:           admissionv1.Update,
			oldSubnetSet: defaultSubnetSet,
			subnetSet:    subnetSet,
			user:         "fake-user",
			isAllowed:    false,
		},
		{
			name:         "Update conflict SubnetSet",
			op:           admissionv1.Update,
			oldSubnetSet: defaultSubnetSet,
			subnetSet:    conflictSubnetSet,
			user:         "fake-user",
			isAllowed:    false,
		},
		{
			name:         "Create conflict SubnetSet",
			op:           admissionv1.Create,
			oldSubnetSet: defaultSubnetSet,
			subnetSet:    conflictSubnetSet,
			user:         "fake-user",
			isAllowed:    false,
		},
		{
			name:         "Update default SubnetSet without NSXOperatorSA user",
			op:           admissionv1.Update,
			oldSubnetSet: defaultSubnetSet,
			subnetSet:    conflictSubnetSet,
			user:         "fake-user",
			isAllowed:    false,
		},
		{
			name:         "Update old format default SubnetSet without NSXOperatorSA user",
			op:           admissionv1.Update,
			oldSubnetSet: oldDefaultSubnetSet,
			subnetSet:    conflictSubnetSet,
			user:         "fake-user",
			isAllowed:    false,
		},
		{
			name:         "Change to old format default SubnetSet without NSXOperatorSA user",
			op:           admissionv1.Update,
			oldSubnetSet: subnetSet,
			subnetSet:    oldDefaultSubnetSet,
			user:         "fake-user",
			isAllowed:    false,
		},
		{
			name:         "Delete old default SubnetSet without NSXOperatorSA user",
			op:           admissionv1.Delete,
			oldSubnetSet: oldDefaultSubnetSet,
			user:         "fake-user",
			isAllowed:    false,
			msg:          "default SubnetSet only can be deleted by nsx-operator",
		},
		{
			name:         "Delete old default SubnetSet with NSXOperatorSA user",
			op:           admissionv1.Delete,
			oldSubnetSet: oldDefaultSubnetSet,
			user:         NSXOperatorSA,
			isAllowed:    true,
		},
		{
			name:         "Not allow SubnetSet switch",
			op:           admissionv1.Update,
			oldSubnetSet: precreatedSubnetSet,
			subnetSet:    subnetSet,
			user:         NSXOperatorSA,
			isAllowed:    false,
		},
	}
	for _, testCase := range testcases {
		t.Run(testCase.name, func(t *testing.T) {
			req := admission.Request{}
			jsonData, err := json.Marshal(testCase.subnetSet)
			assert.NoError(t, err)
			req.Object.Raw = jsonData

			oldJsonData, err := json.Marshal(testCase.oldSubnetSet)
			assert.NoError(t, err)
			req.OldObject.Raw = oldJsonData
			patches := gomonkey.ApplyMethod(reflect.TypeOf(validator.nsxClient.Cluster), "GetVersion", func(_ *nsx.Cluster) (*nsx.NsxVersion, error) {
				return &nsx.NsxVersion{
					NodeVersion: "9.0.0.0.12345",
				}, nil
			})
			defer patches.Reset()
			req.Operation = testCase.op
			req.UserInfo.Username = testCase.user
			response := validator.Handle(context.TODO(), req)
			assert.Equal(t, testCase.isAllowed, response.Allowed)
			if testCase.msg != "" {
				assert.Contains(t, response.Result.Message, testCase.msg)
			}
		})
	}
}

func TestSubnetSetType(t *testing.T) {
	tests := []struct {
		name     string
		input    *v1alpha1.SubnetSet
		expected SubnetSetType
	}{
		{
			name: "PreCreated: populated SubnetNames",
			input: &v1alpha1.SubnetSet{
				Spec: v1alpha1.SubnetSetSpec{
					SubnetNames: &[]string{"subnet-1"},
				},
			},
			expected: SubnetSetTypePreCreated,
		},
		{
			name: "AutoCreated: IPv4SubnetSize set",
			input: &v1alpha1.SubnetSet{
				Spec: v1alpha1.SubnetSetSpec{
					IPv4SubnetSize: 24,
				},
			},
			expected: SubnetSetTypeAutoCreated,
		},
		{
			name: "AutoCreated: AccessMode set",
			input: &v1alpha1.SubnetSet{
				Spec: v1alpha1.SubnetSetSpec{
					AccessMode: "Shared",
				},
			},
			expected: SubnetSetTypeAutoCreated,
		},
		{
			name: "AutoCreated: DHCP Mode set",
			input: &v1alpha1.SubnetSet{
				Spec: v1alpha1.SubnetSetSpec{
					SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{Mode: v1alpha1.DHCPConfigMode("DHCP-Server")},
				},
			},
			expected: SubnetSetTypeAutoCreated,
		},
		{
			name: "None: Empty spec",
			input: &v1alpha1.SubnetSet{
				Spec: v1alpha1.SubnetSetSpec{},
			},
			expected: SubnetSetTypeNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := subnetSetType(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSwitchSubnetSetType(t *testing.T) {
	tests := []struct {
		name     string
		old      *v1alpha1.SubnetSet
		new      *v1alpha1.SubnetSet
		expected bool
	}{
		{
			name: "Switch from PreCreated to AutoCreated",
			old: &v1alpha1.SubnetSet{
				Spec: v1alpha1.SubnetSetSpec{SubnetNames: &[]string{"s1"}},
			},
			new: &v1alpha1.SubnetSet{
				Spec: v1alpha1.SubnetSetSpec{IPv4SubnetSize: 24},
			},
			expected: true,
		},
		{
			name: "Switch from AutoCreated to PreCreated",
			old: &v1alpha1.SubnetSet{
				Spec: v1alpha1.SubnetSetSpec{AccessMode: "Shared"},
			},
			new: &v1alpha1.SubnetSet{
				Spec: v1alpha1.SubnetSetSpec{SubnetNames: &[]string{"s1"}},
			},
			expected: true,
		},
		{
			name: "No Switch: Both PreCreated",
			old: &v1alpha1.SubnetSet{
				Spec: v1alpha1.SubnetSetSpec{SubnetNames: &[]string{"s1"}},
			},
			new: &v1alpha1.SubnetSet{
				Spec: v1alpha1.SubnetSetSpec{SubnetNames: &[]string{"s1", "s2"}},
			},
			expected: false,
		},
		{
			name: "No Switch: From None to AutoCreated (valid transition, not a switch)",
			old: &v1alpha1.SubnetSet{
				Spec: v1alpha1.SubnetSetSpec{},
			},
			new: &v1alpha1.SubnetSet{
				Spec: v1alpha1.SubnetSetSpec{IPv4SubnetSize: 24},
			},
			expected: false,
		},
		{
			name: "Switch: From PreCreated to None (removing SubnetNames not allowed)",
			old: &v1alpha1.SubnetSet{
				Spec: v1alpha1.SubnetSetSpec{SubnetNames: &[]string{"s1"}},
			},
			new: &v1alpha1.SubnetSet{
				Spec: v1alpha1.SubnetSetSpec{},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := switchSubnetSetType(tt.old, tt.new)
			assert.Equal(t, tt.expected, result)
		})
	}
}
