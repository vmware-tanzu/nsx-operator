package clean

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

var (
	cf = &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{NsxApiManagers: []string{"10.0.0.1"}}}
)

func TestClean_ValidationFailed(t *testing.T) {
	ctx := context.Background()
	log := logr.Discard()
	debug := false
	logLevel := 0
	patches := gomonkey.ApplyMethod(reflect.TypeOf(cf.NsxConfig), "ValidateConfigFromCmd", func(_ *config.NsxConfig) error {
		return errors.New("validation failed")
	})

	defer patches.Reset()

	err := Clean(ctx, cf, &log, debug, logLevel)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")
}

func TestClean_GetClientFailed(t *testing.T) {
	ctx := context.Background()

	log := logr.Discard()
	debug := false
	logLevel := 0

	patches := gomonkey.ApplyMethod(reflect.TypeOf(cf.NsxConfig), "ValidateConfigFromCmd", func(_ *config.NsxConfig) error {
		return nil
	})
	defer patches.Reset()
	patches.ApplyFunc(nsx.GetClient, func(_ *config.NSXOperatorConfig) *nsx.Client {
		return nil
	})

	err := Clean(ctx, cf, &log, debug, logLevel)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get nsx client")
}

func TestClean_InitError(t *testing.T) {
	ctx := context.Background()

	log := logr.Discard()
	debug := false
	logLevel := 0

	patches := gomonkey.ApplyMethod(reflect.TypeOf(cf.NsxConfig), "ValidateConfigFromCmd", func(_ *config.NsxConfig) error {
		return nil
	})
	defer patches.Reset()
	patches.ApplyFunc(nsx.GetClient, func(_ *config.NSXOperatorConfig) *nsx.Client {
		return &nsx.Client{}
	})

	patches.ApplyFunc(InitializeCleanupService, func(_ *config.NSXOperatorConfig, _ *nsx.Client) (*CleanupService, error) {
		return nil, errors.New("init cleanup service failed")
	})

	err := Clean(ctx, cf, &log, debug, logLevel)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "init cleanup service failed")
}

func TestClean_CleanupSucc(t *testing.T) {
	ctx := context.Background()

	log := logr.Discard()
	debug := false
	logLevel := 0

	patches := gomonkey.ApplyMethod(reflect.TypeOf(cf.NsxConfig), "ValidateConfigFromCmd", func(_ *config.NsxConfig) error {
		return nil
	})
	defer patches.Reset()
	patches.ApplyFunc(nsx.GetClient, func(_ *config.NSXOperatorConfig) *nsx.Client {
		return &nsx.Client{}
	})

	cleanupService := &CleanupService{}
	clean := &MockCleanup{
		CleanupFunc: func(ctx context.Context) error {
			return nil
		},
	}
	cleanupService.cleans = append(cleanupService.cleans, clean)
	patches.ApplyFunc(InitializeCleanupService, func(_ *config.NSXOperatorConfig, _ *nsx.Client) (*CleanupService, error) {
		return cleanupService, nil
	})

	patches.ApplyFunc(CleanDLB, func(ctx context.Context, cluster *nsx.Cluster, cf *config.NSXOperatorConfig, log *logr.Logger) error {
		return nil
	})
	err := Clean(ctx, cf, &log, debug, logLevel)
	assert.Nil(t, err)
}

type MockCleanup struct {
	CleanupFunc func(ctx context.Context) error
}

func (m *MockCleanup) Cleanup(ctx context.Context) error {
	return m.CleanupFunc(ctx)
}

func TestWrapCleanFunc(t *testing.T) {
	// succ case
	ctx := context.Background()
	clean := &MockCleanup{
		CleanupFunc: func(ctx context.Context) error {
			return nil
		},
	}

	wrappedFunc := wrapCleanFunc(ctx, clean)
	err := wrappedFunc()
	assert.NoError(t, err)

	// error case
	clean = &MockCleanup{
		CleanupFunc: func(ctx context.Context) error {
			return errors.New("cleanup failed")
		},
	}

	wrappedFunc = wrapCleanFunc(ctx, clean)
	err = wrappedFunc()
	assert.Error(t, err)
	assert.Equal(t, "cleanup failed", err.Error())

}
