/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	extdnssrc "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/source"
)

// handlers: ListenerSet→Gateway enqueue, field indexes, Gateway/Route watch predicates, NetworkInfo→Gateway/Route DNS requeue.

func (r *GatewayReconciler) listenerSetEnqueueHandler() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(r.listenerSetToGatewayMapFunc)
}

func (r *GatewayReconciler) listenerSetToGatewayMapFunc(ctx context.Context, obj client.Object) []reconcile.Request {
	var requests []reconcile.Request
	ls, ok := obj.(*gatewayv1.ListenerSet)
	if !ok {
		return requests
	}

	parent := findParentGatewayFromListenerSet(ls)
	if parent == nil {
		return requests
	}

	gw := &gatewayv1.Gateway{}
	if err := r.Client.Get(ctx, *parent, gw); err != nil {
		log.Error(err, "Failed to fetch the parent Gateway", "Gateway", parent.String())
		return requests
	}

	if !shouldProcessGateway(gw) {
		log.Debug("Skipping ListenerSet enqueue: Parent Gateway is not managed", "Gateway", parent.String(),
			"ListenerSet", fmt.Sprintf("%s/%s", ls.Namespace, ls.Name))
		return requests
	}

	log.Debug("ListenerSet enqueue: reconcile parent Gateway", "Gateway", parent.String(),
		"ListenerSet", fmt.Sprintf("%s/%s", ls.Namespace, ls.Name))

	return append(requests, reconcile.Request{NamespacedName: *parent})
}

// gatewayNamespacedNameFromListenerParentRef wraps extdnssrc.GatewayNamespacedNameFromParentRef for ParentGatewayReference.
func gatewayNamespacedNameFromListenerParentRef(p *gatewayv1.ParentGatewayReference, listenerSetNamespace string) (types.NamespacedName, bool) {
	if p == nil {
		return types.NamespacedName{}, false
	}
	pr := gatewayv1.ParentReference{Group: p.Group, Kind: p.Kind, Name: p.Name, Namespace: p.Namespace}
	return extdnssrc.GatewayNamespacedNameFromParentRef(&pr, listenerSetNamespace)
}

func findParentGatewayFromListenerSet(obj client.Object) *types.NamespacedName {
	ls, ok := obj.(*gatewayv1.ListenerSet)
	if !ok || ls == nil {
		return nil
	}
	nn, ok := gatewayNamespacedNameFromListenerParentRef(&ls.Spec.ParentRef, ls.Namespace)
	if !ok {
		return nil
	}
	return &nn
}

func listenerSetParentGatewayIndexFunc(obj client.Object) []string {
	parentGateway := findParentGatewayFromListenerSet(obj)
	if parentGateway == nil {
		return []string{}
	}
	return []string{parentGateway.String()}
}

// shouldProcessGateway: managed GatewayClass and not DNS-ignore annotation.
func shouldProcessGateway(gw *gatewayv1.Gateway) bool {
	return filteredGatewayClasses.Has(string(gw.Spec.GatewayClassName)) && !gatewayDNSIgnored(gw)
}

// gatewayDNSIgnored: DNS ignore anno present and not equal to "false" (case-insensitive).
func gatewayDNSIgnored(gw *gatewayv1.Gateway) bool {
	if gw.Annotations == nil {
		return false
	}
	v, ok := gw.Annotations[servicecommon.AnnotationsDNSSkip]
	if !ok {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(v), "false")
}

func getRouteParentKey(routeNamespace string, parentRef gatewayv1.ParentReference) string {
	kind := "Gateway"
	if parentRef.Kind != nil {
		kind = string(*parentRef.Kind)
	}
	ns := routeNamespace
	if parentRef.Namespace != nil {
		ns = string(*parentRef.Namespace)
	}
	key := fmt.Sprintf("%s:%s/%s", kind, ns, parentRef.Name)
	if parentRef.SectionName != nil {
		key += fmt.Sprintf(":%s", string(*parentRef.SectionName))
	}
	return key
}

// routeParentAcceptedByKey: gateway NN string → RouteConditionAccepted status ("" if missing).
func routeParentAcceptedByKey(routeNamespace string, parents []gatewayv1.RouteParentStatus) map[string]bool {
	out := make(map[string]bool)
	for i := range parents {
		ps := parents[i]
		parentRef := ps.ParentRef
		parentKey := getRouteParentKey(routeNamespace, parentRef)
		out[parentKey] = extdnssrc.RouteAcceptedForParentRef(parents, routeNamespace, parentRef)
	}
	return out
}

