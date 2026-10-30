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
)

func TestRegisterMigrationControllers(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	var registered []string
	require.NoError(t, registerControllers(c, func(r *controllers.TransitionReconciler) error {
		require.Same(t, c, r.Client)
		switch r.Object.(type) {
		case *api.ControlPlaneNetworkTransition:
			registered = append(registered, "controlplane")
		case *api.NamespaceNetworkTransition:
			registered = append(registered, "namespace")
		default:
			t.Fatalf("unexpected controller: %T", r.Object)
		}
		return nil
	}))
	require.Equal(t, []string{"controlplane", "namespace"}, registered)
	failure := errors.New("controller registration failed")
	calls := 0
	err := registerControllers(c, func(*controllers.TransitionReconciler) error { calls++; return failure })
	require.ErrorIs(t, err, failure)
	require.Equal(t, 1, calls, "do not continue after failed registration")
}
