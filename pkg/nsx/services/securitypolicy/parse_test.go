package securitypolicy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetCluster(t *testing.T) {
	assert.Equal(t, "k8scl-one", getCluster(service))
}
