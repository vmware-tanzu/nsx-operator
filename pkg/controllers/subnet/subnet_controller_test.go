package subnet

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	mockClient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
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
					nsxSubnets = append(nsxSubnets, &model.VpcSubnet{Id: &id1, Tags: tags1, Path: common.String("fake-path")})
					id2 := "fake-id2"
					nsxSubnets = append(nsxSubnets, &model.VpcSubnet{Id: &id2, Tags: tags2, Path: common.String("fake-path")})
					return nsxSubnets
				})
				patch.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetPortsOfSubnet", func(_ *subnetport.SubnetPortService, _ string) (ports []*model.VpcSubnetPort) {
					return nil
				})
				patch.ApplyMethod(reflect.TypeOf(r.SubnetService), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
					return nil
				})
				patch.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "DeletePortCount", func(_ *subnetport.SubnetPortService, _ string) {
					return
				})
				return patch
			},
		},
		{
			name: "Should not delete NSX Subnet when the Subnet CR exists",
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
			objects := []client.Object{}
			if testCase.existingSubnetCR != nil {
				objects = append(objects, testCase.existingSubnetCR)
			}
			r := createFakeSubnetReconciler(objects)
			ctx := context.Background()

			patches := testCase.patches(r)
			defer patches.Reset()

			_ = r.CollectGarbage(ctx)
		})
	}
}

type fakeRecorder struct{}

func (recorder fakeRecorder) Event(_ runtime.Object, _, _, _ string) {
}

func (recorder fakeRecorder) Eventf(_ runtime.Object, _, _, _ string, _ ...interface{}) {
}

func (recorder fakeRecorder) AnnotatedEventf(_ runtime.Object, _ map[string]string, _, _, _ string, _ ...interface{}) {
}

