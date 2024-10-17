package subnetset

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v12 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

type fakeRecorder struct{}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
}

func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}

func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}

func createFakeSubnetSetReconciler(objs []client.Object) *SubnetSetReconciler {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(objs...).Build()
	service := &vpc.VPCService{
		Service: common.Service{
			Client:    fakeClient,
			NSXClient: &nsx.Client{},
		},
	}
	subnetService := &subnet.SubnetService{
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
		SubnetStore: &subnet.SubnetStore{},
	}

	subnetPortService := &subnetport.SubnetPortService{
		Service: common.Service{
			Client:    nil,
			NSXClient: &nsx.Client{},
		},
		SubnetPortStore: nil,
	}

	return &SubnetSetReconciler{
		Client:            fakeClient,
		Scheme:            fake.NewClientBuilder().Build().Scheme(),
		VPCService:        service,
		SubnetService:     subnetService,
		SubnetPortService: subnetPortService,
		Recorder:          &fakeRecorder{},
	}
}

func TestReconcile(t *testing.T) {
	subnetsetName := "test-subnetset"
	ns := "test-namespace"

	testCases := []struct {
		name         string
		expectRes    ctrl.Result
		expectErrStr string
		patches      func(r *SubnetSetReconciler) *gomonkey.Patches
	}{
		{
			name:         "Create a SubnetSet with find VPCNetworkConfig error",
			expectRes:    ResultRequeue,
			expectErrStr: "failed to find VPCNetworkConfig for Namespace",
			patches:      nil,
		},
		{
			// TODO: should check the SubnetSet status has error message, which contains 'ipv4SubnetSize has invalid size'
			name:         "Create a SubnetSet with invalid IPv4SubnetSize",
			expectRes:    ResultNormal,
			expectErrStr: "",
			patches: func(r *SubnetSetReconciler) *gomonkey.Patches {
				vpcnetworkInfo := &common.VPCNetworkConfigInfo{DefaultSubnetSize: 15}
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) *common.VPCNetworkConfigInfo {
					return vpcnetworkInfo
				})
				return patches
			},
		},
		{
			name:         "Create a SubnetSet with error failed to generate SubnetSet tags",
			expectRes:    ResultRequeue,
			expectErrStr: "failed to generate SubnetSet tags",
			patches: func(r *SubnetSetReconciler) *gomonkey.Patches {
				vpcnetworkInfo := &common.VPCNetworkConfigInfo{DefaultSubnetSize: 32}
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) *common.VPCNetworkConfigInfo {
					return vpcnetworkInfo
				})

				patches.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
					id1 := "fake-id"
					path := "fake-path"
					vpcSubnet := model.VpcSubnet{Id: &id1, Path: &path}
					return []*model.VpcSubnet{
						&vpcSubnet,
					}
				})
				return patches
			},
		},
		{
			// return nil and not requeue when UpdateSubnetSetTags failed
			name:         "Create a SubnetSet failed to UpdateSubnetSetTags",
			expectRes:    ResultNormal,
			expectErrStr: "",
			patches: func(r *SubnetSetReconciler) *gomonkey.Patches {
				vpcnetworkInfo := &common.VPCNetworkConfigInfo{DefaultSubnetSize: 32}
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) *common.VPCNetworkConfigInfo {
					return vpcnetworkInfo
				})

				tags := []model.Tag{{Scope: common.String(common.TagScopeVMNamespace), Tag: common.String(ns)}}
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
					id1 := "fake-id"
					path := "fake-path"
					vpcSubnet := model.VpcSubnet{Id: &id1, Path: &path, Tags: tags}
					return []*model.VpcSubnet{
						&vpcSubnet,
					}
				})

				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GenerateSubnetNSTags", func(_ *subnet.SubnetService, obj client.Object) []model.Tag {
					return tags
				})
				return patches
			},
		},
		{
			name:         "Create a SubnetSet with exceed tags",
			expectRes:    ResultNormal,
			expectErrStr: "",
			patches: func(r *SubnetSetReconciler) *gomonkey.Patches {
				vpcnetworkInfo := &common.VPCNetworkConfigInfo{DefaultSubnetSize: 32}
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) *common.VPCNetworkConfigInfo {
					return vpcnetworkInfo
				})

				patches.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
					id1 := "fake-id"
					path := "fake-path"
					vpcSubnet := model.VpcSubnet{Id: &id1, Path: &path}
					return []*model.VpcSubnet{
						&vpcSubnet,
					}
				})

				tags := []model.Tag{{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-tag")}}
				for i := 0; i < common.TagsCountMax; i++ {
					key := fmt.Sprintf("fake-tag-key-%d", i)
					value := common.String(fmt.Sprintf("fake-tag-value-%d", i))
					tags = append(tags, model.Tag{Scope: &key, Tag: value})
				}
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GenerateSubnetNSTags", func(_ *subnet.SubnetService, obj client.Object) []model.Tag {
					return tags
				})
				return patches
			},
		},
		{
			name:         "Create a SubnetSet success",
			expectRes:    ResultNormal,
			expectErrStr: "",
			patches: func(r *SubnetSetReconciler) *gomonkey.Patches {
				vpcnetworkInfo := &common.VPCNetworkConfigInfo{DefaultSubnetSize: 32}
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) *common.VPCNetworkConfigInfo {
					return vpcnetworkInfo
				})

				patches.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
					id1 := "fake-id"
					path := "fake-path"
					vpcSubnet := model.VpcSubnet{Id: &id1, Path: &path}
					return []*model.VpcSubnet{
						&vpcSubnet,
					}
				})

				tags := []model.Tag{{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-tag")}}
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GenerateSubnetNSTags", func(_ *subnet.SubnetService, obj client.Object) []model.Tag {
					return tags
				})

				// UpdateSubnetSetTags
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "UpdateSubnetSetTags", func(_ *subnet.SubnetService, ns string, vpcSubnets []*model.VpcSubnet, tags []model.Tag) error {
					return nil
				})
				return patches
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.TODO()
			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: subnetsetName, Namespace: ns}}

			subnetset := &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      subnetsetName,
					Namespace: ns,
				},
				Spec: v1alpha1.SubnetSetSpec{},
			}
			namespace := &v12.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: ns, Namespace: ns},
			}

			r := createFakeSubnetSetReconciler([]client.Object{subnetset, namespace})
			if testCase.patches != nil {
				patches := testCase.patches(r)
				defer patches.Reset()
			}

			res, err := r.Reconcile(ctx, req)

			if testCase.expectErrStr != "" {
				assert.ErrorContains(t, err, testCase.expectErrStr)
			}
			assert.Equal(t, testCase.expectRes, res)
		})
	}
}

