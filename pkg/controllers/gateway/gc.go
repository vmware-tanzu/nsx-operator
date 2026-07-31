/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
)

// gcListExistingOwners returns API-present owner NNs per kind; disabled kinds get empty sets so GC clears stale rows.
func gcListExistingOwners(ctx context.Context, c client.Client, r *GatewayReconciler) (map[string]sets.Set[types.NamespacedName], error) {
	existing := make(map[string]sets.Set[types.NamespacedName])

	// Initialize all known kinds to empty sets first
	existing[dns.ResourceKindHTTPRoute] = sets.New[types.NamespacedName]()
	existing[dns.ResourceKindGRPCRoute] = sets.New[types.NamespacedName]()
	existing[dns.ResourceKindTLSRoute] = sets.New[types.NamespacedName]()

	// Fetch existing owners for registered route reconcilers
	for _, rr := range r.apiResources.routeReconcilers {
		kind := rr.getKind()
		s, err := rr.fetchExistingOwnerNNSet(ctx, c)
		if err != nil {
			return nil, err
		}
		existing[kind] = s
	}

	return existing, nil
}

// gcOwnerMissingDNSRecords deletes store rows whose owner object is gone (per enabled kinds + Gateway set).
func gcOwnerMissingDNSRecords(ctx context.Context, r *GatewayReconciler, gatewayCRSet sets.Set[types.NamespacedName]) error {
	ownersByKind := r.DNS.ListRecordOwnerResource()
	existingByKind, err := gcListExistingOwners(ctx, r.Client, r)
	if err != nil {
		return err
	}
	existingByKind[dns.ResourceKindGateway] = gatewayCRSet

	for kind, cachedOwners := range ownersByKind {
		apiSet, ok := existingByKind[kind]
		if !ok {
			continue
		}
		for nn := range cachedOwners {
			if apiSet.Has(nn) {
				continue
			}
			if _, err := r.DNS.DeleteRecordByOwnerNN(ctx, kind, nn.Namespace, nn.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *GatewayReconciler) CollectGarbage(ctx context.Context) error {
	if !r.apiResources.gatewayEnabled {
		return nil
	}
	gwList := &gatewayv1.GatewayList{}
	if err := r.Client.List(ctx, gwList); err != nil {
		log.Error(err, "failed to list K8s Gateways CR for gateway GC")
		return err
	}
	validGatewayCRSet := sets.New[types.NamespacedName]()
	if err := filterUsableGatewayCRs(gwList, func(gw *gatewayv1.Gateway) error {
		validGatewayCRSet.Insert(types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name})
		return nil
	}); err != nil {
		log.Error(err, "failed to filter usable Gateway CRs for gateway GC")
		return err
	}

	cachedGatewaySet := r.DNS.ListReferredGatewayNN()
	log.Debug("Gateway CollectGarbage started", "cachedGateways", len(cachedGatewaySet), "listenerSet", r.apiResources.listenerSetEnabled)

	var errList []error
	if err := gcOwnerMissingDNSRecords(ctx, r, validGatewayCRSet); err != nil {
		log.Error(err, "Failed to GC DNS records whose K8s owners are missing")
		errList = append(errList, err)
	}

	for elem := range cachedGatewaySet.Difference(validGatewayCRSet) {
		gwStub := &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Namespace: elem.Namespace, Name: elem.Name},
		}
		req := ctrl.Request{NamespacedName: elem}
		if _, err := r.clearGatewayDNSRecordsAndCache(ctx, gwStub, req, "GC stale Gateway in DNS store"); err != nil {
			log.Error(err, "Failed to delete DNS records for stale Gateway", "Gateway", elem.String())
			errList = append(errList, err)
		}
	}

	if len(errList) > 0 {
		return fmt.Errorf("errors found in K8s Gateway garbage collection: %s", errList)
	}
	return nil
}
