package ipblocksinfo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func Test_KeyFunc(t *testing.T) {
	vpcPath := "vpc-path"
	vpc := model.Vpc{Path: &vpcPath}
	vpcProfilePath := "vpc-connectivity-profile-path"
	vpcProfile := model.VpcConnectivityProfile{Path: &vpcProfilePath}
	ipBlockPath := "ip-block-path"
	ipBlock := model.IpAddressBlock{Path: &ipBlockPath}

	type args struct {
		obj interface{}
	}

	tests := []struct {
		name        string
		expectedKey string
		item        args
	}{
		{
			name:        "Vpc",
			item:        args{obj: &vpc},
			expectedKey: vpcPath,
		},
		{
			name:        "VpcConnectivityProfile",
			item:        args{obj: &vpcProfile},
			expectedKey: vpcProfilePath,
		},
		{
			name:        "IpBlock",
			item:        args{obj: &ipBlock},
			expectedKey: ipBlockPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := keyFunc(tt.item.obj)
			assert.Nil(t, err)
			if got != tt.expectedKey {
				t.Errorf("keyFunc() = %v, want %v", got, tt.expectedKey)
			}
		})
	}

}
