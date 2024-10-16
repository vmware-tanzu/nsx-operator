package subnetset

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestSubnetSetValidator(t *testing.T) {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).Build()

	validator := &SubnetSetValidator{
		Client:  fakeClient,
		decoder: admission.NewDecoder(newScheme),
	}

	defaultSubnetSet := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:   common.DefaultVMSubnetSet,
			Labels: map[string]string{common.LabelDefaultSubnetSet: "true"},
		},
	}

	defaultSubnetSet1 := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "fake-subnetset",
			Labels: map[string]string{common.LabelDefaultSubnetSet: "true"},
		},
	}

	subnetSet := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "fake-subnetset",
		},
	}

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
