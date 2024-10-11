package subnet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestGenerateSubnetNSTags(t *testing.T) {
	scheme := clientgoscheme.Scheme
	v1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	service := &SubnetService{
		Service: common.Service{
			Client:    fakeClient,
			NSXClient: &nsx.Client{},

			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint:   "vmc-enforcementpoint",
					UseAVILoadBalancer: false,
				},
			},
		},
	}

	// Create a test namespace
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-ns",
			UID:  "namespace-uid",
			Labels: map[string]string{
				"env": "test",
			},
		},
	}

	assert.NoError(t, fakeClient.Create(context.TODO(), namespace))

	// Define the Subnet object
	subnet := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-subnet",
			Namespace: "test-ns",
		},
	}

	// Generate tags for the Subnet
	tags := service.GenerateSubnetNSTags(subnet)

	// Validate the tags
	assert.NotNil(t, tags)
	assert.Equal(t, 3, len(tags)) // 3 tags should be generated

	// Check specific tags
	assert.Equal(t, "namespace-uid", *tags[0].Tag)
	assert.Equal(t, "test-ns", *tags[1].Tag)
	assert.Equal(t, "test", *tags[2].Tag)

	// Define the SubnetSet object
	subnetSet := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-subnet-set",
			Namespace: "test-ns",
			Labels: map[string]string{
				common.LabelDefaultSubnetSet: common.LabelDefaultPodSubnetSet,
			},
		},
	}

	// Generate tags for the SubnetSet
	tagsSet := service.GenerateSubnetNSTags(subnetSet)

	// Validate the tags for SubnetSet
	assert.NotNil(t, tagsSet)
	assert.Equal(t, 3, len(tagsSet)) // 3 tags should be generated
	assert.Equal(t, "namespace-uid", *tagsSet[0].Tag)
	assert.Equal(t, "test-ns", *tagsSet[1].Tag)
}
