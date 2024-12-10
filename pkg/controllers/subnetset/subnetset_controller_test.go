package subnetset

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v12 "k8s.io/api/core/v1"
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
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	ctlcommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetbinding"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

type fakeRecorder struct{}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
}

func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}

func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}

type fakeOrgRootClient struct {
}

func (f fakeOrgRootClient) Get(basePathParam *string, filterParam *string, typeFilterParam *string) (model.OrgRoot, error) {
	return model.OrgRoot{}, nil
}

func (f fakeOrgRootClient) Patch(orgRootParam model.OrgRoot, enforceRevisionCheckParam *bool) error {
	return errors.New("patch error")
}

type fakeSubnetStatusClient struct {
}

func (f fakeSubnetStatusClient) List(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string) (model.VpcSubnetStatusListResult, error) {
	dhcpServerAddress := "1.1.1.1"
	ipAddressType := "fakeIpAddressType"
	networkAddress := "2.2.2.2"
	gatewayAddress := "3.3.3.3"
	return model.VpcSubnetStatusListResult{
		Results: []model.VpcSubnetStatus{
			{
				DhcpServerAddress: &gatewayAddress,
				GatewayAddress:    &dhcpServerAddress,
				IpAddressType:     &ipAddressType,
				NetworkAddress:    &networkAddress,
			},
		},
		Status: nil,
	}, nil
}

func createFakeSubnetSetReconciler(objs []client.Object) *SubnetSetReconciler {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(objs...).Build()
	vpcService := &vpc.VPCService{
		Service: common.Service{
			Client:    fakeClient,
			NSXClient: &nsx.Client{},
		},
	}
	subnetService := &subnet.SubnetService{
		Service: common.Service{
			Client: fakeClient,
			NSXClient: &nsx.Client{
				OrgRootClient:      &fakeOrgRootClient{},
				SubnetStatusClient: &fakeSubnetStatusClient{},
			},
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "clusterName",
				},
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
		VPCService:        vpcService,
		SubnetService:     subnetService,
		SubnetPortService: subnetPortService,
		Recorder:          &fakeRecorder{},
		StatusUpdater:     ctlcommon.NewStatusUpdater(fakeClient, subnetService.NSXConfig, &fakeRecorder{}, MetricResTypeSubnetSet, "Subnet", "SubnetSet"),
	}
}

func TestReconcile(t *testing.T) {
	subnetsetName := "test-subnetset"
	ns := "test-namespace"
	subnetSet := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      subnetsetName,
			Namespace: ns,
		},
		Spec: v1alpha1.SubnetSetSpec{},
	}

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
			patches: func(r *SubnetSetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnetSet", func(_ *SubnetSetReconciler, _ context.Context, _ *v1alpha1.SubnetSet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
				})
				return patches
			},
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
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnetSet", func(_ *SubnetSetReconciler, _ context.Context, _ *v1alpha1.SubnetSet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
				})
				return patches
			},
		},
		{
			name:      "Create a SubnetSet",
			expectRes: ResultNormal,
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
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnetSet", func(_ *SubnetSetReconciler, _ context.Context, _ *v1alpha1.SubnetSet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
				})
				return patches
			},
		},
		{
			// return nil and requeue when UpdateSubnetSet failed
			name:         "Create a SubnetSet failed to UpdateSubnetSet",
			expectRes:    ResultRequeue,
			expectErrStr: "",
			patches: func(r *SubnetSetReconciler) *gomonkey.Patches {
				vpcnetworkInfo := &common.VPCNetworkConfigInfo{DefaultSubnetSize: 32}
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) *common.VPCNetworkConfigInfo {
					return vpcnetworkInfo
				})

				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnetSet", func(_ *SubnetSetReconciler, _ context.Context, _ *v1alpha1.SubnetSet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
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
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnetSet", func(_ *SubnetSetReconciler, _ context.Context, _ *v1alpha1.SubnetSet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
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
				for i := 0; i < common.MaxTagsCount; i++ {
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
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnetSet", func(_ *SubnetSetReconciler, _ context.Context, _ *v1alpha1.SubnetSet) []v1alpha1.SubnetConnectionBindingMap {
					return []v1alpha1.SubnetConnectionBindingMap{}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
					id1 := "fake-id"
					path := "/orgs/default/projects/nsx_operator_e2e_test/vpcs/subnet-e2e_8f36f7fc-90cd-4e65-a816-daf3ecd6a0f9/subnets/fake-path"
					basicTags1 := util.BuildBasicTags("fakeClusterName", subnetSet, "")
					scopeNamespace := common.TagScopeNamespace
					basicTags1 = append(basicTags1, model.Tag{
						Scope: &scopeNamespace,
						Tag:   &ns,
					})
					basicTags2 := util.BuildBasicTags("fakeClusterName", subnetSet, "")
					ns2 := "ns2"
					basicTags2 = append(basicTags2, model.Tag{
						Scope: &scopeNamespace,
						Tag:   &ns2,
					})
					vpcSubnet1 := model.VpcSubnet{Id: &id1, Path: &path}
					vpcSubnet2 := model.VpcSubnet{Id: &id1, Path: &path, Tags: basicTags1}
					vpcSubnet3 := model.VpcSubnet{Id: &id1, Path: &path, Tags: basicTags2}
					return []*model.VpcSubnet{&vpcSubnet1, &vpcSubnet2, &vpcSubnet3}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "UpdateSubnetSet", func(_ *subnet.SubnetService, ns string, vpcSubnets []*model.VpcSubnet, tags []model.Tag, dhcpMode string) error {
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

			namespace := &v12.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}

			r := createFakeSubnetSetReconciler([]client.Object{subnetSet, namespace})
			if testCase.patches != nil {
				patches := testCase.patches(r)
				defer patches.Reset()
			}

			res, err := r.Reconcile(ctx, req)

			if testCase.expectErrStr != "" {
				assert.ErrorContains(t, err, testCase.expectErrStr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, testCase.expectRes, res)
		})
	}
}

