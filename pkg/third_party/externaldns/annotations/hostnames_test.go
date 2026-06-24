package annotations

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHostnamesFromAnnotations(t *testing.T) {
	annotations := map[string]string{
		"hostname": "a.com, b.com",
	}
	hosts := HostnamesFromAnnotations(annotations, "hostname")
	assert.Equal(t, []string{"a.com", "b.com"}, hosts)

	hosts = HostnamesFromAnnotations(nil, "hostname")
	assert.Nil(t, hosts)

	hosts = HostnamesFromAnnotations(annotations, "")
	assert.Nil(t, hosts)

	hosts = HostnamesFromAnnotations(annotations, "missing")
	assert.Nil(t, hosts)
}
