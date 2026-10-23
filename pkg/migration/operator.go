/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package migration wires the Supervisor migration controllers and their services.
package migration

import (
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/vmware-tanzu/nsx-operator/pkg/migration/controllers"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
	migrationservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/migration"
)

// Operator owns only migration controllers, not normal NSX resource reconciliation.
// Register concrete handlers using NSX before SetupWithManager and manager startup.
// No external migration handlers are implemented in this framework.
type Operator struct {
	Engine *transition.Engine
	NSX    *migrationservice.Service
}

// New constructs a disabled-by-default dispatcher with lazy shared NSX connectivity.
func New(enabled bool) *Operator {
	return &Operator{Engine: &transition.Engine{Enabled: enabled}, NSX: migrationservice.NewService()}
}

// SetupWithManager registers both transition kinds with a common dispatcher.
func (o *Operator) SetupWithManager(mgr ctrl.Manager) error {
	for _, stage := range []transition.Stage{transition.Infrastructure, transition.Namespaces} {
		r := &controllers.Reconciler{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Engine: o.Engine, Stage: stage}
		if err := r.SetupWithManager(mgr); err != nil {
			return err
		}
	}
	return nil
}
