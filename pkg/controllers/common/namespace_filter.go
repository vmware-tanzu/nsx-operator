/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package common

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

// isVPCNamespaceByName fetches the Namespace by name and calls config.IsVPCNamespace.
// Returns true when the namespace cannot be fetched (transient error or already
// gone) so the Reconcile loop can decide what to do.
func isVPCNamespaceByName(c client.Reader, ns string) bool {
	namespace := &corev1.Namespace{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: ns}, namespace); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "Failed to get Namespace for VPC predicate; allowing event through", "namespace", ns)
		}
		return true
	}
	return config.IsVPCNamespace(namespace)
}

// VPCNamespacePredicate returns a predicate that filters events for VPC-only
// controllers.  Events are passed when config.IsVPCNamespace reports true for
// the resource's namespace.
//
// Behaviour by event type:
//   - Create / Update / Generic: allowed only for VPC namespaces.
//   - Delete: always allowed so the controller can clean up any existing NSX
//     resources even if the namespace is already gone.
//
// The namespace check is skipped for cluster-scoped resources (empty namespace),
// which are always allowed through.
func VPCNamespacePredicate(c client.Reader) predicate.Funcs {
	isVPCNs := func(ns string) bool {
		if ns == "" {
			// Cluster-scoped resource: no per-namespace filtering.
			return true
		}
		return isVPCNamespaceByName(c, ns)
	}

	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return isVPCNs(e.Object.GetNamespace())
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return isVPCNs(e.ObjectNew.GetNamespace())
		},
		// Always allow Delete events so the controller can clean up NSX
		// resources regardless of the current namespace network metadata.
		DeleteFunc: func(e event.DeleteEvent) bool {
			return true
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return isVPCNs(e.Object.GetNamespace())
		},
	}
}

// isT1NamespaceByName fetches the Namespace by name and returns true if the namespace
// is NOT a VPC namespace (i.e. it is a T1/legacy namespace).
// Returns true when the namespace cannot be fetched (transient error or already
// gone) so the Reconcile loop can decide what to do.
func isT1NamespaceByName(c client.Reader, ns string) bool {
	namespace := &corev1.Namespace{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: ns}, namespace); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "Failed to get Namespace for T1 predicate; allowing event through", "namespace", ns)
		}
		return true
	}
	return !config.IsVPCNamespace(namespace)
}

// T1NamespacePredicate returns a predicate that filters events for T1-only
// controllers. Events are passed when the resource's namespace is NOT a VPC namespace.
// Behaviour by event type:
//   - Create / Update / Generic: allowed only for T1 namespaces.
//   - Delete: always allowed so the controller can clean up any existing NSX
//     resources even if the namespace is already gone.
//
// The namespace check is skipped for cluster-scoped resources (empty namespace),
// which are always allowed through.
func T1NamespacePredicate(c client.Reader) predicate.Funcs {
	isT1Ns := func(ns string) bool {
		if ns == "" {
			return true
		}
		return isT1NamespaceByName(c, ns)
	}

	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return isT1Ns(e.Object.GetNamespace())
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return isT1Ns(e.ObjectNew.GetNamespace())
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return true
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return isT1Ns(e.Object.GetNamespace())
		},
	}
}
