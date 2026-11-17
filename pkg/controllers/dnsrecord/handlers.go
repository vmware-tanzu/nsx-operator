/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dnsrecord

import (
	"context"
	"errors"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
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

// Both sources identify one namespace; never scan other namespaces or filter out
// failed records, which may become valid after domains have been added.
func (r *DNSRecordReconciler) namespaceToDNSRecordRequests(ctx context.Context, obj client.Object) []ctrl.Request {
	var namespace string
	switch obj := obj.(type) {
	case *corev1.Namespace:
		if obj != nil {
			namespace = obj.Name
		}
	case *v1alpha1.NetworkInfo:
		if obj != nil {
			namespace = obj.Namespace
		}
	}
	if namespace == "" {
		return nil
	}
	reqs, err := r.requestsForNamespace(ctx, namespace)
	if err != nil {
		log.Error(err, "Failed to enqueue DNSRecords for namespace DNS state change", "Namespace", namespace)
		// Map functions cannot return an error. Retry through the controller queue,
		// releasing the worker between attempts rather than waiting in the handler.
		return []ctrl.Request{{NamespacedName: types.NamespacedName{Namespace: namespaceRetryNamespace, Name: namespace}}}
	}
	return reqs
}

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

func (r *DNSRecordReconciler) reconcileNamespace(ctx context.Context, namespace string) (ctrl.Result, error) {
	reqs, err := r.requestsForNamespace(ctx, namespace)
	if err != nil {
		return ResultRequeue, err
	}
	if r.queue == nil {
		return ResultRequeue, errors.New("DNSRecord controller queue is not initialized")
	}
	for _, req := range reqs {
		if err := ctx.Err(); err != nil {
			return ResultRequeue, err
		}
		r.queue.Add(req)
	}
	return ResultNormal, nil
}
