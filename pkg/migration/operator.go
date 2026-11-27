/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package migration registers the Supervisor migration controllers.
package migration

import (
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/controllers"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
	nsxmigration "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/migration"
)

// SetupWithManager registers both stages with input validation for Evaluate.
func SetupWithManager(mgr ctrl.Manager) error {
	return setupWithManager(mgr, func(r *controllers.Reconciler) error {
		return r.SetupWithManager(mgr)
	})
}

// SetupWithEvaluateHandler registers the infrastructure Evaluate handler along
// with the shared transition controllers.
func SetupWithEvaluateHandler(mgr ctrl.Manager, evaluate transition.Handler) error {
	if evaluate == nil {
		return fmt.Errorf("infrastructure Evaluate handler is required")
	}
	return setupWithEvaluateHandler(mgr, evaluate, func(r *controllers.Reconciler) error {
		return r.SetupWithManager(mgr)
	})
}

func setupWithManager(mgr ctrl.Manager, register func(*controllers.Reconciler) error) error {
	return setupWithEvaluateHandler(mgr, nil, register)
}

func setupWithEvaluateHandler(mgr ctrl.Manager, evaluate transition.Handler, register func(*controllers.Reconciler) error) error {
	reader := mgr.GetAPIReader()
	engine := newEngineWithEvaluate(reader, nsxmigration.NewService(), evaluate)
	return registerControllers(mgr.GetClient(), reader, engine, register)
}

func newEngine(reader client.Reader, workflows transition.WorkflowDeleter) *transition.Engine {
	return newEngineWithEvaluate(reader, workflows, nil)
}

func newEngineWithEvaluate(reader client.Reader, workflows transition.WorkflowDeleter, evaluate transition.Handler) *transition.Engine {
	handlers := map[transition.Key]transition.Handler{
		{Stage: transition.Infrastructure, Action: api.ActionEvaluate}: transition.EvaluateInputs{},
		{Stage: transition.Namespaces, Action: api.ActionEvaluate}:     transition.EvaluateInputs{},
		{Stage: transition.Namespaces, Action: api.ActionCommit}:       &transition.NamespaceCommitHandler{Reader: reader, Workflows: workflows},
	}
	if evaluate != nil {
		handlers[transition.Key{Stage: transition.Infrastructure, Action: api.ActionEvaluate}] = evaluate
	}
	return &transition.Engine{Enabled: true, Handlers: handlers}
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
