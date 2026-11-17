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
)

// SetupWithManager registers both stages with a shared dispatcher. Disabled
// startup skips registration entirely. Evaluate checks supplied inputs before
// reporting that source discovery and reservation are unavailable.
func SetupWithManager(mgr ctrl.Manager) error {
	engine := &transition.Engine{Enabled: true, Handlers: map[transition.Key]transition.Handler{
		// Supply each stage's DHCP discovery and Evaluate handler when available.
		{Stage: transition.Infrastructure, Action: api.ActionEvaluate}: transition.EvaluateInputs{},
		{Stage: transition.Namespaces, Action: api.ActionEvaluate}:     transition.EvaluateInputs{},
	}}
	return registerControllers(mgr.GetClient(), mgr.GetAPIReader(), engine, func(r *controllers.Reconciler) error {
		return r.SetupWithManager(mgr)
	})
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
