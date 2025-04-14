package clean

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func mockCleanupFunc() (interface{}, error) {
	return &MockCleanup{}, nil
}

func mockCleanupFuncWithError() (interface{}, error) {
	return nil, errors.New("mock error")
}

func TestNewCleanupService(t *testing.T) {
	service := NewCleanupService()
	assert.NotNil(t, service)
	assert.Nil(t, service.svcErr)
}

func TestAddCleanupService_Success(t *testing.T) {
	service := NewCleanupService()
	service.AddCleanupService(mockCleanupFunc)

	assert.Nil(t, service.svcErr)
	assert.Len(t, service.vpcPreCleaners, 1)
	assert.Len(t, service.vpcChildrenCleaners, 1)
	assert.Len(t, service.infraCleaners, 1)
}

func TestAddCleanupService_Error(t *testing.T) {
	service := NewCleanupService()
	service.AddCleanupService(mockCleanupFuncWithError)

	assert.NotNil(t, service.svcErr)
	assert.Len(t, service.vpcPreCleaners, 0)
	assert.Len(t, service.vpcChildrenCleaners, 0)
	assert.Len(t, service.infraCleaners, 0)
}
