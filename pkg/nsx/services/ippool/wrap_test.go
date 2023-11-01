package ippool

import (
	"testing"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestIPPoolService_WrapHierarchyIPPool(t *testing.T) {
	Converter := bindings.NewTypeConverter()
	Converter.SetMode(bindings.REST)
	service := fakeService()
	iapbs := []*model.IpAddressPoolBlockSubnet{
		&model.IpAddressPoolBlockSubnet{
			Id: String("1"), DisplayName: String("1"),
			Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID), Tag: String("1")}}}}
	iap := &model.IpAddressPool{Id: String("1"), DisplayName: String("1"),
		Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID),
			Tag: String("1")}}}

	tests := []struct {
		name    string
		want    []*data.StructValue
		wantErr bool
	}{
		{"1", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.WrapHierarchyIPPool(iap, iapbs)
			if (err != nil) != tt.wantErr {
				t.Errorf("WrapHierarchyIPPool() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}
