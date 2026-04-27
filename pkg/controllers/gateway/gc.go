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

// fetchExistingOwnerNNSet lists listObj via loopObjectList and returns object NNs.
func fetchExistingOwnerNNSet[T any](ctx context.Context, c client.Client, listObj client.ObjectList) (sets.Set[types.NamespacedName], error) {
	nnSet := sets.New[types.NamespacedName]()
	err := loopObjectList[T](ctx, c, listObj, func(item *T) {
		if obj, ok := any(item).(client.Object); ok {
			nnSet.Insert(client.ObjectKeyFromObject(obj))
		} else {
			log.Error(nil, "Item does not implement client.Object", "type", fmt.Sprintf("%T", item))
		}
	})

	if err != nil {
		return nil, err
	}
	return nnSet, nil
}

// gcListExistingOwners returns API-present owner NNs per kind; disabled kinds get empty sets so GC clears stale rows.
func gcListExistingOwners(ctx context.Context, c client.Client, flags gatewayAPIResources) (map[string]sets.Set[types.NamespacedName], error) {
	existing := make(map[string]sets.Set[types.NamespacedName])

	// Helper closure to fetch and store if the flag is enabled.
	fetchAndStore := func(enabled bool, kind string, _ client.ObjectList, fetchFn func() (sets.Set[types.NamespacedName], error)) error {
		if !enabled {
			existing[kind] = sets.New[types.NamespacedName]()
			return nil
		}
		s, err := fetchFn()
		if err != nil {
			return err
		}
		existing[kind] = s
		return nil
	}

	if err := fetchAndStore(flags.listenerSet, dns.ResourceKindListenerSet, &gatewayv1.ListenerSetList{}, func() (sets.Set[types.NamespacedName], error) {
		return fetchExistingOwnerNNSet[gatewayv1.ListenerSet](ctx, c, &gatewayv1.ListenerSetList{})
	}); err != nil {
		return nil, err
	}

	if err := fetchAndStore(flags.httpRoute, dns.ResourceKindHTTPRoute, &gatewayv1.HTTPRouteList{}, func() (sets.Set[types.NamespacedName], error) {
		return fetchExistingOwnerNNSet[gatewayv1.HTTPRoute](ctx, c, &gatewayv1.HTTPRouteList{})
	}); err != nil {
		return nil, err
	}

	if err := fetchAndStore(flags.grpcRoute, dns.ResourceKindGRPCRoute, &gatewayv1.GRPCRouteList{}, func() (sets.Set[types.NamespacedName], error) {
		return fetchExistingOwnerNNSet[gatewayv1.GRPCRoute](ctx, c, &gatewayv1.GRPCRouteList{})
	}); err != nil {
		return nil, err
	}

	if err := fetchAndStore(flags.tlsRoute, dns.ResourceKindTLSRoute, &gatewayv1.TLSRouteList{}, func() (sets.Set[types.NamespacedName], error) {
		return fetchExistingOwnerNNSet[gatewayv1.TLSRoute](ctx, c, &gatewayv1.TLSRouteList{})
	}); err != nil {
		return nil, err
	}
	return existing, nil
}

// gcOwnerMissingDNSRecords deletes store rows whose owner object is gone (per enabled kinds + Gateway set).
func gcOwnerMissingDNSRecords(ctx context.Context, r *GatewayReconciler, flags gatewayAPIResources, gatewayCRSet sets.Set[types.NamespacedName]) error {
	ownersByKind := r.Service.DNSRecordStore.GroupRecordsByResourceKind()
	existingByKind, err := gcListExistingOwners(ctx, r.Client, flags)
	if err != nil {
		return err
	}
	existingByKind[dns.ResourceKindGateway] = gatewayCRSet

	for kind, cachedOwners := range ownersByKind {
		apiSet := existingByKind[kind]
		for nn := range cachedOwners {
			if apiSet.Has(nn) {
				continue
			}
			if _, err := r.Service.DeleteDNSRecordByOwnerNN(ctx, kind, nn.Namespace, nn.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *GatewayReconciler) CollectGarbage(ctx context.Context) error {
	if !r.apiResources.gateway {
		return nil
	}
	gwList := &gatewayv1.GatewayList{}
	if err := r.Client.List(ctx, gwList); err != nil {
		log.Error(err, "failed to list K8s Gateways CR for gateway GC")
		return err
	}
	validGatewayCRSet := sets.New[types.NamespacedName]()
	_ = filterUsableGatewayCRs(gwList, func(gw *gatewayv1.Gateway) error {
		validGatewayCRSet.Insert(types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name})
		return nil
	})

	var errList []error
	if err := gcOwnerMissingDNSRecords(ctx, r, r.apiResources, validGatewayCRSet); err != nil {
		log.Error(err, "Failed to GC DNS records whose K8s owners are missing")
		errList = append(errList, err)
	}

	cachedGatewaySet := r.Service.ListReferredGatewayNN()
	log.Debug("Gateway CollectGarbage started", "cachedGateways", len(cachedGatewaySet), "listenerSet", r.apiResources.listenerSet)

	for elem := range cachedGatewaySet.Difference(validGatewayCRSet) {
		log.Info("GC stale Gateway in DNS store (Gateway CR missing)", "Gateway", elem.String())
		gwStub := &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Namespace: elem.Namespace, Name: elem.Name},
		}
		req := ctrl.Request{NamespacedName: elem}
		if _, err := r.deleteUnmanagedGateway(ctx, gwStub, req, false); err != nil {
			log.Error(err, "Failed to delete DNS records for stale Gateway", "Gateway", elem.String())
			errList = append(errList, err)
		}
	}

	if len(errList) > 0 {
		return fmt.Errorf("errors found in K8s Gateway garbage collection: %s", errList)
	}
	return nil
}
