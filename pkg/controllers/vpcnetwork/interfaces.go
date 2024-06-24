/* Copyright © 2024 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package vpcnetwork

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
)

type ReconcileFunc func(ctx context.Context, req ctrl.Request) (ctrl.Result, error)

type VPCNetworkProvider interface {
	IsVPCEnabledOnNamespace(ns string) (bool, error)
	ReconcileWithVPCFilters(resource string, ctx context.Context, req ctrl.Request, innerFunc ReconcileFunc) (ctrl.Result, error)
}
