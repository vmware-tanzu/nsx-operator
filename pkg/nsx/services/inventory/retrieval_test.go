package inventory

import (
	"context"
	"errors"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestGetPodIDsFromEndpoint(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	_, k8sClient := createService(t)

	ctx := context.TODO()
	name := "test-service"
	namespace := "default"

	endpoints := &v1.Endpoints{
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						TargetRef: &v1.ObjectReference{
							Kind: "Pod",
							UID:  "pod-uid-123",
						},
					},
				},
			},
		},
	}

	k8sClient.EXPECT().
		Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, o client.Object, _ ...client.GetOption) error {
			res, ok := o.(*v1.Endpoints)
			if !ok {
				return errors.New("invalid type")
			}
			*res = *endpoints
			return nil
		})

	podIDs, hasAddr := GetPodIDsFromEndpoint(ctx, k8sClient, name, namespace)

	assert.Contains(t, podIDs, "pod-uid-123")
	assert.True(t, hasAddr)
}

func TestGetPodByUID(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	_, k8sClient := createService(t)

	ctx := context.TODO()
	uid := types.UID("pod-uid-123")
	namespace := "default"

	podList := &v1.PodList{
		Items: []v1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					UID: uid,
				},
				Status: v1.PodStatus{},
			},
		},
	}

	k8sClient.EXPECT().
		List(ctx, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, o client.ObjectList, _ ...client.ListOption) error {
			pl, ok := o.(*v1.PodList)
			if !ok {
				return errors.New("invalid type")
			}
			*pl = *podList
			return nil
		})

	pod, err := GetPodByUID(ctx, k8sClient, uid, namespace)

	assert.NoError(t, err)
	assert.Equal(t, uid, pod.UID)
}

func TestGetServicesUIDByPodUID(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	_, k8sClient := createService(t)

	ctx := context.TODO()
	podUID := types.UID("pod-uid-123")
	namespace := "default"

	serviceList := &v1.ServiceList{
		Items: []v1.Service{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "service1",
					UID:  "service-uid-123",
				},
			},
		},
	}

	endpoints := &v1.Endpoints{
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						TargetRef: &v1.ObjectReference{
							UID: podUID,
						},
					},
				},
			},
		},
	}

	k8sClient.EXPECT().
		List(ctx, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, o client.ObjectList, _ ...client.ListOption) error {
			sl, ok := o.(*v1.ServiceList)
			if !ok {
				return errors.New("invalid type")
			}
			*sl = *serviceList
			return nil
		})

	k8sClient.EXPECT().
		Get(ctx, types.NamespacedName{Name: "service1", Namespace: namespace}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, o client.Object, _ ...client.GetOption) error {
			e, ok := o.(*v1.Endpoints)
			if !ok {
				return errors.New("invalid type")
			}
			*e = *endpoints
			return nil
		})

	serviceUIDs, err := GetServicesUIDByPodUID(ctx, k8sClient, podUID, namespace)

	assert.NoError(t, err)
	assert.Contains(t, serviceUIDs, "service-uid-123")
}
