package clean

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/go-logr/logr"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

func TestHttpQueryDLBResources_Success(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	cluster := &nsx.Cluster{}
	cf = &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{NsxApiManagers: []string{"10.0.0.1"}}, CoeConfig: &config.CoeConfig{Cluster: "test-cluster"}}
	resource := "Group"
	/*
		expectedURL := "policy/api/v1/search/query?query=resource_type%3AGroup%20AND%20tags.scope%3Ancp%5C%2Fcluster%20AND%20tags.tag%3Atest-cluster%20AND%20tags.scope%3Ancp%5C%2Fcreated_for%20AND%20tags.tag%3ADLB"
	*/
	expectedResponse := map[string]interface{}{
		"results": []interface{}{
			map[string]interface{}{"path": "/test/path/1"},
			map[string]interface{}{"path": "/test/path/2"},
		},
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(cluster), "HttpGet", func(cluster *nsx.Cluster, url string) (map[string]interface{}, error) {
		return expectedResponse, nil
	})
	defer patches.Reset()

	paths, err := httpQueryDLBResources(cluster, cf, resource)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"/test/path/1", "/test/path/2"}, paths)
}

func TestHttpQueryDLBResources_Error(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	cluster := &nsx.Cluster{}
	cf = &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{NsxApiManagers: []string{"10.0.0.1"}}, CoeConfig: &config.CoeConfig{Cluster: "test-cluster"}}
	resource := "Group"
	/*
		expectedURL := "policy/api/v1/search/query?query=resource_type%3AGroup%20AND%20tags.scope%3Ancp%5C%2Fcluster%20AND%20tags.tag%3Atest-cluster%20AND%20tags.scope%3Ancp%5C%2Fcreated_for%20AND%20tags.tag%3ADLB"
	*/
	expectedError := errors.New("http error")

	patches := gomonkey.ApplyMethod(reflect.TypeOf(cluster), "HttpGet", func(cluster *nsx.Cluster, url string) (map[string]interface{}, error) {
		return nil, expectedError
	})
	defer patches.Reset()

	paths, err := httpQueryDLBResources(cluster, cf, resource)
	assert.Error(t, err)
	assert.Nil(t, paths)
}

func TestHttpQueryDLBResources_EmptyResponse(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	cf = &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{NsxApiManagers: []string{"10.0.0.1"}}, CoeConfig: &config.CoeConfig{Cluster: "test-cluster"}}
	resource := "Group"
	cluster := &nsx.Cluster{}

	expectedResponse := map[string]interface{}{
		"results": []interface{}{},
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(cluster), "HttpGet", func(cluster *nsx.Cluster, url string) (map[string]interface{}, error) {
		return expectedResponse, nil
	})
	defer patches.Reset()

	paths, err := httpQueryDLBResources(cluster, cf, resource)
	assert.NoError(t, err)
	assert.Empty(t, paths)
}

func TestCleanDLB_Success(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	cluster := &nsx.Cluster{}
	cf = &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{NsxApiManagers: []string{"10.0.0.1"}}, CoeConfig: &config.CoeConfig{Cluster: "test-cluster"}}
	log := logr.Discard()

	expectedPaths := []string{"/test/path/1", "/test/path/2"}
	patches := gomonkey.ApplyFunc(httpQueryDLBResources, func(cluster *nsx.Cluster, cf *config.NSXOperatorConfig, resource string) ([]string, error) {
		return expectedPaths, nil
	})
	defer patches.Reset()

	patches.ApplyMethod(reflect.TypeOf(cluster), "HttpDelete", func(cluster *nsx.Cluster, url string) error {
		return nil
	})

	ctx := context.Background()
	err := CleanDLB(ctx, cluster, cf, &log)
	assert.NoError(t, err)
}

func TestCleanDLB_HttpQueryError(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	cluster := &nsx.Cluster{}
	cf = &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{NsxApiManagers: []string{"10.0.0.1"}}, CoeConfig: &config.CoeConfig{Cluster: "test-cluster"}}
	log := logr.Discard()

	expectedError := errors.New("http query error")
	patches := gomonkey.ApplyFunc(httpQueryDLBResources, func(cluster *nsx.Cluster, cf *config.NSXOperatorConfig, resource string) ([]string, error) {
		return nil, expectedError
	})
	defer patches.Reset()

	ctx := context.Background()
	err := CleanDLB(ctx, cluster, cf, &log)
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
}

func TestCleanDLB_HttpDeleteError(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	cluster := &nsx.Cluster{}
	cf = &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{NsxApiManagers: []string{"10.0.0.1"}}, CoeConfig: &config.CoeConfig{Cluster: "test-cluster"}}
	log := logr.Discard()

	expectedPaths := []string{"/test/path/1", "/test/path/2"}
	patches := gomonkey.ApplyFunc(httpQueryDLBResources, func(cluster *nsx.Cluster, cf *config.NSXOperatorConfig, resource string) ([]string, error) {
		return expectedPaths, nil
	})
	defer patches.Reset()

	expectedError := errors.New("http delete error")
	patches.ApplyMethod(reflect.TypeOf(cluster), "HttpDelete", func(cluster *nsx.Cluster, url string) error {
		return expectedError
	})

	ctx := context.Background()
	err := CleanDLB(ctx, cluster, cf, &log)
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
}

func TestCleanDLB_ContextDone(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	cluster := &nsx.Cluster{}
	cf = &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{NsxApiManagers: []string{"10.0.0.1"}}, CoeConfig: &config.CoeConfig{Cluster: "test-cluster"}}
	log := logr.Discard()

	expectedPaths := []string{"/test/path/1", "/test/path/2"}
	patches := gomonkey.ApplyFunc(httpQueryDLBResources, func(cluster *nsx.Cluster, cf *config.NSXOperatorConfig, resource string) ([]string, error) {
		return expectedPaths, nil
	})
	defer patches.Reset()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := CleanDLB(ctx, cluster, cf, &log)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
}

func TestAppendIfNotExist_ItemExists(t *testing.T) {
	slice := []string{"test"}
	result := appendIfNotExist(slice, "test")
	assert.Equal(t, []string{"test"}, result)
}

func TestAppendIfNotExist_ItemDoesNotExist(t *testing.T) {
	slice := []string{"test1"}
	result := appendIfNotExist(slice, "test2")
	assert.Equal(t, []string{"test1", "test2"}, result)
}

func TestAppendIfNotExist_MultipleItems(t *testing.T) {
	slice := []string{"test1", "test2"}
	result := appendIfNotExist(slice, "test3")
	assert.Equal(t, []string{"test1", "test2", "test3"}, result)
}

func TestAppendIfNotExist_DuplicateItems(t *testing.T) {
	slice := []string{"test1", "test2", "test1"}
	result := appendIfNotExist(slice, "test1")
	assert.Equal(t, []string{"test1", "test2", "test1"}, result)
}

func TestAppendIfNotExist_EmptySlice(t *testing.T) {
	slice := []string{}
	result := appendIfNotExist(slice, "test")
	assert.Equal(t, []string{"test"}, result)
}
