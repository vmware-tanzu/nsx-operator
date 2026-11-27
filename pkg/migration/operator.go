/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package migration registers the Supervisor migration controllers.
package migration

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/controllers"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/infrastructure"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
	nsxmigration "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/migration"
)

// SetupWithManager registers both stages with a shared dispatcher. Disabled
// startup skips registration entirely. Evaluate checks supplied inputs before
// reporting that source discovery and reservation are unavailable. Commit is
// wired to the lazy NSX workflow client.
func SetupWithManager(mgr ctrl.Manager) error {
	return setupWithManager(mgr, func(r *controllers.Reconciler) error {
		return r.SetupWithManager(mgr)
	})
}

func setupWithManager(mgr ctrl.Manager, register func(*controllers.Reconciler) error) error {
	reader := mgr.GetAPIReader()
	engine := newEngine(reader, nsxmigration.NewService())
	return registerControllers(mgr.GetClient(), reader, engine, register)
}

func newEngine(reader client.Reader, workflows transition.WorkflowDeleter) *transition.Engine {
	return &transition.Engine{Enabled: true, Handlers: map[transition.Key]transition.Handler{
		{Stage: transition.Infrastructure, Action: api.ActionEvaluate}: transition.EvaluateInputs{},
		{Stage: transition.Namespaces, Action: api.ActionEvaluate}:     transition.EvaluateInputs{},
		{Stage: transition.Namespaces, Action: api.ActionCommit}:       &transition.NamespaceCommitHandler{Reader: reader, Workflows: workflows},
	}}
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