// routeDNSRequestsForListenerSet maps a ListenerSet to reconcile.Requests for Routes indexed on that LS.

func reconcileRequestsFromRouteList(list client.ObjectList) []reconcile.Request {
	switch l := list.(type) {
	case *gatewayv1.HTTPRouteList:
		out := make([]reconcile.Request, 0, len(l.Items))
		for i := range l.Items {
			out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&l.Items[i])})
		}
		return out
	case *gatewayv1.GRPCRouteList:
		out := make([]reconcile.Request, 0, len(l.Items))
		for i := range l.Items {
			out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&l.Items[i])})
		}
		return out
	case *gatewayv1.TLSRouteList:
		out := make([]reconcile.Request, 0, len(l.Items))
		for i := range l.Items {
			out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&l.Items[i])})
		}
		return out
	default:
		return nil
	}
}

func parentGatewaysFromParentRefs(routeNamespace string, refs []gatewayv1.ParentReference) []types.NamespacedName {
	routeParents := make(map[string]types.NamespacedName)
	for i := range refs {
		nn, ok := extdnssrc.GatewayNamespacedNameFromParentRef(&refs[i], routeNamespace)
		if !ok {
			continue
		}
		routeParents[nn.String()] = nn
	}
	return slices.Collect(maps.Values(routeParents))
}

func (r *genericRouteReconciler[PT, T, PI]) getRouteHostnames(obj PT) []string {
	meta := obj.GetObjectMeta()
	specHostnames := obj.GetSpecHostnames()
	return extdnssrc.RouteHostnames(
		meta,
		extdnssrc.RouteSpecHostnames(specHostnames),
		servicecommon.AnnotationDNSHostnameSourceKey,
		servicecommon.AnnotationDNSHostnameKey,
		false,
	)
}

func (r *genericRouteReconciler[PT, T, PI]) convertToRouteObject(obj client.Object) (PT, bool) {
	var zero PT
	if obj == nil {
		return zero, false
	}

	switch v := obj.(type) {
	case *gatewayv1.HTTPRoute:
		return any(&HTTPRoute{HTTPRoute: *v}).(PT), true
	case *gatewayv1.GRPCRoute:
		return any(&GRPCRoute{GRPCRoute: *v}).(PT), true
	case *gatewayv1.TLSRoute:
		return any(&TLSRoute{TLSRoute: *v}).(PT), true
	default:
		return zero, false
	}
}

func parentRefKeys(routeNamespace string, refs []gatewayv1.ParentReference) []string {
	var keys []string
	for _, ref := range refs {
		keys = append(keys, getRouteParentKey(routeNamespace, ref))
	}
	slices.Sort(keys)
	return keys
}

// predicateFuncsRouteDNS: hostnames, parents, annos, or per-parent RouteConditionAccepted changed.
func (r *genericRouteReconciler[PT, T, PI]) predicateFuncsRouteDNS() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			route, ok := r.convertToRouteObject(e.Object)
			if !ok {
				return false
			}
			h := r.getRouteHostnames(route)
			return len(h) > 0
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldRoute, oldOk := r.convertToRouteObject(e.ObjectOld)
			newRoute, newOk := r.convertToRouteObject(e.ObjectNew)
			if !oldOk || !newOk {
				return false
			}
			if !maps.Equal(e.ObjectOld.GetAnnotations(), e.ObjectNew.GetAnnotations()) {
				return true
			}
			oldParent := parentRefKeys(oldRoute.GetNamespace(), oldRoute.GetParentRefs())
			newParent := parentRefKeys(newRoute.GetNamespace(), newRoute.GetParentRefs())
			if !slices.Equal(oldParent, newParent) {
				return true
			}
			oldHostnames := r.getRouteHostnames(oldRoute)
			newHostnames := r.getRouteHostnames(newRoute)
			slices.Sort(oldHostnames)
			slices.Sort(newHostnames)
			if !slices.Equal(oldHostnames, newHostnames) {
				return true
			}
			ns := newRoute.GetNamespace() // Accepted only; matches IsParentReady
			return !maps.Equal(
				routeParentAcceptedByKey(ns, oldRoute.GetRouteParentStatus()),
				routeParentAcceptedByKey(ns, newRoute.GetRouteParentStatus()))
		},
		DeleteFunc: func(_ event.DeleteEvent) bool {
			return true
		},
	}
}

