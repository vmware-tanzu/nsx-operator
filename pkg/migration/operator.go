/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package migration registers the Supervisor migration controllers.
package migration

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/controllers"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/inspection"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/topology"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
	migrationservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/migration"
)

// SetupWithManager registers both stages with a shared dispatcher. Disabled
// startup skips registration entirely. Namespace Evaluate includes input
// discovery and preflight; Commit uses the lazy NSX workflow client.
func SetupWithManager(mgr ctrl.Manager) error {
	return SetupWithManagerAndTopologyDiscoverer(mgr, nil)
}

// SetupWithManagerAndTopologyDiscoverer provides the live topology reader to
// namespace preflight and action handlers. A nil discoverer intentionally
// blocks namespace actions and emits an error until discovery is implemented.
func SetupWithManagerAndTopologyDiscoverer(mgr ctrl.Manager, discoverer topology.Discoverer) error {
	reader := mgr.GetAPIReader()
	engine := newEngine(reader, migrationservice.NewService())
	engine.Handlers[transition.Key{Stage: transition.Namespaces, Action: api.ActionEvaluate}] = transition.EvaluateInputs{
		Discovery: transition.NamespaceTopologyDHCPDiscovery{},
		Preflight: transition.NamespaceWorkloadCompatibility{Reader: reader},
		Evaluator: transition.NamespaceEvaluate{NSXService: migrationservice.NewService(), NamingInspector: inspection.Service{}},
	}
	return registerControllers(mgr.GetClient(), reader, engine, discoverer, func(r *controllers.Reconciler) error {
		return r.SetupWithManager(mgr)
	})
}

func setupWithManager(mgr ctrl.Manager, register func(*controllers.Reconciler) error) error {
	return setupWithTopologyDiscoverer(mgr, nil, register)
}

func setupWithTopologyDiscoverer(mgr ctrl.Manager, discoverer topology.Discoverer, register func(*controllers.Reconciler) error) error {
	reader := mgr.GetAPIReader()
	engine := newEngine(reader, migrationservice.NewService())
	return registerControllers(mgr.GetClient(), reader, engine, discoverer, register)
}

func newEngine(reader client.Reader, workflows transition.WorkflowDeleter) *transition.Engine {
	return &transition.Engine{Enabled: true, WorkflowDeleter: workflows, Handlers: map[transition.Key]transition.Handler{
		{Stage: transition.Infrastructure, Action: api.ActionEvaluate}: transition.EvaluateInputs{},
		{Stage: transition.Namespaces, Action: api.ActionEvaluate}:     transition.EvaluateInputs{},
		{Stage: transition.Namespaces, Action: api.ActionCommit}:       &transition.NamespaceCommitHandler{Reader: reader, Workflows: workflows},
	}}
}

func registerControllers(c client.Client, reader client.Reader, engine *transition.Engine, discoverer topology.Discoverer, register func(*controllers.Reconciler) error) error {
	for _, stage := range []transition.Stage{transition.Infrastructure, transition.Namespaces} {
		r := &controllers.Reconciler{Client: c, APIReader: reader, Engine: engine, Stage: stage, TopologyDiscoverer: discoverer}
		if err := register(r); err != nil {
			return err
		}
	}
	return nil
}
