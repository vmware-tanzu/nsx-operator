/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dnsrecord

import (
	"context"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

// A DNSRecord filtered out before migration has no pending reconcile of its own.
func predicateNamespaceBecameVPC() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			ns, ok := e.Object.(*corev1.Namespace)
			return ok && config.IsVPCNamespace(ns)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNS, oldOK := e.ObjectOld.(*corev1.Namespace)
			newNS, newOK := e.ObjectNew.(*corev1.Namespace)
			return oldOK && newOK && oldNS != nil && newNS != nil &&
				!config.IsVPCNamespace(oldNS) && config.IsVPCNamespace(newNS)
		},
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

func predicateNetworkInfoDNSStateChanged() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			ni, ok := e.Object.(*v1alpha1.NetworkInfo)
			return ok && ni != nil
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNI, oldOK := e.ObjectOld.(*v1alpha1.NetworkInfo)
			newNI, newOK := e.ObjectNew.(*v1alpha1.NetworkInfo)
			if !oldOK || !newOK || oldNI == nil || newNI == nil {
				return false
			}
			// Initial VPC readiness can follow the Namespace migration event.
			if len(oldNI.VPCs) == 0 && len(newNI.VPCs) > 0 {
				return true
			}
			// Compare copies: ordering is irrelevant and informer objects must not be mutated.
			oldDomains := slices.Clone(oldNI.AllowedDNSRecordDomains)
			newDomains := slices.Clone(newNI.AllowedDNSRecordDomains)
			slices.Sort(oldDomains)
			slices.Sort(newDomains)
			return !slices.Equal(oldDomains, newDomains)
		},
		// NetworkInfo deletion alone does not revoke the configuration's DNS zones.
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

// requestsForNamespace fans out to every DNSRecord in one namespace; never scan other
// namespaces or filter out failed records, which may become valid after domains have
// been added. Wired into common.NewRetryingNamespaceHandler, which retries list failures.
func (r *DNSRecordReconciler) requestsForNamespace(ctx context.Context, namespace string) ([]ctrl.Request, error) {
	records := &v1alpha1.DNSRecordList{}
	if err := r.Client.List(ctx, records, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("listing DNSRecords in Namespace %s: %w", namespace, err)
	}
	reqs := make([]ctrl.Request, 0, len(records.Items))
	for i := range records.Items {
		reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&records.Items[i])})
	}
	return reqs, nil
}
