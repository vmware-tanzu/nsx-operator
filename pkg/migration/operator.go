/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package migration registers the Supervisor migration controllers.
package migration

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/controllers"
)

// SetupWithManager registers the two transition controllers. Disabled startup
// must skip this function so it creates no transition watches or caches.
func SetupWithManager(mgr ctrl.Manager) error {
	return registerControllers(mgr.GetClient(), func(r *controllers.TransitionReconciler) error { return r.SetupWithManager(mgr) })
}

func registerControllers(c client.Client, register func(*controllers.TransitionReconciler) error) error {
	for _, object := range []client.Object{&api.ControlPlaneNetworkTransition{}, &api.NamespaceNetworkTransition{}} {
		if err := register(&controllers.TransitionReconciler{Client: c, Object: object}); err != nil {
			return err
		}
	}
	return nil
}
