/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package migration registers the Supervisor migration controllers.
package migration

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/migration/controllers"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/infrastructure"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
	nsxmigration "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/migration"
)

// SetupWithManager registers both stages with a shared dispatcher. Disabled
// startup skips registration entirely. Infrastructure start checks are wired;
// concrete action handlers are not yet registered.
func SetupWithManager(mgr ctrl.Manager) error {
	return registerControllers(mgr.GetClient(), mgr.GetAPIReader(), &transition.Engine{Enabled: true}, func(r *controllers.Reconciler) error {
		return r.SetupWithManager(mgr)
	})
}

func registerControllers(c client.Client, reader client.Reader, engine *transition.Engine, register func(*controllers.Reconciler) error) error {
	starter := &infrastructure.Service{Readiness: &infrastructure.KubernetesReadiness{Reader: reader}, Projects: nsxmigration.NewService()}
	for _, stage := range []transition.Stage{transition.Infrastructure, transition.Namespaces} {
		r := &controllers.Reconciler{Client: c, APIReader: reader, Engine: engine, Stage: stage}
		if stage == transition.Infrastructure {
			r.Infrastructure = starter
		}
		if err := register(r); err != nil {
			return err
		}
	}
	return nil
}