// Test Reconcile - SubnetSet Deletion
func TestReconcile_DeleteSubnetSet(t *testing.T) {
	subnetSetName := "test-subnetset"
	testCases := []struct {
		name              string
		existingSubnetSet *v1alpha1.SubnetSet
		expectRes         ctrl.Result
		expectErrStr      string
		patches           func(r *SubnetSetReconciler) *gomonkey.Patches
	}{
		{
			name: "Delete success",
			existingSubnetSet: &v1alpha1.SubnetSet{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "fake-subnetSet-uid-2"},
				Spec:       v1alpha1.SubnetSetSpec{},
				Status:     v1alpha1.SubnetSetStatus{},
			},
			patches: func(r *SubnetSetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
					id1 := "fake-id"
					path := "fake-path"
					tags := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetSetCRUID), Tag: common.String("fake-subnetSet-uid-2")},
						{Scope: common.String(common.TagScopeSubnetSetCRName), Tag: common.String(subnetSetName)},
					}
					vpcSubnetSkip := model.VpcSubnet{Id: &id1, Path: &path, Tags: tags}

					id2 := "fake-id-1"
					path2 := "/orgs/default/projects/nsx_operator_e2e_test/vpcs/subnet-xxx/subnets/" + id2
					tagStale := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetSetCRUID), Tag: common.String("fake-subnetSet-uid-stale")},
						{Scope: common.String(common.TagScopeSubnetSetCRName), Tag: common.String(subnetSetName)},
					}
					vpcSubnetDelete := model.VpcSubnet{Id: &id2, Path: &path2, Tags: tagStale}
					return []*model.VpcSubnet{
						&vpcSubnetSkip, &vpcSubnetDelete,
					}
				})

				patches.ApplyMethod(reflect.TypeOf(r.BindingService), "DeleteSubnetConnectionBindingMapsByParentSubnet", func(_ *subnetbinding.BindingService, parentSubnet *model.VpcSubnet) error {
					return nil
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
			name:         "Delete failed with stale SubnetPort and requeue",
			expectErrStr: "hasStaleSubnetPort: true",
			patches: func(r *SubnetSetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
					id1 := "fake-id"
					path := "fake-path"
					tags := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetSetCRUID), Tag: common.String("fake-subnetSet-uid-2")},
						{Scope: common.String(common.TagScopeSubnetSetCRName), Tag: common.String(subnetSetName)},
					}
					vpcSubnetSkip := model.VpcSubnet{Id: &id1, Path: &path, Tags: tags}

					id2 := "fake-id-1"
					path2 := "/orgs/default/projects/nsx_operator_e2e_test/vpcs/subnet-xxx/subnets/fake-path-2"
					tagStale := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetSetCRUID), Tag: common.String("fake-subnetSet-uid-stale")},
						{Scope: common.String(common.TagScopeSubnetSetCRName), Tag: common.String(subnetSetName)},
					}
					vpcSubnetDelete := model.VpcSubnet{Id: &id2, Path: &path2, Tags: tagStale}
					return []*model.VpcSubnet{
						&vpcSubnetSkip, &vpcSubnetDelete,
					}
				})

				patches.ApplyMethod(reflect.TypeOf(r.BindingService), "DeleteSubnetConnectionBindingMapsByParentSubnet", func(_ *subnetbinding.BindingService, parentSubnet *model.VpcSubnet) error {
					return nil
				})

				patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetPortsOfSubnet", func(_ *subnetport.SubnetPortService, _ string) (ports []*model.VpcSubnetPort) {
					id := "fake-subnetport-0"
					return []*model.VpcSubnetPort{
						{
							Id: &id,
						},
					}
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
					return nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListSubnetSetID", func(_ *subnet.SubnetService, ctx context.Context) (sets.Set[string], error) {
					res := sets.New[string]("fake-subnetSet-uid-2")
					return res, nil
				})
				return patches
			},
			expectRes: ResultRequeue,
		},
		{
			name:         "Delete NSX Subnet failed and requeue",
			expectErrStr: "multiple errors occurred while deleting Subnets",
			patches: func(r *SubnetSetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.SubnetStore), "GetByIndex", func(_ *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
					id1 := "fake-id"
					path := "fake-path"
					tags := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetSetCRUID), Tag: common.String("fake-subnetSet-uid-2")},
						{Scope: common.String(common.TagScopeSubnetSetCRName), Tag: common.String(subnetSetName)},
					}
					vpcSubnetSkip := model.VpcSubnet{Id: &id1, Path: &path, Tags: tags}

					id2 := "fake-id-1"
					path2 := "/orgs/default/projects/nsx_operator_e2e_test/vpcs/subnet-xxx/subnets/fake-path-2"
					tagStale := []model.Tag{
						{Scope: common.String(common.TagScopeSubnetSetCRUID), Tag: common.String("fake-subnetSet-uid-stale")},
						{Scope: common.String(common.TagScopeSubnetSetCRName), Tag: common.String(subnetSetName)},
					}
					vpcSubnetDelete := model.VpcSubnet{Id: &id2, Path: &path2, Tags: tagStale}
					return []*model.VpcSubnet{
						&vpcSubnetSkip, &vpcSubnetDelete,
					}
				})

				patches.ApplyMethod(reflect.TypeOf(r.BindingService), "DeleteSubnetConnectionBindingMapsByParentSubnet", func(_ *subnetbinding.BindingService, parentSubnet *model.VpcSubnet) error {
					return nil
				})

				patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetPortsOfSubnet", func(_ *subnetport.SubnetPortService, _ string) (ports []*model.VpcSubnetPort) {
					return nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
					return errors.New("delete NSX Subnet failed")
				})
				patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListSubnetSetID", func(_ *subnet.SubnetService, ctx context.Context) (sets.Set[string], error) {
					res := sets.New[string]("fake-subnetSet-uid-2")
					return res, nil
				})
				return patches
			},
			expectRes: ResultRequeue,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.TODO()
			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: subnetSetName, Namespace: "default"}}
			var objs []client.Object
			if testCase.existingSubnetSet != nil {
				objs = append(objs, testCase.existingSubnetSet)
			}
			r := createFakeSubnetSetReconciler(objs)
			patches := testCase.patches(r)
			defer patches.Reset()

			res, err := r.Reconcile(ctx, req)

			if testCase.expectErrStr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, testCase.expectErrStr)
			}
			assert.Equal(t, testCase.expectRes, res)
		})
	}
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
		path := "/orgs/default/projects/nsx_operator_e2e_test/vpcs/subnet-e2e_8f36f7fc-90cd-4e65-a816-daf3ecd6a0f9/subnets/" + id1
		vpcSubnet := model.VpcSubnet{Id: &id1, Path: &path}
		return []*model.VpcSubnet{
			&vpcSubnet,
		}
	})

	defer patches.Reset()

	patches.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetBindingCRsBySubnetSet", func(_ *SubnetSetReconciler, _ context.Context, _ *v1alpha1.SubnetSet) []v1alpha1.SubnetConnectionBindingMap {
		return []v1alpha1.SubnetConnectionBindingMap{}
	})

	patches.ApplyPrivateMethod(reflect.TypeOf(r), "getNSXSubnetBindingsBySubnetSet", func(_ *SubnetSetReconciler, _ string) []*v1alpha1.SubnetConnectionBindingMap {
		return []*v1alpha1.SubnetConnectionBindingMap{}
	})

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

