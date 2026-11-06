package common

import (
	"errors"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	mockmanager "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/manager"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func TestSetupWebhookServer(t *testing.T) {
	for _, tc := range []struct {
		name    string
		vpc     bool
		certErr error
		addErr  error
		skip    bool
	}{
		{name: "VPC", vpc: true},
		{name: "T1 skips webhook setup", skip: true},
		{name: "VPC certificate failure", vpc: true, certErr: errors.New("certificate generation failed")},
		{name: "manager registration failure", vpc: true, addErr: errors.New("manager stopped")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mgr := mockmanager.NewMockManager(ctrl)
			certCalls := 0
			patches := gomonkey.ApplyFunc(util.GenerateWebhookCerts, func() error { certCalls++; return tc.certErr })
			defer patches.Reset()
			var registered webhook.Server
			if tc.vpc && tc.certErr == nil {
				mgr.EXPECT().GetScheme().Return(runtime.NewScheme())
				mgr.EXPECT().Add(gomock.Any()).DoAndReturn(func(r manager.Runnable) error {
					registered = r.(webhook.Server)
					return tc.addErr
				})
			}
			server, err := SetupWebhookServer(mgr, tc.vpc)
			if tc.skip {
				require.NoError(t, err)
				assert.Nil(t, server)
				assert.Zero(t, certCalls)
				return
			}
			if tc.certErr != nil || tc.addErr != nil {
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
