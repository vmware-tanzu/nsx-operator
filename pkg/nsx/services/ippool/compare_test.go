package ippool

import (
	"reflect"
	"testing"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func TestComparableToIpAddressPool(t *testing.T) {
	type args struct {
		iap Comparable
	}
	tests := []struct {
		name string
		args args
		want *model.IpAddressPool
	}{
		{"1", args{&IpAddressPool{Id: String("1")}}, &model.IpAddressPool{Id: String("1")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ComparableToIpAddressPool(tt.args.iap); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ComparableToIpAddressPool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestComparableToIpAddressPoolBlockSubnet(t *testing.T) {
	type args struct {
		iapbs Comparable
	}
	tests := []struct {
		name string
		args args
		want *model.IpAddressPoolBlockSubnet
	}{
		{"1", args{&IpAddressPoolBlockSubnet{Id: String("1")}}, &model.IpAddressPoolBlockSubnet{Id: String("1")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ComparableToIpAddressPoolBlockSubnet(tt.args.iapbs); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ComparableToIpAddressPoolBlockSubnet() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestComparableToIpAddressPoolBlockSubnets(t *testing.T) {
	type args struct {
		iapbs []Comparable
	}
	tests := []struct {
		name string
		args args
		want []*model.IpAddressPoolBlockSubnet
	}{
		{"1", args{[]Comparable{&IpAddressPoolBlockSubnet{Id: String("1")}}}, []*model.IpAddressPoolBlockSubnet{{Id: String("1")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ComparableToIpAddressPoolBlockSubnets(tt.args.iapbs); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ComparableToIpAddressPoolBlockSubnets() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIpAddressPoolBlockSubnet_Key(t *testing.T) {
	tests := []struct {
		name  string
		iapbs IpAddressPoolBlockSubnet
		want  string
	}{
		{"1", IpAddressPoolBlockSubnet{Id: String("1")}, "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.iapbs.Key(); got != tt.want {
				t.Errorf("Key() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIpAddressPoolBlockSubnet_Value(t *testing.T) {
	m := model.IpAddressPoolBlockSubnet{Id: String("1"), DisplayName: String("1"), Tags: []model.Tag{{Scope: String("1"), Tag: String("1")}}}
	v, _ := m.GetDataValue__()
	p := IpAddressPoolBlockSubnet{Id: String("1"), DisplayName: String("1"), Tags: []model.Tag{{Scope: String("1"), Tag: String("1")}}}
	tests := []struct {
		name  string
		iapbs IpAddressPoolBlockSubnet
		want  data.DataValue
	}{
		{"1", p, v},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.iapbs.Value(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Value() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIpAddressPoolBlockSubnetsToComparable(t *testing.T) {
	type args struct {
		iapbs []*model.IpAddressPoolBlockSubnet
	}
	tests := []struct {
		name string
		args args
		want []Comparable
	}{
		{"1", args{[]*model.IpAddressPoolBlockSubnet{{Id: String("1")}}}, []Comparable{&IpAddressPoolBlockSubnet{Id: String("1")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IpAddressPoolBlockSubnetsToComparable(tt.args.iapbs); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("IpAddressPoolBlockSubnetsToComparable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIpAddressPoolToComparable(t *testing.T) {
	type args struct {
		iap *model.IpAddressPool
	}
	tests := []struct {
		name string
		args args
		want Comparable
	}{
		{"1", args{&model.IpAddressPool{Id: String("1")}}, &IpAddressPool{Id: String("1")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IpAddressPoolToComparable(tt.args.iap); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("IpAddressPoolToComparable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIpAddressPool_Key(t *testing.T) {
	tests := []struct {
		name string
		iap  IpAddressPool
		want string
	}{
		{"1", IpAddressPool{Id: String("1")}, "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.iap.Key(); got != tt.want {
				t.Errorf("Key() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIpAddressPool_Value(t *testing.T) {
	m := model.IpAddressPool{Id: String("1"), DisplayName: String("1"), Tags: []model.Tag{{Scope: String("1"), Tag: String("1")}}}
	v, _ := m.GetDataValue__()
	p := IpAddressPool{Id: String("1"), DisplayName: String("1"), Tags: []model.Tag{{Scope: String("1"), Tag: String("1")}}}
	tests := []struct {
		name string
		iap  IpAddressPool
		want data.DataValue
	}{
		{"1", p, v},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.iap.Value(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Value() = %v, want %v", got, tt.want)
			}
		})
	}
}
