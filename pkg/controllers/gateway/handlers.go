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
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	extdnssrc "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/source"
)

// handlers: ListenerSet→Gateway enqueue, field indexes, Gateway/Route watch predicates.

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
	v, ok := gw.Annotations[servicecommon.AnnotationsDNSIgnoreGateway]
	if !ok {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(v), "false")
}

// routeParentGatewayAcceptedByKey: gateway NN string → RouteConditionAccepted status ("" if missing).
func routeParentGatewayAcceptedByKey(routeNamespace string, parents []gatewayv1.RouteParentStatus) map[string]string {
	out := make(map[string]string)
	acceptedType := string(gatewayv1.RouteConditionAccepted)
	for i := range parents {
		ps := &parents[i]
		nn, ok := extdnssrc.GatewayNamespacedNameFromParentRef(&ps.ParentRef, routeNamespace)
		if !ok {
			continue
		}
		v := ""
		for j := range ps.Conditions {
			if ps.Conditions[j].Type == acceptedType {
				v = string(ps.Conditions[j].Status)
				break
			}
		}
		out[nn.String()] = v
	}
	return out
}

// routeDNSRequestsForListenerSet maps a ListenerSet to reconcile.Requests for Routes indexed on that LS.
func (r *GatewayReconciler) routeDNSRequestsForListenerSet(routeKind string) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []reconcile.Request {
		ls, ok := o.(*gatewayv1.ListenerSet)
		if !ok {
			return nil
		}
		key := types.NamespacedName{Namespace: ls.Namespace, Name: ls.Name}.String()
		var list client.ObjectList
		var logKind string
		switch routeKind {
		case dns.ResourceKindHTTPRoute:
			if !r.apiResources.httpRoute {
				return nil
			}
			list, logKind = &gatewayv1.HTTPRouteList{}, "HTTPRoute"
		case dns.ResourceKindGRPCRoute:
			if !r.apiResources.grpcRoute {
				return nil
			}
			list, logKind = &gatewayv1.GRPCRouteList{}, "GRPCRoute"
		case dns.ResourceKindTLSRoute:
			if !r.apiResources.tlsRoute {
				return nil
			}
			list, logKind = &gatewayv1.TLSRouteList{}, "TLSRoute"
		default:
			return nil
		}
		if err := r.Client.List(ctx, list, client.MatchingFields{routeParentListenerSetIndex: key}); err != nil {
			log.Error(err, "List routes for ListenerSet watch", "kind", logKind, "ListenerSet", key)
			return nil
		}
		return reconcileRequestsFromRouteList(list)
	}
}

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
	seen := sets.New[string]()
	var out []types.NamespacedName
	for i := range refs {
		nn, ok := extdnssrc.GatewayNamespacedNameFromParentRef(&refs[i], routeNamespace)
		if !ok {
			continue
		}
		if seen.Has(nn.String()) {
			continue
		}
		seen.Insert(nn.String())
		out = append(out, nn)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (a routeReconcilerAdapter[PT, T]) getRouteHostnames(obj PT) ([]string, bool) {
	meta := obj.GetObjectMeta()
	specHostnames := obj.GetSpecHostnames()
	h := extdnssrc.RouteHostnames(
		meta,
		extdnssrc.RouteSpecHostnames(specHostnames),
		servicecommon.AnnotationDNSHostnameSourceKey,
		servicecommon.AnnotationDNSHostnameKey,
		false,
	)
	return extdnssrc.NormalizeHostnameStrings(h), true
}

func (a routeReconcilerAdapter[PT, T]) convertToRouteObject(obj client.Object) (PT, bool) {
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

// predicateFuncsRouteDNS: hostnames, parents, annos, or per-parent RouteConditionAccepted changed.
func (a routeReconcilerAdapter[PT, T]) predicateFuncsRouteDNS() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			route, ok := a.convertToRouteObject(e.Object)
			if !ok {
				return false
			}
			h, hostnameOk := a.getRouteHostnames(route)
			return hostnameOk && len(h) > 0
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldRoute, oldOk := a.convertToRouteObject(e.ObjectOld)
			newRoute, newOk := a.convertToRouteObject(e.ObjectNew)
			if !oldOk || !newOk {
				return false
			}
			if !maps.Equal(e.ObjectOld.GetAnnotations(), e.ObjectNew.GetAnnotations()) {
				return true
			}
			oldParent := parentGatewaysFromParentRefs(oldRoute.GetNamespace(), oldRoute.GetParentRefs())
			newParent := parentGatewaysFromParentRefs(newRoute.GetNamespace(), newRoute.GetParentRefs())
			if !slices.Equal(oldParent, newParent) {
				return true
			}
			oldHostnames, _ := a.getRouteHostnames(oldRoute)
			newHostnames, _ := a.getRouteHostnames(newRoute)
			if !slices.Equal(oldHostnames, newHostnames) {
				return true
			}
			ns := newRoute.GetNamespace() // Accepted only; matches IsParentReady
			return !maps.Equal(
				routeParentGatewayAcceptedByKey(ns, oldRoute.GetRouteParentStatus()),
				routeParentGatewayAcceptedByKey(ns, newRoute.GetRouteParentStatus()))
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
