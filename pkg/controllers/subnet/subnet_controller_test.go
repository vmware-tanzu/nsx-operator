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
	"github.com/stretchr/testify/require"
	apierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
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
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/vmware-tanzu/nsx-operator/pkg/util"

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
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/test/unittests"
)

func TestSubnetReconciler_GarbageCollector(t *testing.T) {
	for _, tc := range []struct {
		name                                           string
		live, ports, deleteFailure, listFailure, empty bool
		wantDeletes, wantCleanup                       int
		wantError                                      string
	}{
		{name: "delete stale subnets", wantDeletes: 2, wantCleanup: 2},
		{name: "retain live subnet", live: true},
		{name: "attached ports block deletion", ports: true, wantError: "still attached by 1 port(s)"},
		{name: "delete failure stops cleanup", deleteFailure: true, wantDeletes: 1, wantError: "delete failed"},
		{name: "list failure stops GC", listFailure: true, wantError: "list failed"},
		{name: "empty store", empty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objects := []client.Object{}
			if tc.live {
				objects = append(objects, &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "live", Namespace: "default", UID: "owner"}})
			}
			r := createFakeSubnetReconciler(objects)
			patches := gomonkey.NewPatches()
			defer patches.Reset()
			if tc.listFailure {
				patches.ApplyMethod(reflect.TypeOf(r.Client), "List", func(_ client.Client, _ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
					return errors.New("list failed")
				})
			}
			patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListSubnetIDsFromNSXSubnets", func(_ *subnet.SubnetService) sets.Set[string] {
				if tc.empty {
					return sets.New[string]()
				}
				return sets.New("owner")
			})
			patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListSubnetCreatedBySubnet", func(_ *subnet.SubnetService, _ string) []*model.VpcSubnet {
				return []*model.VpcSubnet{{Id: common.String("stale-1"), Path: common.String("/subnets/stale-1")}, {Id: common.String("stale-2"), Path: common.String("/subnets/stale-2")}}
			})
			deleted, cleaned := []string{}, []string{}
			patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetPortsOfSubnet", func(_ *subnetport.SubnetPortService, _ string) []*model.VpcSubnetPort {
				if tc.ports {
					return []*model.VpcSubnetPort{{}}
				}
				return nil
			})
			patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "DeleteSubnet", func(_ *subnet.SubnetService, obj model.VpcSubnet) error {
				deleted = append(deleted, *obj.Id)
				if tc.deleteFailure {
					return errors.New("delete failed")
				}
				return nil
			})
			patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "DeletePortCount", func(_ *subnetport.SubnetPortService, path string) { cleaned = append(cleaned, path) })
			err := r.CollectGarbage(context.Background())
			errText := ""
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				errText = err.Error()
			} else {
				require.NoError(t, err)
			}
			assert.Len(t, deleted, tc.wantDeletes)
			assert.Len(t, cleaned, tc.wantCleanup)
			unittests.NewGuard(t, unittests.Metadata{
				Feature:     "subnet",
				Component:   "subnet_controller",
				Scenario:    tc.name,
				Description: "Verify GC deletion attempts, port-count cleanup and errors",
			}).AssertOutput(map[string]interface{}{"deleteAttempts": deleted, "portCountCleanup": cleaned, "error": errText})
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
				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "GetNetworkStackFromNC", func(_ *vpc.VPCService, config *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
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
				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "GetNetworkStackFromNC", func(_ *vpc.VPCService, config *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
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

				patches.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
					id1 := "fake-id"
					path := "/orgs/default/projects/nsx_operator_e2e_test/vpcs/subnet-e2e_8f36f7fc-90cd-4e65-a816-daf3ecd6a0f9/subnets/subnet_fake-path"
					tags := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-subnet-uid-2")},
						{Scope: common.String(common.TagScopeSubnetCRName), Tag: common.String(subnetName)},
					}
					return []*model.VpcSubnet{{Id: &id1, Path: &path, Tags: tags}}
				})

				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetStatus", func(_ *subnet.SubnetService, _ *model.VpcSubnet) ([]model.VpcSubnetStatus, error) {
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
					SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated)},
					AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
						StaticIPAllocation: v1alpha1.StaticIPAllocation{
							Enabled: common.Bool(true),
						},
					},
				},
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
				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "GetNetworkStackFromNC", func(_ *vpc.VPCService, config *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
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

				patches.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
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
					SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated)},
					AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
						StaticIPAllocation: v1alpha1.StaticIPAllocation{
							Enabled: common.Bool(true),
						},
					},
				},
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
				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "GetNetworkStackFromNC", func(_ *vpc.VPCService, config *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
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
		{
			name: "Create or Update Subnet with reservedIPRanges failure",
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
					return nil, nsxutil.NewNSXApiError(&model.ApiError{
						ErrorCode:    util.Ptr(int64(508134)),
						ErrorMessage: util.Ptr("Test error message"),
						Details:      util.Ptr("Reserved IP ranges cannot be overlapped"),
					}, apierrors.ErrorType_INVALID_REQUEST)
				})
				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "IsDefaultNSXProject", func(_ *vpc.VPCService, orgID, projectID string) (bool, error) {
					return false, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "GetNetworkStackFromNC", func(_ *vpc.VPCService, config *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
				})
				return patches
			},
			existingSubnetCR: createNewSubnet(),
			expectRes:        ResultNormal,
		},
		{
			name: "Create or Update Subnet with VPCNetworkConfiguration is nil",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {

				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, ns string) []common.VPCResourceInfo {
					return []common.VPCResourceInfo{
						{OrgID: "org-id", ProjectID: "project-id", VPCID: "vpc-id", ID: "fake-id"},
					}
				})
				patches.ApplyFunc(common2.GetDefaultAccessMode, func(service common.VPCServiceProvider, ns string) (v1alpha1.AccessMode, *v1alpha1.VPCNetworkConfiguration, error) {
					return v1alpha1.AccessMode(""), nil, fmt.Errorf("vpcNeworkConfig is nil")
				})
				return patches
			},
			existingSubnetCR: createNewSubnet(),
			expectErrStr:     "vpcNeworkConfig is nil",
			expectRes:        ResultNormal,
		},
		{
			name: "Create or Update Subnet with get access mode error",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {

				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, ns string) []common.VPCResourceInfo {
					return []common.VPCResourceInfo{
						{OrgID: "org-id", ProjectID: "project-id", VPCID: "vpc-id", ID: "fake-id"},
					}
				})
				patches.ApplyFunc(common2.GetDefaultAccessMode, func(service common.VPCServiceProvider, ns string) (v1alpha1.AccessMode, *v1alpha1.VPCNetworkConfiguration, error) {
					return v1alpha1.AccessMode(""), nil, fmt.Errorf("vpc not found")
				})
				return patches
			},
			existingSubnetCR: createNewSubnet(),
			expectErrStr:     "vpc not found",
			expectRes:        ResultNormal,
		},
		{
			name: "Create Subnet with IPv6",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				vpcnetworkConfig := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DefaultIPv6PrefixLength: 64}}
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return vpcnetworkConfig, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "GetNetworkStackFromNC", func(_ *vpc.VPCService, config *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
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
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
					id1 := "fake-id"
					path := "/orgs/default/projects/project-id/vpcs/vpc-id/subnets/subnet_fake-path"
					tags := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-subnet-uid-2")},
						{Scope: common.String(common.TagScopeSubnetCRName), Tag: common.String("test-subnet")},
					}
					return []*model.VpcSubnet{{Id: &id1, Path: &path, Tags: tags}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetStatus", func(_ *subnet.SubnetService, _ *model.VpcSubnet) ([]model.VpcSubnetStatus, error) {
					fakeStatus := model.VpcSubnetStatus{}
					value := ""
					fakeStatus.GatewayAddress = &value
					fakeStatus.DhcpServerAddress = &value
					fakeStatus.NetworkAddress = &value
					return []model.VpcSubnetStatus{fakeStatus}, nil
				})
				return patches
			},
			existingSubnetCR: &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{Name: "test-subnet", Namespace: "default"},
				Spec: v1alpha1.SubnetSpec{
					IPAddressType:    v1alpha1.IPAddressTypeIPv6,
					IPv6PrefixLength: 64,
					SubnetDHCPv6Config: v1alpha1.SubnetDHCPv6Config{
						Mode: v1alpha1.DHCPv6ConfigMode(v1alpha1.DHCPv6ConfigModeServer),
					},
				},
			},
			expectRes: ResultNormal,
		},
		{
			name: "Create Subnet with IPv4IPv6 and DHCPv6 Stateless",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-subnet"}},
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				vpcnetworkConfig := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DefaultSubnetSize: 24, DefaultIPv6PrefixLength: 80}}
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return vpcnetworkConfig, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.VPCService), "GetNetworkStackFromNC", func(_ *vpc.VPCService, config *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
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
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
					id1 := "fake-id"
					path := "/orgs/default/projects/project-id/vpcs/vpc-id/subnets/subnet_fake-path"
					tags := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-subnet-uid-2")},
						{Scope: common.String(common.TagScopeSubnetCRName), Tag: common.String("test-subnet")},
					}
					return []*model.VpcSubnet{{Id: &id1, Path: &path, Tags: tags}}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetStatus", func(_ *subnet.SubnetService, _ *model.VpcSubnet) ([]model.VpcSubnetStatus, error) {
					fakeStatus := model.VpcSubnetStatus{}
					value := ""
					fakeStatus.GatewayAddress = &value
					fakeStatus.DhcpServerAddress = &value
					fakeStatus.NetworkAddress = &value
					return []model.VpcSubnetStatus{fakeStatus}, nil
				})
				return patches
			},
			existingSubnetCR: &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{Name: "test-subnet", Namespace: "default"},
				Spec: v1alpha1.SubnetSpec{
					IPAddressType:    v1alpha1.IPAddressTypeIPv4IPv6,
					IPv4SubnetSize:   24,
					IPv6PrefixLength: 80,
					SubnetDHCPv6Config: v1alpha1.SubnetDHCPv6Config{
						Mode: v1alpha1.DHCPv6ConfigMode(v1alpha1.DHCPv6ConfigModeServerStateless),
					},
				},
			},
			expectRes: ResultNormal,
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

			guard := unittests.NewGuard(t, unittests.Metadata{
				Feature:     "subnet",
				Component:   "subnet_controller",
				Scenario:    "reconcile_subnet",
				Description: fmt.Sprintf("Verify Subnet reconcile output under scenario: %s", testCase.name),
			})

			result, err := reconciler.Reconcile(ctx, testCase.req)
			if testCase.expectErrStr != "" {
				assert.ErrorContains(t, err, testCase.expectErrStr)
				guard.AssertOutput(err.Error())
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, testCase.expectRes, result)

			if testCase.expectSubnetCR != nil {
				actualSubnetCR := &v1alpha1.Subnet{}
				assert.NoError(t, reconciler.Client.Get(ctx, testCase.req.NamespacedName, actualSubnetCR))
				assert.Equal(t, testCase.expectSubnetCR.Spec, actualSubnetCR.Spec)
				guard.AssertOutput(actualSubnetCR)
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
				patches := gomonkey.ApplyMethod(reflect.TypeOf(&SubnetReconciler{}), "StartController", func(_ *SubnetReconciler, _ ctrl.Manager, _ webhook.Server) error {
					return nil
				})
				return patches
			},
		},
		{
			name:         "StartSubnetController return error",
			expectErrStr: "failed to setupWithManager",
			patches: func() *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(&SubnetReconciler{}), "StartController", func(_ *SubnetReconciler, _ ctrl.Manager, _ webhook.Server) error {
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
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(true),
				},
			},
		},
	}
	testSubnet2 := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: subnetName, Namespace: ns, Finalizers: []string{common.SubnetFinalizerName}},
		Spec: v1alpha1.SubnetSpec{
			AccessMode:     v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
			IPv4SubnetSize: 16,
			VPCName:        "project:test-vpc",
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(true),
				},
			},
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
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(true),
				},
			},
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

			guard := unittests.NewGuard(t, unittests.Metadata{
				Feature:     "subnet",
				Component:   "subnet_controller",
				Scenario:    "reconcile_with_binding_maps",
				Description: fmt.Sprintf("Verify Subnet reconcile with SubnetConnectionBindingMaps output under scenario: %s", tc.name),
			})

			res, err := r.Reconcile(ctx, req)

			if tc.expectErrStr != "" {
				assert.NotNil(t, err, "Expected an error but got nil")
				if err != nil {
					assert.Contains(t, err.Error(), tc.expectErrStr, "Error message does not contain expected string")
					guard.AssertOutput(err.Error())
				}
			} else {
				assert.NoError(t, err)
				guard.AssertOutput(res)
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
	for _, tc := range []struct {
		name                                                string
		shared, missing, deleting, noAddresses, listFailure bool
		lookupError, reconcileError                         string
		result                                              reconcile.Result
		wantCalls                                           int
		wantError                                           string
	}{
		{name: "restore success", wantCalls: 2},
		{name: "aggregate reconcile failures", reconcileError: "restore failed", wantCalls: 2, wantError: "restore failed"},
		{name: "requeue requires retry", result: ResultRequeue, wantCalls: 2, wantError: "reconciliation requested requeue"},
		{name: "delayed requeue requires retry", result: reconcile.Result{RequeueAfter: time.Second}, wantCalls: 2, wantError: "reconciliation requested requeue"},
		{name: "list failure", listFailure: true, wantError: "list failed"},
		{name: "skip terminating subnet", deleting: true},
		{name: "skip subnet without addresses", noAddresses: true},
		{name: "skip existing shared subnet", shared: true},
		{name: "missing shared subnet reports status", shared: true, missing: true},
		{name: "shared lookup failure retries", shared: true, lookupError: "lookup failed", wantError: "lookup failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mockCtl := gomock.NewController(t)
			k8sClient := mockClient.NewMockClient(mockCtl)
			r := &SubnetReconciler{Client: k8sClient, SubnetService: &subnet.SubnetService{}, StatusUpdater: common2.StatusUpdater{}}
			items := []v1alpha1.Subnet{}
			for _, name := range []string{"subnet-1", "subnet-2"} {
				obj := v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns-1"}, Status: v1alpha1.SubnetStatus{NetworkAddresses: []string{"10.0.0.0/28"}}}
				if tc.shared {
					obj.Annotations = map[string]string{common.AnnotationAssociatedResource: ":vpc-1:" + name}
				}
				if tc.deleting {
					now := metav1.Now()
					obj.DeletionTimestamp = &now
				}
				if tc.noAddresses {
					obj.Status.NetworkAddresses = nil
				}
				items = append(items, obj)
			}
			k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
				if tc.listFailure {
					return errors.New("list failed")
				}
				list.(*v1alpha1.SubnetList).Items = items
				return nil
			})
			calls, lookups, warnings := []string{}, []string{}, []string{}
			patches := gomonkey.NewPatches()
			defer patches.Reset()
			patches.ApplyMethod(reflect.TypeOf(r), "Reconcile", func(_ *SubnetReconciler, _ context.Context, req reconcile.Request) (reconcile.Result, error) {
				calls = append(calls, req.NamespacedName.String())
				if tc.reconcileError != "" {
					return tc.result, errors.New(tc.reconcileError)
				}
				return tc.result, nil
			})
			patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetNSXSubnetFromCacheOrAPI", func(_ *subnet.SubnetService, resource string, force bool) (*model.VpcSubnet, error) {
				lookups = append(lookups, fmt.Sprintf("%s forceAPI=%t", resource, force))
				if tc.missing {
					return nil, nsxutil.NewNSXApiError(&model.ApiError{}, apierrors.ErrorType_NOT_FOUND)
				}
				if tc.lookupError != "" {
					return nil, errors.New(tc.lookupError)
				}
				return &model.VpcSubnet{}, nil
			})
			patches.ApplyMethod(reflect.TypeOf(&r.StatusUpdater), "UpdateFail", func(_ *common2.StatusUpdater, _ context.Context, obj client.Object, _ error, msg string, _ common2.UpdateFailStatusFn, _ ...interface{}) {
				warnings = append(warnings, client.ObjectKeyFromObject(obj).String()+": "+msg)
			})
			err := r.RestoreReconcile()
			errText := ""
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				errText = err.Error()
			} else {
				require.NoError(t, err)
			}
			assert.NotContains(t, errText, "%!w")
			assert.Len(t, calls, tc.wantCalls)
			if tc.missing {
				assert.Len(t, warnings, 2)
			} else {
				assert.Empty(t, warnings)
			}
			unittests.NewGuard(t, unittests.Metadata{
				Feature:     "subnet",
				Component:   "subnet_controller",
				Scenario:    tc.name,
				Description: "Verify restore filtering, shared lookup, warning updates and aggregated failures",
			}).AssertOutput(map[string]interface{}{"reconcileRequests": calls, "sharedLookups": lookups, "statusWarnings": warnings, "error": errText})
		})
	}
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

