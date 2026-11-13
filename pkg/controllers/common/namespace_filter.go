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

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// isVPCNamespaceByName fetches the Namespace by name and calls config.IsVPCNamespace.
// Returns true when the namespace cannot be fetched (transient error or already
// gone) so the Reconcile loop can decide what to do.
func isVPCNamespaceByName(c client.Reader, ns string, allowShared bool) bool {
	namespace := &corev1.Namespace{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: ns}, namespace); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "Failed to get Namespace for VPC predicate; allowing event through", "namespace", ns)
		}
		return true
	}
	if !allowShared {
		return config.IsVPCNamespace(namespace)
	}
	allowed, err := IsVPCNamespaceOrShared(context.Background(), c, namespace)
	if err != nil {
		log.Error(err, "Failed to get shared VPC Namespace; allowing event for reconcile retry", "namespace", ns)
		return true
	}
	return allowed
}

// IsVPCNamespaceOrShared permits VPC namespaces and namespaces explicitly sharing
// a VPC owner. Namespace synchronization and opt-in resource filters use the same
// provider checks; unrelated controllers retain their existing namespace rules.
// Lookup errors are returned so reconcilers can retry without admitting an
// unverified owner or silently dropping a configuration update.
func IsVPCNamespaceOrShared(ctx context.Context, c client.Reader, namespace *corev1.Namespace) (bool, error) {
	if config.IsVPCNamespace(namespace) {
		return true, nil
	}
	if !config.IsPerNamespaceProvidersSupported() {
		return false, nil
	}
	shared := namespace.Annotations[servicecommon.AnnotationSharedVPCNamespace]
	if shared == "" || shared == namespace.Name {
		return false, nil
	}
	owner := &corev1.Namespace{}
	if err := c.Get(ctx, types.NamespacedName{Name: shared}, owner); err != nil {
		return false, err
	}
	if !config.IsVPCNamespace(owner) {
		return false, nil
	}
	// Re-evaluate local annotations with the owner's provider to retain T1 conflicts.
	resolved := namespace.DeepCopy()
	resolved.Annotations[config.VPCNetworkConfigAnnotation] = owner.Annotations[config.VPCNetworkConfigAnnotation]
	return config.IsVPCNamespace(resolved), nil
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
	return vpcNamespacePredicate(c, false)
}

// SubnetPortNamespacePredicate also permits namespaces that explicitly share a
// VPC namespace, including system/staging namespaces without their own provider.
// Shared Subnet resources use their own predicate to restrict the exception to
// shared Subnets and pre-created SubnetSets.
func SubnetPortNamespacePredicate(c client.Reader) predicate.Funcs {
	return vpcNamespacePredicate(c, true)
}

func isSharedSubnetResource(obj client.Object) bool {
	switch resource := obj.(type) {
	case *v1alpha1.Subnet:
		return servicecommon.IsSharedSubnet(resource)
	case *v1alpha1.SubnetSet:
		return resource.Spec.SubnetNames != nil
	default:
		return false
	}
}

// CanReconcileSubnetResource rechecks provider eligibility after dequeue, including
// requests from dependency watches or restore. Only shared Subnets and pre-created
// SubnetSets may inherit an owner's provider; managed resources keep the VPC gate.
// Legacy clusters retain their existing reconcile behavior.
func CanReconcileSubnetResource(ctx context.Context, c client.Reader, obj client.Object) (bool, error) {
	if !config.IsPerNamespaceProvidersSupported() {
		return true, nil
	}
	namespace := &corev1.Namespace{}
	if err := c.Get(ctx, types.NamespacedName{Name: obj.GetNamespace()}, namespace); err != nil {
		return false, err
	}
	if isSharedSubnetResource(obj) {
		return IsVPCNamespaceOrShared(ctx, c, namespace)
	}
	return config.IsVPCNamespace(namespace), nil
}

// SubnetResourceNamespacePredicate lets inherited namespaces initialize shared
// Subnets and pre-created SubnetSets without admitting managed resources. Lookup
// failures reach Reconcile for retry, where the owner is verified before any write.
func SubnetResourceNamespacePredicate(c client.Reader) predicate.Funcs {
	base := VPCNamespacePredicate(c)
	sharedAllowed := func(obj client.Object) bool {
		if !config.IsPerNamespaceProvidersSupported() || !isSharedSubnetResource(obj) {
			return false
		}
		allowed, err := CanReconcileSubnetResource(context.Background(), c, obj)
		return allowed || err != nil
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return base.Create(e) || sharedAllowed(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return base.Update(e) || sharedAllowed(e.ObjectNew) },
		DeleteFunc:  base.DeleteFunc,
		GenericFunc: func(e event.GenericEvent) bool { return base.Generic(e) || sharedAllowed(e.Object) },
	}
}

func vpcNamespacePredicate(c client.Reader, allowShared bool) predicate.Funcs {
	isVPCNs := func(ns string) bool {
		if ns == "" {
			// Cluster-scoped resource: check global VPC flag as defensive programming.
			return config.HasVPCNamespaces()
		}
		return isVPCNamespaceByName(c, ns, allowShared)
	}

	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return isVPCNs(e.Object.GetNamespace())
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if !e.ObjectNew.GetDeletionTimestamp().IsZero() {
				return true
			}
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
// is a T1/legacy namespace.
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
	return config.IsT1Namespace(namespace)
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
			if !e.ObjectNew.GetDeletionTimestamp().IsZero() {
				return true
			}
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
