package vpc

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey"
	"github.com/stretchr/testify/assert"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func Test_httpGetAviPortsPaths(t *testing.T) {
	cluster := &nsx.Cluster{}
	testCases := []struct {
		name          string
		prepareFunc   func() *gomonkey.Patches
		expectErrStr  string
		expectPathLen int
		expectPaths   []string
	}{
		{
			name: "GetAviPortsPaths success",
			prepareFunc: func() *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(cluster), "HttpGet", func(c *nsx.Cluster, url string) (map[string]interface{}, error) {
					return mapInterface{
						"results": []interface{}{
							mapInterface{"path": "/some/path1"},
							mapInterface{"path": "/some/path2"},
							"invalid path",
						},
					}, nil
				})
			},
			expectPathLen: 2,
			expectPaths:   []string{"/some/path1", "/some/path2"},
		},
		{
			name: "GetAviPortsPaths success with unsupported type",
			prepareFunc: func() *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(cluster), "HttpGet", func(c *nsx.Cluster, url string) (map[string]interface{}, error) {
					return mapInterface{
						"results1": []interface{}{
							mapInterface{"path": "/some/path1"},
						},
					}, nil
				})
			},
			expectErrStr: "unexpected type",
		},
		{
			name: "GetAviPortsPaths error",
			prepareFunc: func() *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(cluster), "HttpGet", func(c *nsx.Cluster, url string) (map[string]interface{}, error) {
					return nil, fmt.Errorf("network error")
				})
			},
			expectErrStr: "network error",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.prepareFunc != nil {
				patches := tc.prepareFunc()
				defer patches.Reset()
			}

			vpcPath := "/some/vpcpath"
			paths, err := httpGetAviPortsPaths(cluster, vpcPath)
			if tc.expectErrStr != "" {
				assert.ErrorContains(t, err, tc.expectErrStr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.expectPathLen, paths.Len())
			for _, path := range tc.expectPaths {
				assert.True(t, paths.Has(path))
			}
		})
	}
}

func TestCleanAviSubnetPorts(t *testing.T) {
	cluster := &nsx.Cluster{}
	testCases := []struct {
		name         string
		prepareFunc  func(t *testing.T) *gomonkey.Patches
		expectErrStr string
	}{
		{
			name: "CleanAviSubnetPorts success",
			prepareFunc: func(t *testing.T) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(cluster), "HttpGet", func(c *nsx.Cluster, url string) (map[string]interface{}, error) {
					return mapInterface{
						"results": []interface{}{
							mapInterface{"path": "/some/path1"},
							mapInterface{"path": "/some/path2"},
						},
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(cluster), "HttpDelete", func(c *nsx.Cluster, url string) error {
					assert.Contains(t, []string{"policy/api/v1/some/path1", "policy/api/v1/some/path2"}, url)
					return nil
				})
				return patches
			},
		},
		{
			name: "CleanAviSubnetPorts HttpGetErrorNotFound",
			prepareFunc: func(t *testing.T) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(cluster), "HttpGet", func(c *nsx.Cluster, url string) (map[string]interface{}, error) {
					return nil, nsxutil.HttpNotFoundError
				})
				return patches
			},
		},
		{
			name: "CleanAviSubnetPorts StatusBadRequest error",
			prepareFunc: func(t *testing.T) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(cluster), "HttpGet", func(c *nsx.Cluster, url string) (map[string]interface{}, error) {
					return nil, nsxutil.HttpBadRequest
				})
				return patches
			},
		},
		{
			name: "CleanAviSubnetPorts unexpected error",
			prepareFunc: func(t *testing.T) *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(cluster), "HttpGet", func(c *nsx.Cluster, url string) (map[string]interface{}, error) {
					return nil, fmt.Errorf("unexpected error")
				})
			},
			expectErrStr: "error getting Avi Subnet ports",
		},
		{
			name: "CleanAviSubnetPorts Delete error",
			prepareFunc: func(t *testing.T) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(cluster), "HttpGet", func(c *nsx.Cluster, url string) (map[string]interface{}, error) {
					return mapInterface{
						"results": []interface{}{
							mapInterface{"path": "/some/path1"},
							mapInterface{"path": "/some/path2"},
						},
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(cluster), "HttpDelete", func(c *nsx.Cluster, url string) error {
					return fmt.Errorf("delete error")
				})
				return patches
			},
			expectErrStr: "failed to delete Avi Subnet port",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.prepareFunc != nil {
				patches := tc.prepareFunc(t)
				defer patches.Reset()
			}

			vpcPath := "/some/vpcpath"
			err := CleanAviSubnetPorts(ctx, cluster, vpcPath)
			if tc.expectErrStr != "" {
				assert.ErrorContains(t, err, tc.expectErrStr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func Test_CleanAviSubnetPorts_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancel the context

	cluster := &nsx.Cluster{}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(cluster), "HttpGet", func(c *nsx.Cluster, url string) (map[string]interface{}, error) {
		return mapInterface{
			"results": []interface{}{
				mapInterface{"path": "/some/path1"},
				mapInterface{"path": "/some/path2"},
			},
		}, nil
	})
	patches.ApplyMethod(reflect.TypeOf(cluster), "HttpDelete", func(c *nsx.Cluster, url string) error {
		return nil
	})
	defer patches.Reset()

	err := CleanAviSubnetPorts(ctx, cluster, "/vpcpath")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed because of timeout")
}