func TestSetDefaultIPv4SubnetSizeValue(t *testing.T) {
	testCases := []struct {
		name         string
		vpcConfig    *v1alpha1.VPCNetworkConfiguration
		patches      func(r *SubnetReconciler) *gomonkey.Patches
		expectErr    bool
		expectedSize int
	}{
		{
			name:         "Provided VPCNetworkConfig",
			vpcConfig:    &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DefaultSubnetSize: 24}},
			expectErr:    false,
			expectedSize: 24,
		},
		{
			name:      "Fetch VPCNetworkConfig success",
			vpcConfig: nil,
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				vpcnetworkConfig := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DefaultSubnetSize: 26}}
				return gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return vpcnetworkConfig, nil
				})
			},
			expectErr:    false,
			expectedSize: 26,
		},
		{
			name:      "Fetch VPCNetworkConfig error",
			vpcConfig: nil,
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return nil, errors.New("get error")
				})
			},
			expectErr:    true,
			expectedSize: 0,
		},
		{
			name:      "Fetch VPCNetworkConfig nil",
			vpcConfig: nil,
			patches: func(r *SubnetReconciler) *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return nil, nil
				})
			},
			expectErr:    true,
			expectedSize: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			subnetCR := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "test-subnet", Namespace: "test-ns"}}
			r := createFakeSubnetReconciler([]client.Object{subnetCR})
			var patches *gomonkey.Patches
			if tc.patches != nil {
				patches = tc.patches(r)
				defer patches.Reset()
			}
			err := r.setDefaultIPv4SubnetSizeValue(ctx, subnetCR, tc.vpcConfig)
			if tc.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			// verify expected size
			assert.Equal(t, tc.expectedSize, subnetCR.Spec.IPv4SubnetSize)
		})
	}
}