// Test Reconcile - SubnetSet Deletion
func TestReconcile_DeleteSubnetSet(t *testing.T) {
	ctx := context.TODO()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-subnetset", Namespace: "default"}}

	r := createFakeSubnetSetReconciler(nil)

	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
		id1 := "fake-id"
		path := "fake-path"
		vpcSubnet := model.VpcSubnet{Id: &id1, Path: &path}
		return []*model.VpcSubnet{
			&vpcSubnet,
		}
	})
	defer patches.Reset()

	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetPortsOfSubnet", func(_ *subnetport.SubnetPortService, _ string) (ports []*model.VpcSubnetPort) {
		return nil
	})

	patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
		return nil
	})

	// ListSubnetSetID
	patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListSubnetSetID", func(_ *subnet.SubnetService, ctx context.Context) (sets.Set[string], error) {
		res := sets.New[string]("fake-subnetSet-uid-2")
		return res, nil
	})

	res, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, res)
}

// Test Reconcile - SubnetSet Deletion
func TestReconcile_DeleteSubnetSet_WithFinalizer(t *testing.T) {
	ctx := context.TODO()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-subnetset", Namespace: "default"}}

	subnetset := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-subnetset",
			Namespace:         "default",
			DeletionTimestamp: &metav1.Time{Time: time.Now()},
			Finalizers:        []string{"test-Finalizers"},
		},
		Spec: v1alpha1.SubnetSetSpec{},
	}

	r := createFakeSubnetSetReconciler([]client.Object{subnetset})

	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
		id1 := "fake-id"
		path := "fake-path"
		vpcSubnet := model.VpcSubnet{Id: &id1, Path: &path}
		return []*model.VpcSubnet{
			&vpcSubnet,
		}
	})
	defer patches.Reset()

	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetPortsOfSubnet", func(_ *subnetport.SubnetPortService, _ string) (ports []*model.VpcSubnetPort) {
		return nil
	})

	patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
		return nil
	})

	res, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, res)
}

