package subnet

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	common2 "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetbinding"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

func TestSubnetReconciler_GarbageCollector(t *testing.T) {
	testCases := []struct {
		name             string
		patches          func(r *SubnetReconciler) *gomonkey.Patches
		existingSubnetCR *v1alpha1.Subnet
	}{
		{
			name: "Delete stale NSX Subnets success",
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				patch := gomonkey.ApplyMethod(reflect.TypeOf(&common.ResourceStore{}), "ListIndexFuncValues", func(_ *common.ResourceStore, _ string) sets.Set[string] {
					res := sets.New[string]("fake-id1", "fake-id2")
					return res
				})
				patch.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, _ string) []*model.VpcSubnet {
					tags1 := []model.Tag{{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-id1")}}
					tags2 := []model.Tag{{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-id2")}}
					var nsxSubnets []*model.VpcSubnet
					id1 := "fake-id1"
					nsxSubnets = append(nsxSubnets, &model.VpcSubnet{Id: &id1, Tags: tags1})
					id2 := "fake-id2"
					nsxSubnets = append(nsxSubnets, &model.VpcSubnet{Id: &id2, Tags: tags2})
					return nsxSubnets
				})
				patch.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetPortsOfSubnet", func(_ *subnetport.SubnetPortService, _ string) (ports []*model.VpcSubnetPort) {
					return nil
				})
				patch.ApplyMethod(reflect.TypeOf(r.SubnetService), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
					return nil
				})
				return patch
			},
		},
		{
			name: "Should not delete NSX Subnet when the Subnet CR existes",
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				// local store has same item as k8s cache
				patch := gomonkey.ApplyMethod(reflect.TypeOf(&common.ResourceStore{}), "ListIndexFuncValues", func(_ *common.ResourceStore, _ string) sets.Set[string] {
					res := sets.New[string]("fake-id2")
					return res
				})
				patch.ApplyMethod(reflect.TypeOf(r.SubnetService), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
					assert.FailNow(t, "should not be called")
					return nil
				})
				return patch
			},
			existingSubnetCR: &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnetName",
					Namespace: "default",
					UID:       types.UID("fake-id2"),
				},
			},
		},
		{
			name: "Delete NSX Subnet error",
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				patch := gomonkey.ApplyMethod(reflect.TypeOf(&common.ResourceStore{}), "ListIndexFuncValues", func(_ *common.ResourceStore, _ string) sets.Set[string] {
					res := sets.New[string]("fake-id1", "fake-id2")
					return res
				})
				patch.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, _ string) []*model.VpcSubnet {
					tags1 := []model.Tag{{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-id1")}}
					tags2 := []model.Tag{{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-id2")}}
					var nsxSubnets []*model.VpcSubnet
					id1 := "fake-id1"
					nsxSubnets = append(nsxSubnets, &model.VpcSubnet{Id: &id1, Tags: tags1})
					id2 := "fake-id2"
					nsxSubnets = append(nsxSubnets, &model.VpcSubnet{Id: &id2, Tags: tags2})
					return nsxSubnets
				})
				patch.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetPortsOfSubnet", func(_ *subnetport.SubnetPortService, _ string) (ports []*model.VpcSubnetPort) {
					return nil
				})
				patch.ApplyMethod(reflect.TypeOf(r.SubnetService), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
					return errors.New("delete failed")
				})
				return patch
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			objs := []client.Object{}
			if testCase.existingSubnetCR != nil {
				objs = append(objs, testCase.existingSubnetCR)
			}
			r := createFakeSubnetReconciler(objs)
			ctx := context.Background()

			patches := testCase.patches(r)
			defer patches.Reset()

			r.collectGarbage(ctx)
		})
	}
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
				CoeConfig: &config.CoeConfig{Cluster: "fakeCluster"},
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
		StatusUpdater:     common2.NewStatusUpdater(fakeClient, subnetService.NSXConfig, &fakeRecorder{}, MetricResTypeSubnet, "Subnet", "Subnet"),
	}
}

