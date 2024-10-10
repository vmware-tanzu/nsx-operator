package subnet

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

type MockRateLimitingInterface struct {
	mock.Mock
}

func (m *MockRateLimitingInterface) Add(item interface{}) {
}

func (m *MockRateLimitingInterface) Len() int {
	return 0
}

func (m *MockRateLimitingInterface) Get() (item interface{}, shutdown bool) {
	return
}

func (m *MockRateLimitingInterface) Done(item interface{}) {
	return
}

func (m *MockRateLimitingInterface) ShutDown() {
}

func (m *MockRateLimitingInterface) ShutDownWithDrain() {
}

func (m *MockRateLimitingInterface) ShuttingDown() bool {
	return true
}

func (m *MockRateLimitingInterface) AddAfter(item interface{}, duration time.Duration) {
	return
}

func (m *MockRateLimitingInterface) AddRateLimited(item interface{}) {
	m.Called(item)
}

func (m *MockRateLimitingInterface) Forget(item interface{}) {
	m.Called(item)
}

func (m *MockRateLimitingInterface) NumRequeues(item interface{}) int {
	args := m.Called(item)
	return args.Int(0)
}

func TestEnqueueRequestForNamespace(t *testing.T) {
	fakeClient := fake.NewClientBuilder().Build()
	queue := new(MockRateLimitingInterface)
	handler := &EnqueueRequestForNamespace{Client: fakeClient}

	t.Run("Update event with changed labels", func(t *testing.T) {
		oldNamespace := &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "test-ns",
				Labels: map[string]string{"key": "old"},
			},
		}
		newNamespace := &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "test-ns",
				Labels: map[string]string{"key": "new"},
			},
		}
		updateEvent := event.UpdateEvent{
			ObjectOld: oldNamespace,
			ObjectNew: newNamespace,
		}
		queue.On("Add", mock.Anything).Return()

		handler.Update(context.Background(), updateEvent, queue)
	})

	t.Run("Update event with unchanged labels", func(t *testing.T) {
		oldNamespace := &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "test-ns",
				Labels: map[string]string{"key": "same"},
			},
		}
		newNamespace := &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "test-ns",
				Labels: map[string]string{"key": "same"},
			},
		}
		updateEvent := event.UpdateEvent{
			ObjectOld: oldNamespace,
			ObjectNew: newNamespace,
		}

		queue.On("Add", mock.Anything).Return()

		handler.Update(context.Background(), updateEvent, queue)

		queue.AssertNotCalled(t, "Add", mock.Anything)
	})

	t.Run("Requeue subnet function", func(t *testing.T) {
		ns := "test-ns"

		schem := fake.NewClientBuilder().Build().Scheme()
		v1alpha1.AddToScheme(schem)
		queue.On("Add", mock.Anything).Return()

		err := requeueSubnet(fakeClient, ns, queue)

		assert.NoError(t, err)
		queue.AssertNumberOfCalls(t, "Add", 0)
	})
}