// Test Merge SubnetSet Status Condition
func TestMergeSubnetSetStatusCondition(t *testing.T) {
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

	newCondition := v1alpha1.Condition{
		Type:   v1alpha1.Ready,
		Status: v12.ConditionStatus(metav1.ConditionTrue),
	}

	updated := mergeSubnetSetStatusCondition(subnetset, &newCondition)

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
		path := "/orgs/default/projects/nsx_operator_e2e_test/vpcs/subnet-e2e_8f36f7fc-90cd-4e65-a816-daf3ecd6a0f9/subnets/fake-path"
		vpcSubnet1 := model.VpcSubnet{Id: &id1, Path: &path}
		return []*model.VpcSubnet{
			&vpcSubnet1,
		}
	})
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetPortsOfSubnet", func(_ *subnetport.SubnetPortService, _ string) (ports []*model.VpcSubnetPort) {
		return nil
	})
	patches.ApplyMethod(reflect.TypeOf(r.BindingService), "DeleteSubnetConnectionBindingMapsByParentSubnet", func(_ *subnetbinding.BindingService, parentSubnet *model.VpcSubnet) error {
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
		path := "/orgs/default/projects/nsx_operator_e2e_test/vpcs/subnet-e2e_8f36f7fc-90cd-4e65-a816-daf3ecd6a0f9/subnets/fake-path"
		vpcSubnet1 := model.VpcSubnet{Id: &id1, Path: &path}
		invalidPath := "fakePath"
		vpcSubnet2 := model.VpcSubnet{Id: &id1, Path: &invalidPath}
		return []*model.VpcSubnet{
			&vpcSubnet1, &vpcSubnet2,
		}
	})

	// fake SubnetSetLocks
	lock := sync.Mutex{}
	subnetSetId := types.UID(uuid.NewString())
	ctlcommon.SubnetSetLocks.LoadOrStore(subnetSetId, &lock)

	r.CollectGarbage(ctx)
	// the lock for should be deleted
	_, ok := ctlcommon.SubnetSetLocks.Load(subnetSetId)
	assert.False(t, ok)
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

