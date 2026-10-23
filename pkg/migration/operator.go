/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package migration wires the Supervisor migration controllers and their services.
package migration

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/controllers"
	migrationservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/migration"
)

// Operator owns migration controller registration and lazy NSX connectivity.
// Action execution is implemented separately in UBMWM-12405.
type Operator struct {
	Enabled bool
	NSX     *migrationservice.Service
}

// New constructs a disabled-by-default framework without contacting NSX.
func New(enabled bool) *Operator {
	return &Operator{Enabled: enabled, NSX: migrationservice.NewService()}
}

// SetupWithManager registers watches for the two orchestration APIs.
func (o *Operator) SetupWithManager(mgr ctrl.Manager) error {
	for _, object := range []client.Object{&api.ControlPlaneNetworkTransition{}, &api.NamespaceNetworkTransition{}} {
		r := &controllers.Reconciler{Client: mgr.GetClient(), Enabled: o.Enabled, Object: object}
		if err := r.SetupWithManager(mgr); err != nil {
			return err
		}
	}
	return nil
}
