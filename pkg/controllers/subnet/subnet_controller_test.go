package subnet

import (
	"context"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
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

	r.CollectGarbage(ctx)

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
	r.CollectGarbage(ctx)

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
	r.CollectGarbage(ctx)
	patch.Reset()
}
