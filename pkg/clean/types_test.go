package clean

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockCleanup struct{}

func (m *mockCleanup) Cleanup(ctx context.Context) error {
	return nil
}

func mockCleanupFunc() (cleanup, error) {
	return &mockCleanup{}, nil
}

func mockCleanupFuncWithError() (cleanup, error) {
	return nil, errors.New("mock error")
}

func TestNewCleanupService(t *testing.T) {
	service := NewCleanupService()
	assert.NotNil(t, service)
	assert.Nil(t, service.err)
	assert.Empty(t, service.cleans)
}

func TestAddCleanupService_Success(t *testing.T) {
	service := NewCleanupService()
	service.AddCleanupService(mockCleanupFunc)

	assert.Nil(t, service.err)
	assert.Len(t, service.cleans, 1)
}

func TestAddCleanupService_Error(t *testing.T) {
	service := NewCleanupService()
	service.AddCleanupService(mockCleanupFuncWithError)

	assert.NotNil(t, service.err)
	assert.Len(t, service.cleans, 0)
}