func createFakeSubnetReconciler(objects []client.Object) *SubnetReconciler {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(objects...).Build()
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
		SharedSubnetData: subnet.SharedSubnetData{
			NSXSubnetCache: make(map[string]struct {
				Subnet     *model.VpcSubnet
				StatusList []model.VpcSubnetStatus
			}),
			SharedSubnetResourceMap: make(map[string]sets.Set[types.NamespacedName]),
		},
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
				patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "DeletePortCount", func(_ *subnetport.SubnetPortService, _ string) {
					return
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
				patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "DeletePortCount", func(_ *subnetport.SubnetPortService, _ string) {
					return
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
				patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "DeletePortCount", func(_ *subnetport.SubnetPortService, _ string) {
					return
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
				vpcnetworkConfig := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DefaultSubnetSize: 16}}
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return vpcnetworkConfig, nil
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
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "CreateOrUpdateSubnet", func(_ *subnet.SubnetService, obj client.Object, vpcInfo common.VPCResourceInfo, tags []model.Tag) (*model.VpcSubnet, error) {
					return nil, errors.New("create or update failed")
				})
				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "IsDefaultNSXProject", func(_ *vpc.VPCService, orgID, projectID string) (bool, error) {
					return false, nil
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
				vpcnetworkConfig := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DefaultSubnetSize: 16}}
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return vpcnetworkConfig, nil
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
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "CreateOrUpdateSubnet", func(_ *subnet.SubnetService, obj client.Object, vpcInfo common.VPCResourceInfo, tags []model.Tag) (*model.VpcSubnet, error) {
					return nil, nil
				})

				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "IsDefaultNSXProject", func(_ *vpc.VPCService, orgID, projectID string) (bool, error) {
					return false, nil
				})

				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetsByIndex", func(_ *subnet.SubnetService, key string, value string) []*model.VpcSubnet {
					id1 := "fake-id"
					path := "/orgs/default/projects/nsx_operator_e2e_test/vpcs/subnet-e2e_8f36f7fc-90cd-4e65-a816-daf3ecd6a0f9/subnets/subnet_fake-path"
					tags := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-subnet-uid-2")},
						{Scope: common.String(common.TagScopeSubnetCRName), Tag: common.String(subnetName)},
					}
					return []*model.VpcSubnet{{Id: &id1, Path: &path, Tags: tags}}
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
				Spec: v1alpha1.SubnetSpec{VPCName: "project-id:vpc-id", IPv4SubnetSize: 16, AccessMode: "Private",
					IPAddresses:      []string(nil),
					SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated)}},
				Status: v1alpha1.SubnetStatus{},
			},
		},
		{
			name: "Update Subnet CR with update status error",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				vpcnetworkConfig := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DefaultSubnetSize: 16}}
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return vpcnetworkConfig, nil
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

				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "CreateOrUpdateSubnet", func(_ *subnet.SubnetService, obj client.Object, vpcInfo common.VPCResourceInfo, tags []model.Tag) (*model.VpcSubnet, error) {
					return nil, nil
				})

				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetsByIndex", func(_ *subnet.SubnetService, key string, value string) []*model.VpcSubnet {
					return nil
				})

				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "IsDefaultNSXProject", func(_ *vpc.VPCService, orgID, projectID string) (bool, error) {
					return false, nil
				})
				return patches
			},
			existingSubnetCR: createNewSubnet(),
			expectSubnetCR: &v1alpha1.Subnet{
				Spec: v1alpha1.SubnetSpec{VPCName: "project-id:vpc-id", IPv4SubnetSize: 16, AccessMode: "Private", IPAddresses: []string(nil),
					SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated)}},
				Status: v1alpha1.SubnetStatus{},
			},
			expectRes:    ResultRequeue,
			expectErrStr: "failed to get NSX Subnet from store",
		},
		{
			name: "Create or Update Subnet with VPCNetworkConfig not found failure",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return nil, nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnet", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
				})
				return patches
			},
			existingSubnetCR: createNewSubnet(),
			expectRes:        ResultRequeueAfter10sec,
		},
		{
			name: "Create or Update Subnet with generate Subnet tags failure",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				vpcnetworkConfig := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DefaultSubnetSize: 16}}
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return vpcnetworkConfig, nil
				})

				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnet", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
				})

				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, ns string) []common.VPCResourceInfo {
					return []common.VPCResourceInfo{
						{OrgID: "org-id", ProjectID: "project-id", VPCID: "vpc-id", ID: "fake-id"},
					}
				})

				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "IsDefaultNSXProject", func(_ *vpc.VPCService, orgID, projectID string) (bool, error) {
					return false, nil
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
				vpcnetworkConfig := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DefaultSubnetSize: 16}}
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return vpcnetworkConfig, nil
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
			var objects []client.Object
			if testCase.existingSubnetCR != nil {
				objects = append(objects, testCase.existingSubnetCR)
			}
			reconciler := createFakeSubnetReconciler(objects)
			ctx := context.Background()

			err := v1alpha1.AddToScheme(reconciler.Scheme)
			assert.NoError(t, err, "failed to add v1alpha1 scheme")
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

type MockFieldIndexer struct{}

func (m *MockFieldIndexer) IndexField(_ context.Context, _ client.Object, _ string, _ client.IndexerFunc) error {
	return nil
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

func (m *MockManager) GetEventRecorderFor(_ string) record.EventRecorder {
	return nil
}

func (m *MockManager) GetFieldIndexer() client.FieldIndexer {
	return &MockFieldIndexer{}
}

func (m *MockManager) Add(_ manager.Runnable) error {
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
				patches := gomonkey.ApplyFunc(common2.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context) error) {
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
				patches := gomonkey.ApplyFunc(common2.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context) error) {
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

			reconciler := NewSubnetReconciler(mockMgr, subnetService, subnetPortService, vpcService, bindingService)
			err := reconciler.StartController(mockMgr, nil)

			if testCase.expectErrStr != "" {
				assert.ErrorContains(t, err, testCase.expectErrStr)
			} else {
				assert.NoError(t, err, "expected no error when starting the SubnetSet controller")
			}
		})
	}
}