// Test UpdateSuccess and UpdateFail
func TestUpdateSuccess(t *testing.T) {
	ctx := context.TODO()
	subnetset := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-subnetset",
			Namespace: "default",
		},
		Status: v1alpha1.SubnetSetStatus{},
	}

	r := createFakeSubnetSetReconciler([]client.Object{subnetset})

	updateSuccess(r, ctx, subnetset)

	updatedSubnetset := &v1alpha1.SubnetSet{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: "test-subnetset", Namespace: "default"}, updatedSubnetset)
	assert.NoError(t, err)
	// TODO: assert updatedSubnetset.Status.Conditions[0].Message
}

// Test deleteSuccess and deleteFail
func TestDeleteSuccess(t *testing.T) {
	ctx := context.TODO()
	subnetset := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-subnetset",
			Namespace: "default",
		},
		Status: v1alpha1.SubnetSetStatus{},
	}

	r := createFakeSubnetSetReconciler([]client.Object{subnetset})

	deleteFail(r, ctx, subnetset, "fake delete error")

	updatedSubnetset := &v1alpha1.SubnetSet{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: "test-subnetset", Namespace: "default"}, updatedSubnetset)
	assert.NoError(t, err)
	// TODO: assert updatedSubnetset.Status.Conditions[0].Message
}

// Test Merge SubnetSet Status Condition
func TestMergeSubnetSetStatusCondition(t *testing.T) {
	ctx := context.TODO()
	subnetset := &v1alpha1.SubnetSet{
		Status: v1alpha1.SubnetSetStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.Ready,
					Status: v12.ConditionStatus(metav1.ConditionFalse),
				},
			},
		},
	}

	r := createFakeSubnetSetReconciler([]client.Object{subnetset})

	newCondition := v1alpha1.Condition{
		Type:   v1alpha1.Ready,
		Status: v12.ConditionStatus(metav1.ConditionTrue),
	}

	updated := r.mergeSubnetSetStatusCondition(ctx, subnetset, &newCondition)

	assert.True(t, updated)
	assert.Equal(t, v12.ConditionStatus(metav1.ConditionTrue), subnetset.Status.Conditions[0].Status)
}

// Test deleteSubnetBySubnetSetName
func TestDeleteSubnetBySubnetSetName(t *testing.T) {
	ctx := context.TODO()

	r := createFakeSubnetSetReconciler(nil)

	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListSubnetBySubnetSetName", func(_ *subnet.SubnetService, ns, subnetSetName string) []*model.VpcSubnet {
		return []*model.VpcSubnet{}
	})
	defer patches.Reset()

	patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListSubnetSetID", func(_ *subnet.SubnetService, ctx context.Context) (sets.Set[string], error) {
		return nil, nil
	})

	err := r.deleteSubnetBySubnetSetName(ctx, "test-subnetset", "default")
	assert.NoError(t, err)
}

func TestSubnetSetReconciler_CollectGarbage(t *testing.T) {
	r := createFakeSubnetSetReconciler(nil)

	ctx := context.TODO()

	subnetSet := v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "fake-subnetset-uid",
			Name:      "test-subnetset",
			Namespace: "test-namespace",
		},
	}
	subnetSetList := &v1alpha1.SubnetSetList{
		TypeMeta: metav1.TypeMeta{},
		ListMeta: metav1.ListMeta{},
		Items:    []v1alpha1.SubnetSet{subnetSet},
	}

	patches := gomonkey.ApplyFunc(listSubnetSet, func(c client.Client, ctx context.Context, options ...client.ListOption) (*v1alpha1.SubnetSetList, error) {
		return subnetSetList, nil
	})
	defer patches.Reset()

	patches.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
		id1 := "fake-id"
		path := "fake-path"
		vpcSubnet := model.VpcSubnet{Id: &id1, Path: &path}
		return []*model.VpcSubnet{
			&vpcSubnet,
		}
	})
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetPortsOfSubnet", func(_ *subnetport.SubnetPortService, _ string) (ports []*model.VpcSubnetPort) {
		return nil
	})
	patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
		return nil
	})

	patches.ApplyMethod(reflect.TypeOf(&common.ResourceStore{}), "ListIndexFuncValues", func(_ *common.ResourceStore, _ string) sets.Set[string] {
		res := sets.New[string]("fake-subnetSet-uid-2")
		return res
	})
	// ListSubnetCreatedBySubnetSet
	patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListSubnetCreatedBySubnetSet", func(_ *subnet.SubnetService, id string) []*model.VpcSubnet {
		id1 := "fake-id"
		path := "fake-path"
		vpcSubnet := model.VpcSubnet{Id: &id1, Path: &path}
		return []*model.VpcSubnet{
			&vpcSubnet,
		}
	})

	r.CollectGarbage(ctx)
}
