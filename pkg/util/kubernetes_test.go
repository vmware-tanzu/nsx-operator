package util

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestGetConfig(t *testing.T) {
	patches := gomonkey.ApplyFunc(ctrl.GetConfigOrDie, func() *rest.Config {
		return &rest.Config{
			Host: "https://10.0.0.1:443",
		}
	})
	defer patches.Reset()

	tests := []struct {
		name         string
		preparedFunc func() *gomonkey.Patches
		expectedHost string
		expectedErr  string
	}{
		{
			name: "healthyTraffic",
			preparedFunc: func() *gomonkey.Patches {
				return gomonkey.ApplyFunc((*http.Client).Get, func(c *http.Client, url string) (resp *http.Response, err error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(bytes.NewBufferString(`{"ok": true}`)),
					}, nil
				})
			},
			expectedHost: "https://10.0.0.1:443",
		},
		{
			name: "unhealthyTrafficAndIPv4",
			preparedFunc: func() *gomonkey.Patches {
				return gomonkey.ApplyFunc((*http.Client).Get, func(c *http.Client, url string) (resp *http.Response, err error) {
					return &http.Response{
						StatusCode: http.StatusNotFound,
						Body:       io.NopCloser(bytes.NewBufferString(`{"ok": false}`)),
					}, nil
				})
			},
			expectedErr: "Kubernetes API Server is unhealthy",
		},
		{
			name: "errorTrafficAndIPv4",
			preparedFunc: func() *gomonkey.Patches {
				patches.ApplyFunc((*http.Client).Get, func(c *http.Client, url string) (resp *http.Response, err error) {
					return nil, errors.New("mock get failure")
				})
				return patches
			},
			expectedHost: "https://127.0.0.1:6443",
		},
		{
			name: "errorTrafficAndIPv6",
			preparedFunc: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(ctrl.GetConfigOrDie, func() *rest.Config {
					return &rest.Config{
						Host: "https://aa:bb:cc:dd:ee:ff:443",
					}
				})
				patches.ApplyFunc((*http.Client).Get, func(c *http.Client, url string) (resp *http.Response, err error) {
					return nil, errors.New("mock get failure")
				})
				return patches
			},
			expectedHost: "https://[::1]:6443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.preparedFunc()
			defer patches.Reset()
			cfg, err := GetConfig()
			if tt.expectedErr != "" {
				assert.ErrorContains(t, err, tt.expectedErr)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, tt.expectedHost, cfg.Host)
			}
		})
	}
}