func TestReconcileWithSubnetConnectionBindingMaps(t *testing.T) {
	subnetName := "subnet1"
	ns := "ns1"
	testSubnet1 := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: subnetName, Namespace: ns},
		Spec: v1alpha1.SubnetSpec{
			AccessMode:     v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
			IPv4SubnetSize: 16,
			VPCName:        "project:test-vpc",
		},
	}
	testSubnet2 := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: subnetName, Namespace: ns, Finalizers: []string{common.SubnetFinalizerName}},
		Spec: v1alpha1.SubnetSpec{
			AccessMode:     v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
			IPv4SubnetSize: 16,
			VPCName:        "project:test-vpc",
		},
	}
	deletionTime := metav1.Now()
	testSubnet3 := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:              subnetName,
			Namespace:         ns,
			Finalizers:        []string{common.SubnetFinalizerName},
			DeletionTimestamp: &deletionTime,
		},
		Spec: v1alpha1.SubnetSpec{
			AccessMode:     v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
			IPv4SubnetSize: 16,
			VPCName:        "project:test-vpc",
		},
	}
	for _, tc := range []struct {
		name           string
		existingSubnet *v1alpha1.Subnet
		patches        func(t *testing.T, r *SubnetReconciler) *gomonkey.Patches
		expectErrStr   string
		expectRes      ctrl.Result
	}{
		{
			name:           "Successfully add finalizer after a Subnet is used by SubnetConnectionBindingMap",
			existingSubnet: testSubnet1,
			patches: func(t *testing.T, r *SubnetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnet", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{{ObjectMeta: metav1.ObjectMeta{Name: "binding1", Namespace: ns}}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.Client), "Update", func(_ client.Client, _ context.Context, obj client.Object, opts ...client.UpdateOption) error {
					return nil
				})
				return patchSuccessfulReconcileSubnetWorkflow(r, patches)
			},
			expectRes: ctrl.Result{},
		}, {
			name:           "Failed to add finalizer after a Subnet is used by SubnetConnectionBindingMap",
			existingSubnet: testSubnet1,
			patches: func(t *testing.T, r *SubnetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnet", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{{ObjectMeta: metav1.ObjectMeta{Name: "binding1", Namespace: ns}}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.Client), "Update", func(_ client.Client, _ context.Context, obj client.Object, opts ...client.UpdateOption) error {
					return fmt.Errorf("failed to update CR")
				})
				patches.ApplyFunc(updateSubnetStatusConditions, func(_ client.Client, _ context.Context, _ *v1alpha1.Subnet, newConditions []v1alpha1.Condition) {
					require.Equal(t, 1, len(newConditions))
					cond := newConditions[0]
					assert.Equal(t, "Failed to add the finalizer on a Subnet for the reference by SubnetConnectionBindingMap binding1", cond.Message)
				})
				return patches
			},
			expectErrStr: "failed to update CR",
			expectRes:    common2.ResultRequeue,
		}, {
			name:           "Not add duplicated finalizer after a Subnet is used by SubnetConnectionBindingMap",
			existingSubnet: testSubnet2,
			patches: func(t *testing.T, r *SubnetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnet", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{{ObjectMeta: metav1.ObjectMeta{Name: "binding1", Namespace: ns}}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.Client), "Update", func(_ client.Client, _ context.Context, obj client.Object, opts ...client.UpdateOption) error {
					assert.FailNow(t, "Should not update Subnet CR finalizer")
					return nil
				})
				return patchSuccessfulReconcileSubnetWorkflow(r, patches)
			},
			expectRes: ctrl.Result{},
		}, {
			name:           "Successfully remove finalizer after a Subnet is not used by any SubnetConnectionBindingMaps",
			existingSubnet: testSubnet2,
			patches: func(t *testing.T, r *SubnetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnet", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
				})
				patches.ApplyMethod(reflect.TypeOf(r.Client), "Update", func(_ client.Client, _ context.Context, obj client.Object, opts ...client.UpdateOption) error {
					return nil
				})
				return patchSuccessfulReconcileSubnetWorkflow(r, patches)
			},
			expectRes: ctrl.Result{},
		}, {
			name:           "Failed to remove finalizer after a Subnet is not used by any SubnetConnectionBindingMaps",
			existingSubnet: testSubnet2,
			patches: func(t *testing.T, r *SubnetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnet", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
				})
				patches.ApplyMethod(reflect.TypeOf(r.Client), "Update", func(_ client.Client, _ context.Context, obj client.Object, opts ...client.UpdateOption) error {
					return fmt.Errorf("failed to update CR")
				})
				patches.ApplyFunc(updateSubnetStatusConditions, func(_ client.Client, _ context.Context, _ *v1alpha1.Subnet, newConditions []v1alpha1.Condition) {
					require.Equal(t, 1, len(newConditions))
					cond := newConditions[0]
					assert.Equal(t, "Failed to remove the finalizer on a Subnet when there is no reference by SubnetConnectionBindingMaps", cond.Message)
				})
				return patches
			},
			expectErrStr: "failed to update CR",
			expectRes:    common2.ResultRequeue,
		}, {
			name:           "Not update finalizers if a Subnet is not used by any SubnetConnectionBindingMaps",
			existingSubnet: testSubnet1,
			patches: func(t *testing.T, r *SubnetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnet", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
				})
				patches.ApplyMethod(reflect.TypeOf(r.Client), "Update", func(_ client.Client, _ context.Context, obj client.Object, opts ...client.UpdateOption) error {
					assert.FailNow(t, "Should not update Subnet CR finalizer")
					return nil
				})
				return patchSuccessfulReconcileSubnetWorkflow(r, patches)
			},
			expectRes: ctrl.Result{},
		}, {
			name:           "Delete a Subnet is not allowed if it is used by SubnetConnectionBindingMap",
			existingSubnet: testSubnet3,
			patches: func(t *testing.T, r *SubnetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnet", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getNSXSubnetBindingsBySubnet", func(_ *SubnetReconciler, _ string) []*v1alpha1.SubnetConnectionBindingMap {
					binding := &v1alpha1.SubnetConnectionBindingMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "binding1",
							Namespace: ns,
						},
						Spec: v1alpha1.SubnetConnectionBindingMapSpec{
							SubnetName: "subnet1",
						},
					}
					return []*v1alpha1.SubnetConnectionBindingMap{binding}
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "setSubnetDeletionFailedStatus", func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet, _ metav1.Time, msg string, reason string) {
					// Skip assertions for now
				})
				return patches
			},
			expectErrStr: "failed to delete Subnet CR",
			expectRes:    ResultRequeue,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: subnetName, Namespace: ns}}
			r := createFakeSubnetReconciler([]client.Object{tc.existingSubnet})
			if tc.patches != nil {
				patches := tc.patches(t, r)
				defer patches.Reset()
			}

			res, err := r.Reconcile(ctx, req)

			if tc.expectErrStr != "" {
				assert.NotNil(t, err, "Expected an error but got nil")
				if err != nil {
					assert.Contains(t, err.Error(), tc.expectErrStr, "Error message does not contain expected string")
				}
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.expectRes, res)
		})
	}
}

