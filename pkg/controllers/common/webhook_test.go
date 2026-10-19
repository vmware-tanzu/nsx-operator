package common

import (
	"errors"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	mockclient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	mockmanager "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/manager"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func TestSetupWebhookServer(t *testing.T) {
	resource := schema.GroupResource{Group: "admissionregistration.k8s.io", Resource: "validatingwebhookconfigurations"}
	for _, tc := range []struct {
		name     string
		required bool
		readErr  error
		certErr  error
		addErr   error
		skip     bool
	}{
		{name: "VPC", required: true},
		{name: "T1 with webhook installed"},
		{name: "legacy T1 without webhook", readErr: apierrors.NewNotFound(resource, "nsx-operator-validating-webhook-configuration"), skip: true},
		{name: "legacy T1 without webhook RBAC", readErr: apierrors.NewForbidden(resource, "nsx-operator-validating-webhook-configuration", errors.New("denied")), skip: true},
		{name: "T1 API server unavailable", readErr: errors.New("connection timeout")},
		{name: "T1 certificate failure", certErr: errors.New("certificate generation failed")},
		{name: "VPC certificate failure", required: true, certErr: errors.New("certificate generation failed")},
		{name: "manager registration failure", required: true, addErr: errors.New("manager stopped")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mgr := mockmanager.NewMockManager(ctrl)
			if !tc.required {
				reader := mockclient.NewMockClient(ctrl)
				mgr.EXPECT().GetAPIReader().Return(reader)
				reader.EXPECT().Get(gomock.Any(), client.ObjectKey{Name: "nsx-operator-validating-webhook-configuration"}, gomock.Any()).Return(tc.readErr)
			}
			certCalls := 0
			patches := gomonkey.ApplyFunc(util.GenerateWebhookCerts, func() error { certCalls++; return tc.certErr })
			defer patches.Reset()
			var registered webhook.Server
			if tc.readErr == nil && tc.certErr == nil {
				mgr.EXPECT().GetScheme().Return(runtime.NewScheme())
				mgr.EXPECT().Add(gomock.Any()).DoAndReturn(func(r manager.Runnable) error {
					registered = r.(webhook.Server)
					return tc.addErr
				})
			}
			server, err := SetupWebhookServer(mgr, tc.required)
			if tc.skip {
				require.NoError(t, err)
				assert.Nil(t, server)
				assert.Zero(t, certCalls)
				return
			}
			if tc.readErr != nil || tc.certErr != nil || tc.addErr != nil {
				require.Error(t, err)
				assert.Nil(t, server)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, server)
			assert.Same(t, registered, server)
			assert.Equal(t, 1, certCalls)
			assertNCPConfigWebhookDeniesUntrustedWrite(t, server)
		})
	}
}
