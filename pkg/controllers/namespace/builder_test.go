package namespace

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildVPCCR(t *testing.T) {
	vpcName := "fake-vpc"
	tests := []struct {
		name    string
		ns      string
		nc      string
		vpcName *string
	}{
		{"1", "test-ns1", "test-nc1", nil},
		{"2", "test-ns2", "test-nc2", &vpcName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vpc := BuildVPCCR(tt.ns, tt.nc, tt.vpcName)
			if tt.vpcName == nil {
				assert.True(t, strings.Contains(vpc.Name, "vpc-"))
			} else {
				assert.Equal(t, *tt.vpcName, vpc.Name)
			}

			assert.Equal(t, tt.ns, vpc.Namespace)
		})
	}
}
