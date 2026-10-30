/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package migration

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/controllers"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
)

func TestRegisterMigrationControllers(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		scheme := runtime.NewScheme()
		require.NoError(t, api.AddToScheme(scheme))
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		reader := fake.NewClientBuilder().WithScheme(scheme).Build()
		engine := &transition.Engine{Enabled: enabled}
		var registered []string
		require.NoError(t, registerControllers(c, reader, engine, func(r *controllers.Reconciler) error {
			require.Same(t, c, r.Client)
			require.Same(t, reader, r.APIReader)
			require.Same(t, engine, r.Engine)
			require.Equal(t, enabled, r.Engine.Enabled)
			switch r.Stage {
			case transition.Infrastructure:
				registered = append(registered, "controlplane")
			case transition.Namespaces:
				registered = append(registered, "namespace")
			default:
				t.Fatalf("unexpected stage: %s", r.Stage)
			}
			return nil
		}))
		require.Equal(t, []string{"controlplane", "namespace"}, registered)
		failure := errors.New("controller registration failed")
		calls := 0
		err := registerControllers(c, reader, engine, func(*controllers.Reconciler) error { calls++; return failure })
		require.ErrorIs(t, err, failure)
		require.Equal(t, 1, calls, "do not continue after failed registration")
	}
}