type mockWebhookServer struct{}

func (m *mockWebhookServer) Register(path string, hook http.Handler) {
	return
}

func (m *mockWebhookServer) Start(ctx context.Context) error {
	return nil
}

func (m *mockWebhookServer) StartedChecker() healthz.Checker {
	return nil
}

func (m *mockWebhookServer) WebhookMux() *http.ServeMux {
	return nil
}

func (m *mockWebhookServer) NeedLeaderElection() bool {
	return true
}

func TestStartSubnetSetController(t *testing.T) {
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
	subnetBindingService := &subnetbinding.BindingService{
		Service:      common.Service{},
		BindingStore: nil,
	}

	mockMgr := &MockManager{scheme: runtime.NewScheme()}

	testCases := []struct {
		name          string
		expectErrStr  string
		webHookServer webhook.Server
		patches       func() *gomonkey.Patches
	}{
		// expected no error when starting the SubnetSet controller with webhook
		{
			name:          "StartSubnetSetController with webhook",
			webHookServer: &mockWebhookServer{},
			patches: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(ctlcommon.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context)) {
					return
				})
				patches.ApplyMethod(reflect.TypeOf(&ctrl.Builder{}), "Complete", func(_ *ctrl.Builder, r reconcile.Reconciler) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(&SubnetSetReconciler{}), "setupWithManager", func(_ *SubnetSetReconciler, mgr ctrl.Manager) error {
					return nil
				})
				return patches
			},
		},
		// expected no error when starting the SubnetSet controller without webhook
		{
			name:          "StartSubnetSetController without webhook",
			webHookServer: nil,
			patches: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(ctlcommon.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context)) {
					return
				})
				patches.ApplyMethod(reflect.TypeOf(&ctrl.Builder{}), "Complete", func(_ *ctrl.Builder, r reconcile.Reconciler) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(&SubnetSetReconciler{}), "setupWithManager", func(_ *SubnetSetReconciler, mgr ctrl.Manager) error {
					return nil
				})
				return patches
			},
		},
		{
			name:          "StartSubnetSetController return error",
			expectErrStr:  "failed to setupWithManager",
			webHookServer: &mockWebhookServer{},
			patches: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(ctlcommon.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context)) {
					return
				})
				patches.ApplyMethod(reflect.TypeOf(&ctrl.Builder{}), "Complete", func(_ *ctrl.Builder, r reconcile.Reconciler) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(&SubnetSetReconciler{}), "setupWithManager", func(_ *SubnetSetReconciler, mgr ctrl.Manager) error {
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

			err := StartSubnetSetController(mockMgr, subnetService, subnetPortService, vpcService, subnetBindingService, testCase.webHookServer)

			if testCase.expectErrStr != "" {
				assert.ErrorContains(t, err, testCase.expectErrStr)
			} else {
				assert.NoError(t, err, "expected no error when starting the SubnetSet controller")
			}
		})
	}
}