func TestSubnetReconciler_Reconcile(t *testing.T) {
	subnetName := "test-subnet"
	subnetID := "fake-subnet-uid"
	ns := "default"
	subnetName1 := "test-subnet1"
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
				SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
					Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated),
				},
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
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: subnetName1}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "deleteSubnetByID", func(_ *SubnetReconciler, id string) error {
					return nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
					id1 := "fake-id"
					path := "fake-path"
					tags := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String(subnetID)},
						{Scope: common.String(common.TagScopeSubnetCRName), Tag: common.String(subnetName1)},
						{Scope: common.String(common.TagScopeVMNamespace), Tag: common.String(ns)},
					}
					vpcSubnetSkip := model.VpcSubnet{Id: &id1, Path: &path, Tags: tags}

					id2 := "fake-id-1"
					path2 := "fake-path-2"
					tagStale := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-subnet-uid-stale")},
						{Scope: common.String(common.TagScopeSubnetCRName), Tag: common.String(subnetName1)},
						{Scope: common.String(common.TagScopeVMNamespace), Tag: common.String(ns)},
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
			existingSubnetCR: createNewSubnet(),
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
			name: "Delete Subnet CR with stale SubnetPort",
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
					id := "fake-subnetport-0"
					return []*model.VpcSubnetPort{{Id: &id}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
					return nil
				})
				return patches
			},
			expectRes:    ResultRequeue,
			expectErrStr: "cannot delete Subnet fake-id, still attached by 1 port(s)",
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
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnet", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getNSXSubnetBindingsBySubnet", func(_ *SubnetReconciler, _ string) []*v1alpha1.SubnetConnectionBindingMap {
					return []*v1alpha1.SubnetConnectionBindingMap{}
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
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnet", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getNSXSubnetBindingsBySubnet", func(_ *SubnetReconciler, _ string) []*v1alpha1.SubnetConnectionBindingMap {
					return []*v1alpha1.SubnetConnectionBindingMap{}
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
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnet", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getNSXSubnetBindingsBySubnet", func(_ *SubnetReconciler, _ string) []*v1alpha1.SubnetConnectionBindingMap {
					return []*v1alpha1.SubnetConnectionBindingMap{}
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
			name: "Create or Update Subnet with create NSX Subnet failure",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				vpcConfig := &common.VPCNetworkConfigInfo{DefaultSubnetSize: 16}
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) *common.VPCNetworkConfigInfo {
					return vpcConfig
				})

				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnet", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
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

				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnet", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
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

				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetByKey", func(_ *subnet.SubnetService, key string) (*model.VpcSubnet, error) {
					id1 := "fake-id"
					path := "/orgs/default/projects/nsx_operator_e2e_test/vpcs/subnet-e2e_8f36f7fc-90cd-4e65-a816-daf3ecd6a0f9/subnets/subnet_fake-path"
					tags := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-subnet-uid-2")},
						{Scope: common.String(common.TagScopeSubnetCRName), Tag: common.String(subnetName)},
					}
					return &model.VpcSubnet{Id: &id1, Path: &path, Tags: tags}, nil
				})

				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetStatus", func(_ *subnet.SubnetService) ([]model.VpcSubnetStatus, error) {
					fakeStatus := model.VpcSubnetStatus{}
					value := ""
					fakeStatus.GatewayAddress = &value
					fakeStatus.DhcpServerAddress = &value
					fakeStatus.NetworkAddress = &value
					return []model.VpcSubnetStatus{fakeStatus}, nil
				})
				return patches
			},
			existingSubnetCR: createNewSubnet(),
			expectSubnetCR: &v1alpha1.Subnet{
				Spec:   v1alpha1.SubnetSpec{IPv4SubnetSize: 16, AccessMode: "Private", IPAddresses: []string(nil), SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated)}},
				Status: v1alpha1.SubnetStatus{},
			},
		},
		{
			name: "Update Subnet CR with update status error",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				vpcConfig := &common.VPCNetworkConfigInfo{DefaultSubnetSize: 16}
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) *common.VPCNetworkConfigInfo {
					return vpcConfig
				})

				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnet", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
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

				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetByKey", func(_ *subnet.SubnetService, key string) (*model.VpcSubnet, error) {
					return nil, fmt.Errorf("failed to get NSX Subnet from store")
				})
				return patches
			},
			existingSubnetCR: createNewSubnet(),
			expectSubnetCR: &v1alpha1.Subnet{
				Spec:   v1alpha1.SubnetSpec{IPv4SubnetSize: 16, AccessMode: "Private", IPAddresses: []string(nil), SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated)}},
				Status: v1alpha1.SubnetStatus{},
			},
			expectRes:    ResultRequeue,
			expectErrStr: "failed to get NSX Subnet from store",
		},
		{
			name: "Create or Update Subnet with VPCNetworkConfig not found failure",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) *common.VPCNetworkConfigInfo {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnet", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
				})
				return patches
			},
			existingSubnetCR: createNewSubnet(),
			expectErrStr:     "VPCNetworkConfig not found for Subnet CR",
			expectRes:        ResultRequeue,
		},
		{
			name: "Create or Update Subnet with generate Subnet tags failure",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				vpcConfig := &common.VPCNetworkConfigInfo{DefaultSubnetSize: 16}
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) *common.VPCNetworkConfigInfo {
					return vpcConfig
				})

				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnet", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
				})

				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GenerateSubnetNSTags", func(_ *subnet.SubnetService, obj client.Object) []model.Tag {
					return nil
				})
				return patches
			},
			existingSubnetCR: createNewSubnet(),
			expectErrStr:     "failed to generate Subnet tags",
			expectRes:        ResultRequeue,
		},
		{
			name: "Create or Update Subnet with No VPC info found failure",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				vpcConfig := &common.VPCNetworkConfigInfo{DefaultSubnetSize: 16}
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) *common.VPCNetworkConfigInfo {
					return vpcConfig
				})

				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnet", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
				})

				tags := []model.Tag{{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-tag")}}
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GenerateSubnetNSTags", func(_ *subnet.SubnetService, obj client.Object) []model.Tag {
					return tags
				})

				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, ns string) []common.VPCResourceInfo {
					return nil
				})
				return patches
			},
			existingSubnetCR: createNewSubnet(),
			expectRes:        ResultRequeueAfter10sec,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var objs []client.Object
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

