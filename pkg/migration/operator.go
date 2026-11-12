/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package migration registers the Supervisor migration controllers.
package migration

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/controllers"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
	nsxmigration "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/migration"
)

// SetupWithManager registers both stages with a shared dispatcher. Disabled
// startup skips registration entirely. Commit is wired to the lazy NSX workflow
// client; the remaining migration actions require their service handlers.
func SetupWithManager(mgr ctrl.Manager) error {
	reader := mgr.GetAPIReader()
	engine := newEngine(reader, nsxmigration.NewService())
	return registerControllers(mgr.GetClient(), reader, engine, func(r *controllers.Reconciler) error {
		return r.SetupWithManager(mgr)
	})
}

func newEngine(reader client.Reader, workflows transition.WorkflowDeleter) *transition.Engine {
	return &transition.Engine{Enabled: true, Handlers: map[transition.Key]transition.Handler{
		{Stage: transition.Namespaces, Action: api.ActionCommit}: &transition.NamespaceCommitHandler{Reader: reader, Workflows: workflows},
	}}
}

func registerControllers(c client.Client, reader client.Reader, engine *transition.Engine, register func(*controllers.Reconciler) error) error {
	for _, stage := range []transition.Stage{transition.Infrastructure, transition.Namespaces} {
		r := &controllers.Reconciler{Client: c, APIReader: reader, Engine: engine, Stage: stage}
		if err := register(r); err != nil {
			return err
		}
	}
	return nil
}
