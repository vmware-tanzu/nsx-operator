package subnet

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
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
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
			SubnetName:       "child1",
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
			SubnetName:       "child1",
			TargetSubnetName: "parent2",
			VLANTrafficTag:   102,
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
			VLANTrafficTag:      101,
		},
	}

	bm4 = &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "binding1",
			Namespace: "default",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       "child2",
			TargetSubnetName: "parent3",
			VLANTrafficTag:   101,
		},
	}

	subnet1 = &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "child1", Namespace: "default"},
	}
	subnet2 = &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "parent", Namespace: "default"},
	}
	subnet3 = &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "child2", Namespace: "default"},
	}
	subnet4 = &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "parent2", Namespace: "default"},
	}
	req1 = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "child1",
			Namespace: "default",
		},
	}
	req2 = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "parent",
			Namespace: "default",
		},
	}
	req3 = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "child2",
			Namespace: "default",
		},
	}
	req4 = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "parent2",
			Namespace: "default",
		},
	}
)

func TestRequeueSubnetBySubnetBinding(t *testing.T) {
	myQueue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer myQueue.ShutDown()

	ctx := context.TODO()
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))

	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(subnet1, subnet2, subnet3, subnet4).Build()

	requeueSubnetBySubnetBindingCreate(ctx, fakeClient, bm1, myQueue)
	require.Equal(t, 2, myQueue.Len())
	queueItemEquals(t, myQueue, req1)
	queueItemEquals(t, myQueue, req2)

	requeueSubnetBySubnetBindingCreate(ctx, fakeClient, bm4, myQueue)
	require.Equal(t, 1, myQueue.Len())
	queueItemEquals(t, myQueue, req3)

	requeueSubnetBySubnetBindingCreate(ctx, fakeClient, bm3, myQueue)
	require.Equal(t, 1, myQueue.Len())
	queueItemEquals(t, myQueue, req1)

	requeueSubnetBySubnetBindingUpdate(ctx, fakeClient, bm1, bm1, myQueue)
	require.Equal(t, 0, myQueue.Len())

	requeueSubnetBySubnetBindingUpdate(ctx, fakeClient, bm1, bm2, myQueue)
	require.Equal(t, 2, myQueue.Len())
	queueItemEquals(t, myQueue, req4)
	queueItemEquals(t, myQueue, req2)

	requeueSubnetBySubnetBindingUpdate(ctx, fakeClient, bm1, bm3, myQueue)
	require.Equal(t, 1, myQueue.Len())
	queueItemEquals(t, myQueue, req2)

	requeueSubnetBySubnetBindingUpdate(ctx, fakeClient, bm3, bm1, myQueue)
	require.Equal(t, 1, myQueue.Len())
	queueItemEquals(t, myQueue, req2)

	requeueSubnetBySubnetBindingDelete(ctx, fakeClient, bm1, myQueue)
	require.Equal(t, 2, myQueue.Len())
	queueItemEquals(t, myQueue, req1)
	queueItemEquals(t, myQueue, req2)
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
			SubnetName:       "subnet1",
			TargetSubnetName: "subnet2",
			VLANTrafficTag:   201,
		},
	}
	binding2 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "binding2",
			Namespace: "default",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       "subnet2",
			TargetSubnetName: "subnet3",
			VLANTrafficTag:   201,
		},
	}
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(binding1, binding2).Build()
	r := &SubnetReconciler{
		Client: fakeClient,
	}
	for _, tc := range []struct {
		name                           string
		subnetCR                       *v1alpha1.Subnet
		expSubnetConnectionBindingMaps []v1alpha1.SubnetConnectionBindingMap
	}{
		{
			name: "Success case to list all SubnetConnectionBindingMaps using the Subnet as either CHILD or PARENT",
			subnetCR: &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnet2",
					Namespace: "default",
				},
			},
			expSubnetConnectionBindingMaps: []v1alpha1.SubnetConnectionBindingMap{*binding1, *binding2},
		},
		{
			name: "No SubnetConnectionBindingMap found",
			subnetCR: &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnet4",
					Namespace: "default",
				},
			},
			expSubnetConnectionBindingMaps: []v1alpha1.SubnetConnectionBindingMap{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			actBindings := r.getSubnetBindingCRsBySubnet(ctx, tc.subnetCR)
			assert.ElementsMatch(t, tc.expSubnetConnectionBindingMaps, actBindings)
		})
	}
}

