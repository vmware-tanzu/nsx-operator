package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExcept(t *testing.T) {
	notFound := CreateResourceNotFound("192.168.1.1", "ippool")
	assert.True(t, 0 == notFound.ErrorCode)
	// notFound should be a ManagerError
	var managerErr ManagerError = *notFound
	assert.True(t, managerErr != nil)
	managerErr.managerError()
}