func patchSuccessfulReconcileSubnetWorkflow(r *SubnetReconciler, patches *gomonkey.Patches) *gomonkey.Patches {
	patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GenerateSubnetNSTags", func(_ *subnet.SubnetService, _ client.Object) []model.Tag {
		return []model.Tag{{Scope: common.String("test"), Tag: common.String("subnet")}}
	})
	patches.ApplyMethod(reflect.TypeOf(r.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, _ string) []common.VPCResourceInfo {
		return []common.VPCResourceInfo{{ID: "vpc1"}}
	})
	patches.ApplyMethod(reflect.TypeOf(r.VPCService), "IsDefaultNSXProject", func(_ *vpc.VPCService, orgID, projectID string) (bool, error) {
		return false, nil
	})
	patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "CreateOrUpdateSubnet", func(_ *subnet.SubnetService, _ client.Object, _ common.VPCResourceInfo, _ []model.Tag) (subnet *model.VpcSubnet, err error) {
		return &model.VpcSubnet{
			Path: common.String("subnet-path"),
		}, nil
	})
	patches.ApplyPrivateMethod(reflect.TypeOf(r), "updateSubnetStatus", func(_ *SubnetReconciler, _ *v1alpha1.Subnet) error {
		return nil
	})
	patches.ApplyFunc(setSubnetReadyStatusTrue, func(_ client.Client, _ context.Context, _ client.Object, _ metav1.Time, _ ...interface{}) {
	})
	return patches
}

