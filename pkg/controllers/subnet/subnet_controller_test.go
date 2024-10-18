package subnet

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
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
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

func TestSubnetReconciler_GarbageCollector(t *testing.T) {
	subnetStore := &subnet.SubnetStore{}
	service := &subnet.SubnetService{
		SubnetStore: subnetStore,
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
	}
	serviceSubnetPort := &subnetport.SubnetPortService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
	}
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	r := &SubnetReconciler{
		Client:            k8sClient,
		Scheme:            nil,
		SubnetService:     service,
		SubnetPortService: serviceSubnetPort,
	}

	// Subnet doesn't have TagScopeSubnetSetCRId (not  belong to SubnetSet)
	// gc collect item "fake-id1", local store has more item than k8s cache
	patch := gomonkey.ApplyMethod(reflect.TypeOf(&common.ResourceStore{}), "ListIndexFuncValues", func(_ *common.ResourceStore, _ string) sets.Set[string] {
		res := sets.New[string]("fake-id1", "fake-id2")
		return res
	})
	patch.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, _ string) []*model.VpcSubnet {
		tags1 := []model.Tag{{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-id1")}}
		tags2 := []model.Tag{{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-id2")}}
		var a []*model.VpcSubnet
		id1 := "fake-id1"
		a = append(a, &model.VpcSubnet{Id: &id1, Tags: tags1})
		id2 := "fake-id2"
		a = append(a, &model.VpcSubnet{Id: &id2, Tags: tags2})
		return a
	})
	patch.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetPortsOfSubnet", func(_ *subnetport.SubnetPortService, _ string) (ports []*model.VpcSubnetPort) {
		return nil
	})
	patch.ApplyMethod(reflect.TypeOf(r.SubnetService), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
		return nil
	})

	ctx := context.Background()
	srList := &v1alpha1.SubnetList{}
	k8sClient.EXPECT().List(ctx, srList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.SubnetList)
		a.Items = append(a.Items, v1alpha1.Subnet{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "fake-id2"
		return nil
	})

	r.collectGarbage(ctx)

	// local store has same item as k8s cache
	patch.Reset()
	patch = gomonkey.ApplyMethod(reflect.TypeOf(&common.ResourceStore{}), "ListIndexFuncValues", func(_ *common.ResourceStore, _ string) sets.Set[string] {
		res := sets.New[string]("fake-id2")
		return res
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
		assert.FailNow(t, "should not be called")
		return nil
	})
	k8sClient.EXPECT().List(gomock.Any(), srList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.SubnetList)
		a.Items = append(a.Items, v1alpha1.Subnet{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "fake-id2"
		return nil
	})
	r.collectGarbage(ctx)

	// local store has no item
	patch.Reset()
	patch = patch.ApplyMethod(reflect.TypeOf(&common.ResourceStore{}), "ListIndexFuncValues", func(_ *common.ResourceStore, _ string) sets.Set[string] {
		return nil
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
		assert.FailNow(t, "should not be called")
		return nil
	})
	k8sClient.EXPECT().List(ctx, srList).Return(nil).Times(1)
	r.collectGarbage(ctx)
	patch.Reset()
}

type fakeRecorder struct{}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
}

func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}

func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}

func createFakeSubnetReconciler(objs []client.Object) *SubnetReconciler {
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
			Client:    fakeClient,
			NSXClient: &nsx.Client{},
		},
		SubnetPortStore: nil,
	}

	return &SubnetReconciler{
		Client:            fakeClient,
		Scheme:            fake.NewClientBuilder().Build().Scheme(),
		VPCService:        service,
		SubnetService:     subnetService,
		SubnetPortService: subnetPortService,
		Recorder:          &fakeRecorder{},
	}
}

