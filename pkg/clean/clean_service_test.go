package clean

import (
	"context"
	"errors"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"

	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
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

func TestCleanupService_Retriable(t *testing.T) {
	log := logr.Discard()
	service := &CleanupService{log: &log}

	// nil error should return false
	assert.False(t, service.retriable(nil))

	// standard error should return true
	assert.True(t, service.retriable(errors.New("some error")))

	// context.Canceled should return false
	assert.False(t, service.retriable(context.Canceled))

	// context.DeadlineExceeded should return false
	assert.False(t, service.retriable(context.DeadlineExceeded))

	// TimeoutFailed should return false
	assert.False(t, service.retriable(nsxutil.TimeoutFailed))
}
