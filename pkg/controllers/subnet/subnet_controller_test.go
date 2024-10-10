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
	service := &subnet.SubnetService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
	}
	// Subnet doesn't have TagScopeSubnetSetCRId (not  belong to SubnetSet)
	// gc collect item "2345", local store has more item than k8s cache
	patch := gomonkey.ApplyMethod(reflect.TypeOf(service), "ListSubnetCreatedBySubnet", func(_ *subnet.SubnetService, uid string) []*model.VpcSubnet {
		tags1 := []model.Tag{{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("2345")}}
		tags2 := []model.Tag{{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("1234")}}
		var a []*model.VpcSubnet
		id1 := "2345"
		a = append(a, &model.VpcSubnet{Id: &id1, Tags: tags1})
		id2 := "1234"
		a = append(a, &model.VpcSubnet{Id: &id2, Tags: tags2})
		return a
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
		return nil
	})
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)

	r := &SubnetReconciler{
		Client:        k8sClient,
		Scheme:        nil,
		SubnetService: service,
	}
	ctx := context.Background()
	srList := &v1alpha1.SubnetList{}
	k8sClient.EXPECT().List(ctx, srList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.SubnetList)
		a.Items = append(a.Items, v1alpha1.Subnet{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "1234"
		return nil
	})

	r.collectGarbage(ctx)

	// local store has same item as k8s cache
	patch.Reset()
	patch.ApplyMethod(reflect.TypeOf(service), "ListSubnetCreatedBySubnet", func(_ *subnet.SubnetService, uid string) []*model.VpcSubnet {
		tags := []model.Tag{{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("1234")}}
		var a []*model.VpcSubnet
		id := "1234"
		a = append(a, &model.VpcSubnet{Id: &id, Tags: tags})
		return a
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteSubnet", func(_ *subnet.SubnetService, subnet model.VpcSubnet) error {
		assert.FailNow(t, "should not be called")
		return nil
	})
	k8sClient.EXPECT().List(gomock.Any(), srList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.SubnetList)
		a.Items = append(a.Items, v1alpha1.Subnet{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "1234"
		return nil
	})
	r.collectGarbage(ctx)

	// local store has no item
	patch.Reset()
	patch.ApplyMethod(reflect.TypeOf(service), "ListSubnetCreatedBySubnet", func(_ *subnet.SubnetService, uid string) []*model.VpcSubnet {
		return []*model.VpcSubnet{}
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

func createFakeSubnetReconciler() *SubnetReconciler {
	service := &vpc.VPCService{
		Service: common.Service{
			Client:    nil,
			NSXClient: &nsx.Client{},
		},
	}
	subnetService := &subnet.SubnetService{
		Service: common.Service{
			Client:    nil,
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

	return &SubnetReconciler{
		Client:            fake.NewClientBuilder().Build(),
		Scheme:            fake.NewClientBuilder().Build().Scheme(),
		VPCService:        service,
		SubnetService:     subnetService,
		SubnetPortService: subnetPortService,
		Recorder:          &fakeRecorder{},
	}
}

func TestSubnetReconciler_Reconcile(t *testing.T) {
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "test-subnet",
		},
	}

	createNewSubnet := func() *v1alpha1.Subnet {
		return &v1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-subnet",
				Namespace: "default",
				UID:       "fake-subnet-uid",
			},
			Spec: v1alpha1.SubnetSpec{
				IPv4SubnetSize: 0,
				AccessMode:     "",
			},
		}
	}

	reconciler := createFakeSubnetReconciler()
	ctx := context.Background()

	// When the Subnet CR does not exist
	t.Run("Subnet CR not found", func(t *testing.T) {
		v1alpha1.AddToScheme(reconciler.Scheme)
		patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(reconciler), "deleteSubnetByName", func(_ *SubnetReconciler, name, ns string) error {
			return nil
		})
		defer patches.Reset()

		result, err := reconciler.Reconcile(ctx, req)

		assert.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
		patches.Reset()
	})

	t.Run("Get Subnet CR return other error should retry", func(t *testing.T) {
		v1alpha1.AddToScheme(reconciler.Scheme)
		patches := gomonkey.ApplyMethod(reflect.TypeOf(reconciler.Client), "Get", func(_ client.Client, _ context.Context, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
			return errors.New("get Subnet CR error")
		})
		defer patches.Reset()
		patches.ApplyPrivateMethod(reflect.TypeOf(reconciler), "deleteSubnetByName", func(_ *SubnetReconciler, name, ns string) error {
			return nil
		})

		result, err := reconciler.Reconcile(ctx, req)

		assert.ErrorContains(t, err, "get Subnet CR error")
		assert.Equal(t, ResultRequeue, result)
	})

	// When the Subnet CR is being deleted should delete the Subnet and return no error
	t.Run("Subnet CR being deleted", func(t *testing.T) {
		t.Log("When the Subnet CR is being deleted should delete the Subnet and return no error")
		v1alpha1.AddToScheme(reconciler.Scheme)
		subnetCR := createNewSubnet()
		now := metav1.NewTime(time.Now())
		subnetCR.DeletionTimestamp = &now

		createErr := reconciler.Client.Create(ctx, subnetCR)
		defer reconciler.Client.Delete(ctx, subnetCR)
		assert.NoError(t, createErr)

		patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(reconciler), "deleteSubnetByID", func(_ *SubnetReconciler, _ string) error {
			return nil
		})
		defer patches.Reset()
		patches.ApplyMethod(reflect.TypeOf(reconciler.Client), "Delete", func(_ client.Client, _ context.Context, _ client.Object, _ ...client.DeleteOption) error {
			return nil
		})

		result, err := reconciler.Reconcile(ctx, req)

		assert.ErrorContains(t, err, "VPCNetworkConfig not found")
		assert.Equal(t, ResultRequeue, result)
	})

	// When an error occurs during reconciliation, should return an error and requeue"
	t.Run("Create or Update Subnet Failure", func(t *testing.T) {
		v1alpha1.AddToScheme(reconciler.Scheme)

		subnetCR := createNewSubnet()
		createErr := reconciler.Client.Create(ctx, subnetCR)
		assert.NoError(t, createErr)
		defer reconciler.Client.Delete(ctx, subnetCR)

		vpcConfig := &common.VPCNetworkConfigInfo{DefaultSubnetSize: 16}
		patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(reconciler.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) *common.VPCNetworkConfigInfo {
			return vpcConfig
		})
		defer patches.Reset()

		tags := []model.Tag{{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-tag")}}
		patches.ApplyMethod(reflect.TypeOf(reconciler.SubnetService), "GenerateSubnetNSTags", func(_ *subnet.SubnetService, obj client.Object) []model.Tag {
			return tags
		})

		patches.ApplyMethod(reflect.TypeOf(reconciler.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, ns string) []common.VPCResourceInfo {
			return []common.VPCResourceInfo{
				{OrgID: "org-id", ProjectID: "project-id", VPCID: "vpc-id", ID: "fake-id"},
			}
		})

		patches.ApplyMethod(reflect.TypeOf(reconciler.SubnetService), "CreateOrUpdateSubnet", func(_ *subnet.SubnetService, obj client.Object, vpcInfo common.VPCResourceInfo, tags []model.Tag) (string, error) {
			return "", errors.New("create or update failed")
		})

		result, err := reconciler.Reconcile(ctx, req)

		assert.Error(t, err)
		assert.Equal(t, ctrl.Result{Requeue: true}, result)
	})

	// When updating the Subnet spec, should update the Subnet CR spec if not set
	t.Run("Update Subnet CR spec", func(t *testing.T) {
		v1alpha1.AddToScheme(reconciler.Scheme)

		subnetCR := createNewSubnet()
		createErr := reconciler.Client.Create(ctx, subnetCR)
		assert.NoError(t, createErr)
		defer reconciler.Client.Delete(ctx, subnetCR)

		vpcConfig := &common.VPCNetworkConfigInfo{DefaultSubnetSize: 16}
		patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(reconciler.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, ns string) *common.VPCNetworkConfigInfo {
			return vpcConfig
		})
		defer patches.Reset()

		tags := []model.Tag{{Scope: common.String(common.TagScopeSubnetCRUID), Tag: common.String("fake-tag")}}
		patches.ApplyMethod(reflect.TypeOf(reconciler.SubnetService), "GenerateSubnetNSTags", func(_ *subnet.SubnetService, obj client.Object) []model.Tag {
			return tags
		})

		patches.ApplyMethod(reflect.TypeOf(reconciler.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, ns string) []common.VPCResourceInfo {
			return []common.VPCResourceInfo{
				{OrgID: "org-id", ProjectID: "project-id", VPCID: "vpc-id", ID: "fake-id"},
			}
		})

		patches.ApplyMethod(reflect.TypeOf(reconciler.SubnetService), "CreateOrUpdateSubnet", func(_ *subnet.SubnetService, obj client.Object, vpcInfo common.VPCResourceInfo, tags []model.Tag) (string, error) {
			return "", nil
		})

		patches.ApplyPrivateMethod(reflect.TypeOf(reconciler), "updateSubnetStatus", func(_ *SubnetReconciler, obj *v1alpha1.Subnet) error {
			return nil
		})

		result, err := reconciler.Reconcile(ctx, req)

		assert.NoError(t, err)
		assert.NoError(t, reconciler.Client.Get(ctx, req.NamespacedName, subnetCR))
		assert.Equal(t, 16, subnetCR.Spec.IPv4SubnetSize)
		assert.Equal(t, ctrl.Result{}, result)
	})
}