func TestSubnetReconciler_Reconcile(t *testing.T) {
	subnetName := "test-subnet"
	subnetID := "fake-subnet-uid"
	createNewSubnet := func(specs ...bool) *v1alpha1.Subnet {
		subnetCR := &v1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      subnetName,
				Namespace: "default",
				UID:       types.UID(subnetID),
			},
			Spec: v1alpha1.SubnetSpec{
				IPv4SubnetSize: 0,
				AccessMode:     "",
			},
		}
		if len(specs) > 0 && specs[0] {
			subnetCR.Finalizers = []string{"test-Finalizers"}
			subnetCR.DeletionTimestamp = &metav1.Time{Time: time.Now()}
		}
		return subnetCR
	}

	testCases := []struct {
		name             string
		req              ctrl.Request
		expectRes        ctrl.Result
		expectErrStr     string
		patches          func(r *SubnetReconciler) *gomonkey.Patches
		existingSubnetCR *v1alpha1.Subnet
		expectSubnetCR   *v1alpha1.Subnet
	}{
		{
			name: "Subnet CR not found",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				return gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "deleteSubnetByName", func(_ *SubnetReconciler, name, ns string) error {
					return nil
				})
			},
			expectRes:        ResultNormal,
			existingSubnetCR: nil,
		},
		{
			name: "Delete Subnet CR success",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "deleteSubnetByID", func(_ *SubnetReconciler, id string) error {
					return nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
					id1 := "fake-id"
					path := "fake-path"
					tags := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-subnet-uid-2")},
						{Scope: common.String(common.TagScopeSubnetCRName), Tag: common.String(subnetName)},
					}
					vpcSubnetSkip := model.VpcSubnet{Id: &id1, Path: &path, Tags: tags}

					id2 := "fake-id-1"
					path2 := "fake-path-2"
					tagStale := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-subnet-uid-stale")},
						{Scope: common.String(common.TagScopeSubnetCRName), Tag: common.String(subnetName)},
					}
					vpcSubnetDelete := model.VpcSubnet{Id: &id2, Path: &path2, Tags: tagStale}
					return []*model.VpcSubnet{
						&vpcSubnetSkip, &vpcSubnetDelete,
					}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetPortsOfSubnet", func(_ *subnetport.SubnetPortService, _ string) (ports []*model.VpcSubnetPort) {
					return nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
					return nil
				})
				return patches
			},
			expectRes: ResultNormal,
		},
		{
			name: "Delete Subnet CR failed to delete NSX Subnet",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
					id1 := "fake-id"
					path := "fake-path"
					tags := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-subnet-uid-2")},
						{Scope: common.String(common.TagScopeSubnetCRName), Tag: common.String(subnetName)},
					}
					vpcSubnetSkip := model.VpcSubnet{Id: &id1, Path: &path, Tags: tags}

					id2 := "fake-id-1"
					path2 := "fake-path-2"
					tagStale := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-subnet-uid-stale")},
						{Scope: common.String(common.TagScopeSubnetCRName), Tag: common.String(subnetName)},
					}
					vpcSubnetDelete := model.VpcSubnet{Id: &id2, Path: &path2, Tags: tagStale}
					return []*model.VpcSubnet{
						&vpcSubnetSkip, &vpcSubnetDelete,
					}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetPortsOfSubnet", func(_ *subnetport.SubnetPortService, _ string) (ports []*model.VpcSubnetPort) {
					return nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
					return errors.New("failed to delete NSX Subnet")
				})
				return patches
			},
			expectRes:    ResultRequeue,
			expectErrStr: "failed to delete NSX Subnet",
		},
		{
			name: "Get Subnet CR return other error should retry",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Client), "Get", func(_ client.Client, _ context.Context, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
					return errors.New("get Subnet CR error")
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "deleteSubnetByName", func(_ *SubnetReconciler, name, ns string) error {
					return nil
				})
				return patches
			},
			expectErrStr:     "get Subnet CR error",
			expectRes:        ResultRequeue,
			existingSubnetCR: nil,
		},
		{
			name: "Subnet CR with finalizer delete success",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "deleteSubnetByID", func(_ *SubnetReconciler, _ string) error {
					return nil
				})
				return patches
			},
			expectRes:        ResultNormal,
			existingSubnetCR: createNewSubnet(true),
		},
		{
			name: "Subnet CR with finalizer",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
					id1 := "fake-id"
					path := "fake-path"
					tags := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-subnet-uid-2")},
						{Scope: common.String(common.TagScopeSubnetCRName), Tag: common.String(subnetName)},
					}
					vpcSubnetSkip := model.VpcSubnet{Id: &id1, Path: &path, Tags: tags}

					id2 := "fake-id-1"
					path2 := "fake-path-2"
					tagStale := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String(subnetID)},
						{Scope: common.String(common.TagScopeSubnetCRName), Tag: common.String(subnetName)},
					}
					vpcSubnetDelete := model.VpcSubnet{Id: &id2, Path: &path2, Tags: tagStale}
					return []*model.VpcSubnet{
						&vpcSubnetSkip, &vpcSubnetDelete,
					}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetPortsOfSubnet", func(_ *subnetport.SubnetPortService, _ string) (ports []*model.VpcSubnetPort) {
					return nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
					return nil
				})
				return patches
			},
			expectRes:        ResultNormal,
			existingSubnetCR: createNewSubnet(true),
		},
		{
			name: "Subnet CR with finalizer delete failed",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
					id1 := "fake-id"
					path := "fake-path"
					tags := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-subnet-uid-2")},
						{Scope: common.String(common.TagScopeSubnetCRName), Tag: common.String(subnetName)},
					}
					vpcSubnetSkip := model.VpcSubnet{Id: &id1, Path: &path, Tags: tags}

					id2 := "fake-id-1"
					path2 := "fake-path-2"
					tagStale := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String(subnetID)},
						{Scope: common.String(common.TagScopeSubnetCRName), Tag: common.String(subnetName)},
					}
					vpcSubnetDelete := model.VpcSubnet{Id: &id2, Path: &path2, Tags: tagStale}
					return []*model.VpcSubnet{
						&vpcSubnetSkip, &vpcSubnetDelete,
					}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetPortsOfSubnet", func(_ *subnetport.SubnetPortService, _ string) (ports []*model.VpcSubnetPort) {
					return nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
					return errors.New("delete NSX Subnet failed")
				})
				return patches
			},
			expectRes:        ResultRequeue,
			expectErrStr:     "delete NSX Subnet failed",
			existingSubnetCR: createNewSubnet(true),
		},
		{
			name: "Create or Update Subnet Failure",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				vpcConfig := &common.VPCNetworkConfigInfo{DefaultSubnetSize: 16}
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) *common.VPCNetworkConfigInfo {
					return vpcConfig
				})

				tags := []model.Tag{{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-tag")}}
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GenerateSubnetNSTags", func(_ *subnet.SubnetService, obj client.Object) []model.Tag {
					return tags
				})

				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, ns string) []common.VPCResourceInfo {
					return []common.VPCResourceInfo{
						{OrgID: "org-id", ProjectID: "project-id", VPCID: "vpc-id", ID: "fake-id"},
					}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "CreateOrUpdateSubnet", func(_ *subnet.SubnetService, obj client.Object, vpcInfo common.VPCResourceInfo, tags []model.Tag) (string, error) {
					return "", errors.New("create or update failed")
				})
				return patches
			},
			existingSubnetCR: createNewSubnet(),
			expectErrStr:     "create or update failed",
			expectRes:        ResultRequeue,
		},
		{
			name: "Update Subnet CR spec success",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				vpcConfig := &common.VPCNetworkConfigInfo{DefaultSubnetSize: 16}
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) *common.VPCNetworkConfigInfo {
					return vpcConfig
				})

				tags := []model.Tag{{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-tag")}}
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GenerateSubnetNSTags", func(_ *subnet.SubnetService, obj client.Object) []model.Tag {
					return tags
				})

				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, ns string) []common.VPCResourceInfo {
					return []common.VPCResourceInfo{
						{OrgID: "org-id", ProjectID: "project-id", VPCID: "vpc-id", ID: "fake-id"},
					}
				})

				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "CreateOrUpdateSubnet", func(_ *subnet.SubnetService, obj client.Object, vpcInfo common.VPCResourceInfo, tags []model.Tag) (string, error) {
					return "", nil
				})

				patches.ApplyPrivateMethod(reflect.TypeOf(r), "updateSubnetStatus", func(_ *SubnetReconciler, obj *v1alpha1.Subnet) error {
					return nil
				})
				return patches
			},
			existingSubnetCR: createNewSubnet(),
			expectSubnetCR: &v1alpha1.Subnet{
				Spec:   v1alpha1.SubnetSpec{IPv4SubnetSize: 16, AccessMode: "Private", IPAddresses: []string(nil), DHCPConfig: v1alpha1.DHCPConfig{EnableDHCP: false}},
				Status: v1alpha1.SubnetStatus{},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			objs := []client.Object{}
			if testCase.existingSubnetCR != nil {
				objs = append(objs, testCase.existingSubnetCR)
			}
			reconciler := createFakeSubnetReconciler(objs)
			ctx := context.Background()

			v1alpha1.AddToScheme(reconciler.Scheme)
			patches := testCase.patches(reconciler)
			defer patches.Reset()

			result, err := reconciler.Reconcile(ctx, testCase.req)
			if testCase.expectErrStr != "" {
				assert.ErrorContains(t, err, testCase.expectErrStr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, testCase.expectRes, result)

			if testCase.expectSubnetCR != nil {
				actualSubnetCR := &v1alpha1.Subnet{}
				assert.NoError(t, reconciler.Client.Get(ctx, testCase.req.NamespacedName, actualSubnetCR))
				assert.Equal(t, testCase.expectSubnetCR.Spec, actualSubnetCR.Spec)
			}
		})
	}
}
