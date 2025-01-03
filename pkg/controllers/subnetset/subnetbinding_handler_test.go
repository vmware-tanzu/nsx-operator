package subnetset

import (
	"context"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetbinding"
)

var (
	bm1 = &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "binding1",
			Namespace: "default",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       "child",
			TargetSubnetName: "parent",
			VLANTrafficTag:   101,
		},
	}

	bm2 = &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "binding1",
			Namespace: "default",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:          "child1",
			TargetSubnetSetName: "parentSet",
			VLANTrafficTag:      102,
		},
	}

	bm3 = &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "binding1",
			Namespace: "default",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:          "child1",
			TargetSubnetSetName: "parentSet2",
			VLANTrafficTag:      102,
		},
	}

	subnet1 = &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "child", Namespace: "default"},
	}
	subnet2 = &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "parent", Namespace: "default"},
	}
	subnetSet1 = &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{Name: "parentSet", Namespace: "default"},
	}
	subnetSet2 = &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{Name: "parentSet2", Namespace: "default"},
	}
	req1 = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "parentSet",
			Namespace: "default",
		},
	}
	req2 = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "parentSet2",
			Namespace: "default",
		},
	}
)

func TestRequeueSubnetSetByBindingMap(t *testing.T) {
	myQueue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer myQueue.ShutDown()

	ctx := context.TODO()
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))

	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(subnet1, subnet2, subnetSet1, subnetSet2).Build()

	requeueSubnetSetBySubnetBindingCreate(ctx, fakeClient, bm1, myQueue)
	require.Equal(t, 0, myQueue.Len())

	requeueSubnetSetBySubnetBindingCreate(ctx, fakeClient, bm2, myQueue)
	require.Equal(t, 1, myQueue.Len())
	queueItemEquals(t, myQueue, req1)

	requeueSubnetSetBySubnetBindingUpdate(ctx, fakeClient, bm2, bm2, myQueue)
	require.Equal(t, 0, myQueue.Len())

	requeueSubnetSetBySubnetBindingUpdate(ctx, fakeClient, bm1, bm2, myQueue)
	require.Equal(t, 1, myQueue.Len())
	queueItemEquals(t, myQueue, req1)

	requeueSubnetSetBySubnetBindingUpdate(ctx, fakeClient, bm2, bm3, myQueue)
	require.Equal(t, 2, myQueue.Len())
	queueItemEquals(t, myQueue, req2)
	queueItemEquals(t, myQueue, req1)

	requeueSubnetSetBySubnetBindingUpdate(ctx, fakeClient, bm1, bm3, myQueue)
	require.Equal(t, 1, myQueue.Len())
	queueItemEquals(t, myQueue, req2)

	requeueSubnetSetBySubnetBindingUpdate(ctx, fakeClient, bm3, bm1, myQueue)
	require.Equal(t, 1, myQueue.Len())
	queueItemEquals(t, myQueue, req2)

	requeueSubnetSetBySubnetBindingDelete(ctx, fakeClient, bm1, myQueue)
	require.Equal(t, 0, myQueue.Len())

	requeueSubnetSetBySubnetBindingDelete(ctx, fakeClient, bm2, myQueue)
	require.Equal(t, 1, myQueue.Len())
	queueItemEquals(t, myQueue, req1)
}

func queueItemEquals(t *testing.T, myQueue workqueue.TypedRateLimitingInterface[reconcile.Request], req reconcile.Request) {
	item, _ := myQueue.Get()
	assert.Equal(t, req, item)
	myQueue.Done(item)
}