type MockManager struct {
	ctrl.Manager
	client client.Client
	scheme *runtime.Scheme
}

func (m *MockManager) GetClient() client.Client {
	return m.client
}

func (m *MockManager) GetScheme() *runtime.Scheme {
	return m.scheme
}

func (m *MockManager) GetEventRecorderFor(name string) record.EventRecorder {
	return nil
}

func (m *MockManager) Add(runnable manager.Runnable) error {
	return nil
}

func (m *MockManager) Start(context.Context) error {
	return nil
}

func TestStartSubnetController(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects().Build()
	vpcService := &vpc.VPCService{
		Service: common.Service{
			Client: fakeClient,
		},
	}
	subnetService := &subnet.SubnetService{
		Service: common.Service{
			Client: fakeClient,
		},
		SubnetStore: &subnet.SubnetStore{},
	}
	subnetPortService := &subnetport.SubnetPortService{
		Service:         common.Service{},
		SubnetPortStore: nil,
	}
	bindingService := &subnetbinding.BindingService{
		Service:      common.Service{},
		BindingStore: subnetbinding.SetupStore(),
	}

	mockMgr := &MockManager{scheme: runtime.NewScheme()}

	testCases := []struct {
		name         string
		expectErrStr string
		patches      func() *gomonkey.Patches
	}{
		// expected no error when starting the SubnetSet controller with webhook
		{
			name: "StartSubnetController with webhook",
			patches: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(common2.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context)) {
					return
				})
				patches.ApplyMethod(reflect.TypeOf(&ctrl.Builder{}), "Complete", func(_ *ctrl.Builder, r reconcile.Reconciler) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(&SubnetReconciler{}), "setupWithManager", func(_ *SubnetReconciler, mgr ctrl.Manager) error {
					return nil
				})
				return patches
			},
		},
		{
			name:         "StartSubnetController return error",
			expectErrStr: "failed to setupWithManager",
			patches: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(common2.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context)) {
					return
				})
				patches.ApplyMethod(reflect.TypeOf(&ctrl.Builder{}), "Complete", func(_ *ctrl.Builder, r reconcile.Reconciler) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(&SubnetReconciler{}), "setupWithManager", func(_ *SubnetReconciler, mgr ctrl.Manager) error {
					return errors.New("failed to setupWithManager")
				})
				return patches
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			patches := testCase.patches()
			defer patches.Reset()

			err := StartSubnetController(mockMgr, subnetService, subnetPortService, vpcService, bindingService, nil)

			if testCase.expectErrStr != "" {
				assert.ErrorContains(t, err, testCase.expectErrStr)
			} else {
				assert.NoError(t, err, "expected no error when starting the SubnetSet controller")
			}
		})
	}
}
