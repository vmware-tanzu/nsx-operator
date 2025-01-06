package common

import (
	"github.com/stretchr/testify/assert"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuilder(t *testing.T) {
	builder, err := PolicyPathVpcSubnetConnectionBindingMap.NewPolicyTreeBuilder()
	require.NoError(t, err)

	assert.Equal(t, ResourceTypeOrgRoot, builder.rootType)
}