func TestGetNSXSubnetBindingsBySubnet(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		subnetCRUID                    string
		patches                        func(r *SubnetReconciler) *gomonkey.Patches
		expSubnetConnectionBindingMaps []*v1alpha1.SubnetConnectionBindingMap
	}{
		{
			name:        "No NSX VpcSubnet exists for the Subnet CR",
			subnetCRUID: "uuid1",
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				patch := gomonkey.ApplyMethod(reflect.TypeOf(r.BindingService), "GetSubnetConnectionBindingMapCRsBySubnet", func(_ *subnetbinding.BindingService, _ *model.VpcSubnet) []*v1alpha1.SubnetConnectionBindingMap {
					return []*v1alpha1.SubnetConnectionBindingMap{}
				})
				return patch
			},
			expSubnetConnectionBindingMaps: nil,
		}, {
			name:        "No NSX SubnetConnectionBindingMap created for VpcSubnet",
			subnetCRUID: "uuid1",
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				r.SubnetService.SubnetStore.Indexer.Add(&model.VpcSubnet{
					Id: servicecommon.String("id1"),
					Tags: []model.Tag{
						{Scope: servicecommon.String(servicecommon.TagScopeSubnetCRUID), Tag: servicecommon.String("uuid1")},
					},
				})
				patch := gomonkey.ApplyMethod(reflect.TypeOf(r.BindingService), "GetSubnetConnectionBindingMapCRsBySubnet", func(_ *subnetbinding.BindingService, _ *model.VpcSubnet) []*v1alpha1.SubnetConnectionBindingMap {
					return []*v1alpha1.SubnetConnectionBindingMap{}
				})
				return patch
			},
			expSubnetConnectionBindingMaps: nil,
		}, {
			name:        "Partial of VpcSubnets are associated with the NSX SubnetConnectionBindingMap",
			subnetCRUID: "uuid1",
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				r.SubnetService.SubnetStore.Indexer.Add(&model.VpcSubnet{
					Id: servicecommon.String("id1"),
					Tags: []model.Tag{
						{Scope: servicecommon.String(servicecommon.TagScopeSubnetCRUID), Tag: servicecommon.String("uuid1")},
					},
				})
				r.SubnetService.SubnetStore.Indexer.Add(&model.VpcSubnet{
					Id: servicecommon.String("id2"),
					Tags: []model.Tag{
						{Scope: servicecommon.String(servicecommon.TagScopeSubnetCRUID), Tag: servicecommon.String("uuid1")},
					},
				})
				patch := gomonkey.ApplyMethod(reflect.TypeOf(r.BindingService), "GetSubnetConnectionBindingMapCRsBySubnet", func(_ *subnetbinding.BindingService, subnet *model.VpcSubnet) []*v1alpha1.SubnetConnectionBindingMap {
					if subnet != nil && subnet.Id != nil && *subnet.Id == "id1" {
						return []*v1alpha1.SubnetConnectionBindingMap{}
					}
					if subnet != nil && subnet.Id != nil && *subnet.Id == "id2" {
						return []*v1alpha1.SubnetConnectionBindingMap{bm1}
					}
					return []*v1alpha1.SubnetConnectionBindingMap{bm1}
				})
				return patch
			},
			expSubnetConnectionBindingMaps: []*v1alpha1.SubnetConnectionBindingMap{bm1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &SubnetReconciler{
				SubnetService: &subnet.SubnetService{
					SubnetStore: &subnet.SubnetStore{
						ResourceStore: servicecommon.ResourceStore{
							Indexer: cache.NewIndexer(func(obj interface{}) (string, error) {
								return *obj.(*model.VpcSubnet).Id, nil
							}, cache.Indexers{
								servicecommon.TagScopeSubnetCRUID: func(obj interface{}) ([]string, error) {
									if subnet, ok := obj.(*model.VpcSubnet); ok {
										for _, tag := range subnet.Tags {
											if *tag.Scope == servicecommon.TagScopeSubnetCRUID {
												return []string{*tag.Tag}, nil
											}
										}
									}
									return []string{}, nil
								},
							}),
						},
					},
				},
				BindingService: &subnetbinding.BindingService{},
			}
			patches := tc.patches(r)
			defer patches.Reset()

			actBindings := r.getNSXSubnetBindingsBySubnet(tc.subnetCRUID)

			// We need to compare the elements without considering the pointers
			var actBindingsVals []v1alpha1.SubnetConnectionBindingMap
			for _, b := range actBindings {
				if b != nil {
					actBindingsVals = append(actBindingsVals, *b)
				}
			}
			var expBindingsVals []v1alpha1.SubnetConnectionBindingMap
			if tc.expSubnetConnectionBindingMaps != nil {
				for _, b := range tc.expSubnetConnectionBindingMaps {
					if b != nil {
						expBindingsVals = append(expBindingsVals, *b)
					}
				}
			} else {
				expBindingsVals = []v1alpha1.SubnetConnectionBindingMap{}
			}
			if actBindingsVals == nil {
				actBindingsVals = []v1alpha1.SubnetConnectionBindingMap{}
			}
			assert.Equal(t, len(expBindingsVals), len(actBindingsVals))
			if len(expBindingsVals) == len(actBindingsVals) && len(expBindingsVals) > 0 {
				assert.Equal(t, expBindingsVals[0].Name, actBindingsVals[0].Name)
			}
		})
	}
}