func listenersSortedByName(ls []gatewayv1.Listener) []gatewayv1.Listener {
	if len(ls) == 0 {
		return nil
	}
	out := slices.Clone(ls)
	slices.SortFunc(out, func(a, b gatewayv1.Listener) int {
		return cmp.Compare(string(a.Name), string(b.Name))
	})
	return out
}

// predicateFuncsGateway: managed class + IP; annos, addresses, or sorted listeners changed.
var predicateFuncsGateway = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		gw := e.Object.(*gatewayv1.Gateway)
		return shouldProcessGateway(gw) && hasUsableGatewayIP(gw)
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldObj := e.ObjectOld.(*gatewayv1.Gateway)
		newObj := e.ObjectNew.(*gatewayv1.Gateway)
		if !shouldProcessGateway(oldObj) && !shouldProcessGateway(newObj) {
			return false
		}
		if oldObj.Spec.GatewayClassName != newObj.Spec.GatewayClassName {
			return true
		}
		if !maps.Equal(oldObj.GetAnnotations(), newObj.GetAnnotations()) {
			return true
		}

		if !reflect.DeepEqual(oldObj.Status.Addresses, newObj.Status.Addresses) { // Gateway + Route ipCache
			return true
		}

		return !reflect.DeepEqual(listenersSortedByName(oldObj.Spec.Listeners), listenersSortedByName(newObj.Spec.Listeners))
	},
	DeleteFunc: func(_ event.DeleteEvent) bool {
		return true
	},
}

// predicateNetworkInfoAllowedDNSDomainsChanged triggers when NetworkInfo.AllowedDNSDomains changes on update, or on create when domains are non-empty.
// Requeue the K8s Gateway resources to ensure the latest allowed DNS domains are used to validate the FQDNs claimed in the Gateway resources.
func predicateNetworkInfoAllowedDNSDomainsChanged() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			obj, ok := e.Object.(*v1alpha1.NetworkInfo)
			if !ok {
				return false
			}
			return len(obj.AllowedDNSDomains) > 0
		},
		DeleteFunc: func(event.DeleteEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNI, ok1 := e.ObjectOld.(*v1alpha1.NetworkInfo)
			newNI, ok2 := e.ObjectNew.(*v1alpha1.NetworkInfo)
			if !ok1 || !ok2 {
				return false
			}
			return !slices.Equal(oldNI.AllowedDNSDomains, newNI.AllowedDNSDomains)
		},
		GenericFunc: func(event.GenericEvent) bool {
			return false
		},
	}
}

// networkInfoToGatewayDNSRequests enqueues Gateways in the NetworkInfo namespace that are managed, have a usable LB IP, and set nsx.vmware.com/hostname.
func (r *GatewayReconciler) networkInfoToGatewayDNSRequests(ctx context.Context, obj client.Object) []reconcile.Request {
	ni, ok := obj.(*v1alpha1.NetworkInfo)
	if !ok || ni == nil {
		return nil
	}
	gwList := &gatewayv1.GatewayList{}
	if err := r.Client.List(ctx, gwList, client.InNamespace(ni.Namespace)); err != nil {
		log.Error(err, "Failed to list Gateways for NetworkInfo DNS domain change", "Namespace", ni.Namespace)
		return nil
	}
	var reqs []reconcile.Request
	for i := range gwList.Items {
		gw := &gwList.Items[i]
		if !shouldProcessGateway(gw) || !hasUsableGatewayIP(gw) {
			continue
		}
		gwAnnotations := gw.Annotations
		if len(gwAnnotations) == 0 {
			continue
		}
		hostnames := gwAnnotations[servicecommon.AnnotationDNSHostnameKey]
		if strings.TrimSpace(hostnames) == "" {
			continue
		}
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(gw)})
	}
	return reqs
}

// networkInfoToRouteDNSRequests lists all Routes of routeKind in the NetworkInfo namespace (same List pattern as resyncRouteDNS).
func networkInfoToRouteDNSRequests[Item any, PT Object[Item]](
	ctx context.Context,
	c client.Client,
	obj client.Object,
	routeKind string,
	list client.ObjectList,
) []reconcile.Request {
	ni, ok := obj.(*v1alpha1.NetworkInfo)
	if !ok || ni == nil {
		return nil
	}
	var reqs []reconcile.Request
	err := loopObjectList[Item](ctx, c, list, func(item *Item) {
		o := PT(item)
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(o)})
	}, client.InNamespace(ni.Namespace))
	if err != nil {
		log.Error(err, "Failed to list Routes for NetworkInfo DNS domain change", "Namespace", ni.Namespace, "routeKind", routeKind)
		return nil
	}
	return reqs
}