func TestGetSubnetBindingCRsBySubnet(t *testing.T) {
	binding1 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "binding1",
			Namespace: "default",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:          "subnet1",
			TargetSubnetSetName: "subnetset1",
			VLANTrafficTag:      201,
		},
	}
	binding2 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "binding2",
			Namespace: "default",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       "subnet2",
			TargetSubnetName: "subnet1",
			VLANTrafficTag:   201,
		},
	}
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(binding1, binding2).Build()
	r := &SubnetSetReconciler{
		Client: fakeClient,
	}
	for _, tc := range []struct {
		name                           string
		subnetSetCR                    *v1alpha1.SubnetSet
		expSubnetConnectionBindingMaps []v1alpha1.SubnetConnectionBindingMap
	}{
		{
			name: "Success case to list SubnetConnectionBindingMaps using the target SubnetSet",
			subnetSetCR: &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnetset1",
					Namespace: "default",
				},
			},
			expSubnetConnectionBindingMaps: []v1alpha1.SubnetConnectionBindingMap{*binding1},
		},
		{
			name: "No SubnetConnectionBindingMap found",
			subnetSetCR: &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnetset2",
					Namespace: "default",
				},
			},
			expSubnetConnectionBindingMaps: []v1alpha1.SubnetConnectionBindingMap{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			actBindings := r.getSubnetBindingCRsBySubnetSet(ctx, tc.subnetSetCR)
			assert.ElementsMatch(t, tc.expSubnetConnectionBindingMaps, actBindings)
		})
	}
}

func TestGetNSXSubnetBindingsBySubnet(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		subnetsetCRUID                 string
		patches                        func(r *SubnetSetReconciler) *gomonkey.Patches
		expSubnetConnectionBindingMaps []*v1alpha1.SubnetConnectionBindingMap
	}{
		{
			name:           "No NSX VpcSubnet exists for the Subnet CR",
			subnetsetCRUID: "uuid1",
			patches: func(r *SubnetSetReconciler) *gomonkey.Patches {
				patch := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListSubnetCreatedBySubnetSet", func(_ *subnet.SubnetService, _ string) []*model.VpcSubnet {
					return []*model.VpcSubnet{}
				})
				return patch
			},
			expSubnetConnectionBindingMaps: nil,
		}, {
			name:           "No NSX SubnetConnectionBindingMap created for VpcSubnet",
			subnetsetCRUID: "uuid1",
			patches: func(r *SubnetSetReconciler) *gomonkey.Patches {
				patch := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListSubnetCreatedBySubnetSet", func(_ *subnet.SubnetService, _ string) []*model.VpcSubnet {
					return []*model.VpcSubnet{
						{Id: common.String("id1")},
					}
				})
				patch.ApplyMethod(reflect.TypeOf(r.BindingService), "GetSubnetConnectionBindingMapCRsBySubnet", func(_ *subnetbinding.BindingService, _ *model.VpcSubnet) []*v1alpha1.SubnetConnectionBindingMap {
					return []*v1alpha1.SubnetConnectionBindingMap{}
				})
				return patch
			},
			expSubnetConnectionBindingMaps: nil,
		}, {
			name:           "Partial of VpcSubnets are associated with the NSX SubnetConnectionBindingMap",
			subnetsetCRUID: "uuid1",
			patches: func(r *SubnetSetReconciler) *gomonkey.Patches {
				patch := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListSubnetCreatedBySubnetSet", func(_ *subnet.SubnetService, _ string) []*model.VpcSubnet {
					return []*model.VpcSubnet{
						{Id: common.String("id1")},
						{Id: common.String("id2")},
					}
				})
				patch.ApplyMethod(reflect.TypeOf(r.BindingService), "GetSubnetConnectionBindingMapCRsBySubnet", func(_ *subnetbinding.BindingService, subnet *model.VpcSubnet) []*v1alpha1.SubnetConnectionBindingMap {
					if *subnet.Id == "id1" {
						return []*v1alpha1.SubnetConnectionBindingMap{}
					}
					return []*v1alpha1.SubnetConnectionBindingMap{bm1}
				})
				return patch
			},
			expSubnetConnectionBindingMaps: []*v1alpha1.SubnetConnectionBindingMap{bm1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &SubnetSetReconciler{
				SubnetService:  &subnet.SubnetService{},
				BindingService: &subnetbinding.BindingService{},
			}
			patches := tc.patches(r)
			defer patches.Reset()

			actBindings := r.getNSXSubnetBindingsBySubnetSet(tc.subnetsetCRUID)
			assert.ElementsMatch(t, tc.expSubnetConnectionBindingMaps, actBindings)
		})
	}
}
