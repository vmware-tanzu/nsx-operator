/*
Copyright © 2024 VMware, Inc. All Rights Reserved.

	SPDX-License-Identifier: Apache-2.0
*/
package testing

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/vpcnetwork"
)

// FakeVPCNetworkProvider is maintained for test only
type FakeVPCNetworkProvider struct {
}

func (p *FakeVPCNetworkProvider) IsVPCEnabledOnNamespace(ns string) (bool, error) {
	return true, nil
}

func (p *FakeVPCNetworkProvider) ReconcileWithVPCFilters(resource string, ctx context.Context, req ctrl.Request, innerFunc vpcnetwork.ReconcileFunc) (ctrl.Result, error) {
	return innerFunc(ctx, req)
}
