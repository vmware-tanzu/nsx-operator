/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package namespace

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// Namespaces are cluster-scoped, so ordinary requests have an empty Namespace.
// These reserved values distinguish retryable dependency lookups from resource work.
const (
	networkConfigDependency = "network-config"
	sharedOwnerDependency   = "shared-vpc-owner"
)

func sharedOwnerRequests(_ context.Context, obj client.Object) []reconcile.Request {
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: sharedOwnerDependency, Name: obj.GetName()}}}
}

var sharedOwnerPredicate = predicate.Funcs{
	CreateFunc: func(event.CreateEvent) bool { return true },
	UpdateFunc: func(e event.UpdateEvent) bool {
		before, after := e.ObjectOld.GetAnnotations(), e.ObjectNew.GetAnnotations()
		for _, key := range []string{config.VPCNetworkConfigAnnotation, servicecommon.AnnotationSharedVPCNamespace,
			config.T1DefaultConfigAnnotation, config.T1VmwareSystemSharedAnnotation, config.T1NsxNetworkConfigAnnotation} {
			oldValue, oldPresent := before[key]
			newValue, newPresent := after[key]
			if oldValue != newValue || oldPresent != newPresent {
				return true
			}
		}
		return false
	},
	DeleteFunc:  func(event.DeleteEvent) bool { return false },
	GenericFunc: func(event.GenericEvent) bool { return false },
}

func (r *NamespaceReconciler) reconcileDependency(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var names []string
	if req.Namespace == networkConfigDependency {
		var err error
		names, err = r.VPCService.GetNamespacesByNetworkconfigName(req.Name)
		if err != nil {
			return ctrl.Result{}, err
		}
	} else {
		namespaces := &corev1.NamespaceList{}
		if err := r.Client.List(ctx, namespaces); err != nil {
			return ctrl.Result{}, err
		}
		for _, ns := range namespaces.Items {
			_, explicit := ns.Annotations[config.VPCNetworkConfigAnnotation]
			if !explicit && ns.Name != req.Name && ns.Annotations[servicecommon.AnnotationSharedVPCNamespace] == req.Name {
				names = append(names, ns.Name)
			}
		}
	}
	for _, name := range names {
		// Feed the ordinary queue rather than reconciling inline: this retains
		// per-namespace serialization and independent retry/backoff for each child.
		// Its reconciler revalidates the current owner/provider before any writes.
		select {
		case r.namespaceEvents <- event.GenericEvent{Object: &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}}:
		case <-ctx.Done():
			return ctrl.Result{}, ctx.Err()
		}
	}
	return ctrl.Result{}, nil
}