func TestSubnetReconciler_updateSubnetStatus(t *testing.T) {
	r := &SubnetReconciler{
		SubnetService: &subnet.SubnetService{},
	}
	tests := []struct {
		name         string
		nsxSubnets   []*model.VpcSubnet
		statusList   []model.VpcSubnetStatus
		initialConds []v1alpha1.Condition
		hasChanged   bool
		expectErr    bool
	}{
		{
			name:       "empty nsx subnets",
			nsxSubnets: []*model.VpcSubnet{},
			hasChanged: false,
			expectErr:  true,
		},
		{
			name:       "status updated",
			nsxSubnets: []*model.VpcSubnet{{Id: common.String("subnet-1")}},
			statusList: []model.VpcSubnetStatus{
				{
					NetworkAddress:    common.String("10.0.0.0/24"),
					GatewayAddress:    common.String("10.0.0.1"),
					DhcpServerAddress: common.String("10.0.0.2"),
				},
			},
			hasChanged: true,
			expectErr:  false,
		},
		{
			name:       "status not updated",
			nsxSubnets: []*model.VpcSubnet{{Id: common.String("subnet-1")}},
			statusList: []model.VpcSubnetStatus{
				{
					NetworkAddress:    common.String("10.0.0.0/24"),
					GatewayAddress:    common.String("10.0.0.1"),
					DhcpServerAddress: common.String("10.0.0.2"),
				},
			},
			hasChanged: false,
			expectErr:  false,
		},
		{
			name:       "nil network address status",
			nsxSubnets: []*model.VpcSubnet{{Id: common.String("subnet-1")}},
			statusList: []model.VpcSubnetStatus{
				{
					GatewayAddress: common.String("10.0.0.1"),
				},
			},
			hasChanged: false,
			expectErr:  true,
		},
		{
			name:       "nil gateway address status",
			nsxSubnets: []*model.VpcSubnet{{Id: common.String("subnet-1")}},
			statusList: []model.VpcSubnetStatus{
				{
					NetworkAddress: common.String("10.0.0.0/24"),
				},
			},
			hasChanged: false,
			expectErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{UID: "uid-1"},
				Status: v1alpha1.SubnetStatus{
					NetworkAddresses:    []string{"10.0.0.0/24"},
					GatewayAddresses:    []string{"10.0.0.1"},
					DHCPServerAddresses: []string{"10.0.0.2"},
				},
			}
			if tt.hasChanged {
				obj.Status.NetworkAddresses = []string{}
			}

			patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetsByIndex",
				func(_ *subnet.SubnetService, _ string, _ string) []*model.VpcSubnet {
					return tt.nsxSubnets
				})
			defer patches.Reset()

			patches2 := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetStatus",
				func(_ *subnet.SubnetService, _ *model.VpcSubnet) ([]model.VpcSubnetStatus, error) {
					return tt.statusList, nil
				})
			defer patches2.Reset()

			changed, err := r.updateSubnetStatus(obj)
			if tt.expectErr {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, tt.hasChanged, changed)
			}
		})
	}
}
