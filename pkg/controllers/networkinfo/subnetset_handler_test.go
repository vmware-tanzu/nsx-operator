package networkinfo

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestPredicateFuncsSubnetSet_DeleteFunc(t *testing.T) {
	subnetSet := &v1alpha1.SubnetSet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "pod-default",
			Namespace: "ns-1",
			Labels: map[string]string{
				common.LabelDefaultNetwork: "pod",
			},
		},
	}
	deleteEvent := event.DeleteEvent{
		Object: subnetSet,
	}
	result := PredicateFuncsSubnetSet.DeleteFunc(deleteEvent)
	assert.True(t, result)

	subnetSet = &v1alpha1.SubnetSet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnetset-1",
			Namespace: "ns-1",
		},
	}
	deleteEvent = event.DeleteEvent{
		Object: subnetSet,
	}
	result = PredicateFuncsSubnetSet.DeleteFunc(deleteEvent)
	assert.False(t, result)
}

func TestSubnetSetHandler_Delete(t *testing.T) {
	subnetSet := &v1alpha1.SubnetSet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "pod-default",
			Namespace: "ns-1",
			Labels: map[string]string{
				common.LabelDefaultNetwork: "pod",
			},
		},
	}
	deleteEvent := event.DeleteEvent{
		Object: subnetSet,
	}
	queue := workqueue.NewTypedRateLimitingQueue(
		workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())

	h := SubnetSetHandler{}
	h.Delete(context.TODO(), deleteEvent, queue)
	assert.Equal(t, 1, queue.Len())
}