func TestSubnetReconciler_RestoreReconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	r := &SubnetReconciler{
		Client: k8sClient,
	}

	// Reconcile success
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		subnetList := list.(*v1alpha1.SubnetList)
		subnetList.Items = []v1alpha1.Subnet{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnet-1",
					Namespace: "ns-1",
					UID:       "subnet-1",
				},
				Status: v1alpha1.SubnetStatus{
					NetworkAddresses: []string{"10.0.0.0/28"},
					GatewayAddresses: []string{"10.0.0.0"},
				},
			},
		}
		return nil
	})

	patches := gomonkey.ApplyFunc((*SubnetReconciler).Reconcile, func(r *SubnetReconciler, ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
		assert.Equal(t, "subnet-1", req.Name)
		assert.Equal(t, "ns-1", req.Namespace)
		return ResultNormal, nil
	})
	defer patches.Reset()
	err := r.RestoreReconcile()
	assert.Nil(t, err)

	// Reconcile failure
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		subnetList := list.(*v1alpha1.SubnetList)
		subnetList.Items = []v1alpha1.Subnet{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnet-1",
					Namespace: "ns-1",
					UID:       "subnet-1",
				},
				Status: v1alpha1.SubnetStatus{
					NetworkAddresses: []string{"10.0.0.0/28"},
					GatewayAddresses: []string{"10.0.0.0"},
				},
			},
		}
		return nil
	})
	patches = gomonkey.ApplyFunc((*SubnetReconciler).Reconcile, func(r *SubnetReconciler, ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
		assert.Equal(t, "subnet-1", req.Name)
		assert.Equal(t, "ns-1", req.Namespace)
		return ResultRequeue, nil
	})
	defer patches.Reset()
	err = r.RestoreReconcile()
	assert.ErrorContains(t, err, "failed to restore Subnet ns-1/subnet-1")
}

