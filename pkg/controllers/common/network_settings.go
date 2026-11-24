/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package common

import (
	"context"
	"fmt"
	"path"

	netop "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

// WatchNetworkSettings adds the effective-provider watch on Supervisors serving this API.
// Older installations retain the existing namespace provisioning path.
func WatchNetworkSettings(mgr ctrl.Manager, b *builder.Builder, enqueue handler.MapFunc) (client.Reader, error) {
	kind := netop.SchemeGroupVersion.WithKind("NetworkSettings")
	if _, err := mgr.GetRESTMapper().RESTMapping(kind.GroupKind(), kind.Version); err != nil {
		if meta.IsNoMatchError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("discover NetworkSettings API: %w", err)
	}
	b.Watches(&netop.NetworkSettings{}, handler.EnqueueRequestsFromMapFunc(enqueue),
		builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool { return obj.GetName() == "default" })))
	return mgr.GetClient(), nil
}

// NamespaceDefaultNetworking returns whether VPC defaults may be reconciled and
// whether they must be checked against the target of a VDS cutover. The Namespace
// annotation binds the VPCNetworkConfiguration. It cannot override a realized provider.
func NamespaceDefaultNetworking(ctx context.Context, reader client.Reader, ns *corev1.Namespace) (provision, migrated bool, err error) {
	coordinated := ns.Labels[netop.NamespaceNetworkLabelKey] != ""
	if reader == nil {
		return !coordinated, false, nil
	}
	settings := &netop.NetworkSettings{}
	err = reader.Get(ctx, client.ObjectKey{Namespace: ns.Name, Name: "default"}, settings)
	if apierrors.IsNotFound(err) {
		return !coordinated, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if !settings.DeletionTimestamp.IsZero() || settings.Provider != netop.NetworkProviderVPC {
		return false, false, nil
	}
	migrated = settings.LegacyProvider == netop.NetworkProviderVSphereDistributed
	if migrated && ns.Annotations[config.VPCNetworkConfigAnnotation] == "" {
		// A delayed target assignment must not provision the cluster default VPC.
		return false, true, nil
	}
	return true, migrated, nil
}

// ValidateCutoverConfig checks the namespace assignment before either controller
// acts on it. Net Operator's VPCConfig requires shared defaults to be in the
// selected pre-created VPC.
func ValidateCutoverConfig(ns *corev1.Namespace, nc *v1alpha1.VPCNetworkConfiguration) error {
	if nc.Name != ns.Annotations[config.VPCNetworkConfigAnnotation] || !nc.DeletionTimestamp.IsZero() {
		return fmt.Errorf("target VPCNetworkConfiguration for namespace %s changed or is terminating", ns.Name)
	}
	for _, subnet := range nc.Spec.Subnets {
		if (subnet.PodDefault || subnet.VMDefault) && (nc.Spec.VPC == "" || path.Dir(subnet.Path) != nc.Spec.VPC+"/subnets") {
			return fmt.Errorf("default subnet %q does not belong to the target VPC in VPCNetworkConfiguration %s", subnet.Path, nc.Name)
		}
	}
	return nil
}