func TestHandleSharedSubnet(t *testing.T) {
	// Test cases
	tests := []struct {
		name                string
		associatedResource  string
		nsxSubnet           *model.VpcSubnet
		nsxSubnetErr        error
		getStatusErr        error
		updateStatusErr     error
		expectedResult      ctrl.Result
		expectedErrContains string
	}{
		{
			name:               "Success case",
			associatedResource: "project1:vpc1:subnet1",
			nsxSubnet: &model.VpcSubnet{
				Id:   common.String("subnet-id"),
				Path: common.String("/projects/project1/vpcs/vpc1/subnets/subnet1"),
			},
			nsxSubnetErr:    nil,
			getStatusErr:    nil,
			updateStatusErr: nil,
			expectedResult:  ctrl.Result{},
		},
		{
			name:                "Error getting NSX subnet",
			associatedResource:  "project1:vpc1:subnet1",
			nsxSubnet:           nil,
			nsxSubnetErr:        fmt.Errorf("failed to get NSX subnet"),
			getStatusErr:        nil,
			updateStatusErr:     nil,
			expectedResult:      ResultRequeue,
			expectedErrContains: "failed to get NSX subnet",
		},
		{
			name:                "Error getting subnet status",
			associatedResource:  "project1:vpc1:subnet1",
			nsxSubnet:           &model.VpcSubnet{},
			nsxSubnetErr:        nil,
			getStatusErr:        fmt.Errorf("failed to get subnet status"),
			updateStatusErr:     nil,
			expectedResult:      ResultRequeue,
			expectedErrContains: "failed to get subnet status",
		},
		{
			name:                "NSX subnet not found",
			associatedResource:  "project1:vpc1:subnet1",
			nsxSubnet:           &model.VpcSubnet{},
			nsxSubnetErr:        nil,
			getStatusErr:        nil,
			updateStatusErr:     nil,
			expectedResult:      ctrl.Result{},
			expectedErrContains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fake reconciler
			r := createFakeSubnetReconciler(nil)

			// Create a test subnet CR
			subnetCR := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-subnet",
					Namespace: "default",
					UID:       types.UID("test-uid"),
					Annotations: map[string]string{
						common.AnnotationAssociatedResource: tt.associatedResource,
					},
				},
				Spec: v1alpha1.SubnetSpec{},
			}

			// Mock the GetNSXSubnetByAssociatedResource function
			patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetNSXSubnetByAssociatedResource", func(_ *subnet.SubnetService, associatedResource string) (*model.VpcSubnet, error) {
				assert.Equal(t, tt.associatedResource, associatedResource)
				return tt.nsxSubnet, tt.nsxSubnetErr
			})

			// Mock the GetSubnetStatus function
			if tt.nsxSubnet != nil && tt.nsxSubnetErr == nil {
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetStatus",
					func(_ *subnet.SubnetService, nsxSubnet *model.VpcSubnet) ([]model.VpcSubnetStatus, error) {
						assert.Equal(t, tt.nsxSubnet, nsxSubnet)
						if tt.getStatusErr != nil {
							return nil, tt.getStatusErr
						}
						statusList := []model.VpcSubnetStatus{
							{
								NetworkAddress:    common.String("10.0.0.0/24"),
								GatewayAddress:    common.String("10.0.0.1"),
								DhcpServerAddress: common.String("10.0.0.2"),
							},
						}
						return statusList, nil
					})
			}

			// Mock the MapNSXSubnetToSubnetCR function
			if tt.nsxSubnet != nil && tt.nsxSubnetErr == nil && tt.getStatusErr == nil {
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "MapNSXSubnetToSubnetCR",
					func(_ *subnet.SubnetService, subnetCR *v1alpha1.Subnet, _ *model.VpcSubnet) {
						subnetCR.Spec.AccessMode = v1alpha1.AccessMode(v1alpha1.AccessModePublic)
						subnetCR.Spec.IPv4SubnetSize = 24
						subnetCR.Spec.IPAddresses = []string{"192.168.1.0/24"}
						subnetCR.Spec.SubnetDHCPConfig.Mode = v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeServer)
					})
			}

			// Mock the MapNSXSubnetStatusToSubnetCRStatus function
			if tt.nsxSubnet != nil && tt.nsxSubnetErr == nil && tt.getStatusErr == nil {
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "MapNSXSubnetStatusToSubnetCRStatus",
					func(_ *subnet.SubnetService, subnetCR *v1alpha1.Subnet, statusList []model.VpcSubnetStatus) {
						// Verify the status list is passed correctly
						assert.Equal(t, 1, len(statusList))
						assert.Equal(t, "10.0.0.0/24", *statusList[0].NetworkAddress)
						assert.Equal(t, "10.0.0.1", *statusList[0].GatewayAddress)
						assert.Equal(t, "10.0.0.2", *statusList[0].DhcpServerAddress)

						// Set the status fields
						subnetCR.Status.NetworkAddresses = []string{"10.0.0.0/24"}
						subnetCR.Status.GatewayAddresses = []string{"10.0.0.1"}
						subnetCR.Status.DHCPServerAddresses = []string{"10.0.0.2"}
						subnetCR.Status.Shared = true
					})
			}

			// Mock the updateSubnetIfNeeded function
			patches.ApplyPrivateMethod(reflect.TypeOf(r), "updateSubnetIfNeeded",
				func(_ *SubnetReconciler, ctx context.Context, subnetCR *v1alpha1.Subnet, nsxSubnet *model.VpcSubnet, statusList []model.VpcSubnetStatus, namespacedName types.NamespacedName) error {
					return tt.updateStatusErr
				})

			// Call the function being tested
			namespacedName := client.ObjectKey{Namespace: "default", Name: "test-subnet"}
			result, err := r.handleSharedSubnet(context.Background(), subnetCR, namespacedName, tt.associatedResource)

			// Check the result
			assert.Equal(t, tt.expectedResult, result)
			if tt.expectedErrContains != "" {
				assert.Contains(t, err.Error(), tt.expectedErrContains)
			} else {
				assert.NoError(t, err)
			}

			// Clean up
			patches.Reset()
		})
	}
}
