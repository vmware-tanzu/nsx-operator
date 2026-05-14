/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
	extdnssrc "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/source"
)

const (
	routeParentGatewayIndex        = "routeParentGateway"   // Route -> Gateway parent index
	ipCacheStartupWarmRequeueAfter = 500 * time.Millisecond // Route DNS requeue until ipCache warm
)

type routeReconciler interface {
	resyncRouteDNS(ctx context.Context, gwNN types.NamespacedName, lsList []gatewayv1.ListenerSet)
	registerWatcher(mgr ctrl.Manager, opts controller.Options, listenerSetEnabled bool) error
	getKind() string
	fetchExistingOwnerNNSet(ctx context.Context, c client.Client) (sets.Set[types.NamespacedName], error)
	setRouteIndexField(mgr ctrl.Manager, listenerSetEnabled bool) error
}

func (r *genericRouteReconciler[PT, T, PI]) routeParentGatewayIndexFunc(obj client.Object) []string {
	route, ok := r.convertToRouteObject(obj)
	if !ok {
		return nil
	}
	parents := parentGatewaysFromParentRefs(route.GetNamespace(), route.GetParentRefs())
	if len(parents) == 0 {
		return nil
	}
	keys := make([]string, len(parents))
	for i, p := range parents {
		keys[i] = p.String()
	}
	return keys
}

func (r *genericRouteReconciler[PT, T, PI]) routeParentListenerSetIndexFunc(obj client.Object) []string {
	route, ok := r.convertToRouteObject(obj)
	if !ok {
		return nil
	}
	ns := route.GetNamespace()
	var seen []string
	for _, ref := range route.GetParentRefs() {
		if lsNN, ok := extdnssrc.ListenerSetNamespacedNameFromParentRef(&ref, ns); ok {
			seen = append(seen, lsNN.String())
		}
	}
	if len(seen) == 0 {
		return nil
	}
	slices.Sort(seen)
	return slices.Compact(seen)
}

// genericRouteReconciler implements reconcile.Reconciler for one Route kind (HTTP/GRPC/TLS) using objCreator PT.
type genericRouteReconciler[PT RouteObject[T], T any, PI Object[T]] struct {
	client                 client.Client
	dns                    dns.DNSRecordProvider
	ipCache                *gatewayIPCache
	ipCacheWarmedOnStartup *atomic.Bool
	kind                   string
	objCreator             func(*T) PT
	statusUpdater          StatusUpdater
	resyncCh               chan event.TypedGenericEvent[*T]
	newList                func() client.ObjectList
}

// Reconcile upserts or deletes DNS store rows for one Route; returns ctrl.Result, err.
func (r *genericRouteReconciler[PT, T, PI]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	wrapper := r.objCreator(nil)
	routeKey := req.NamespacedName.String()
	route := wrapper.GetObject()

	if err := r.client.Get(ctx, req.NamespacedName, route); err != nil {
		if apierrors.IsNotFound(err) { // Route gone: drop DNS owner rows
			if _, derr := r.dns.DeleteRecordByOwnerNN(ctx, r.kind, req.Namespace, req.Name); derr != nil {
				log.Error(derr, "failed to delete DNS records for route", "route", routeKey)
				return common.ResultRequeueAfter10sec, nil
			}
			return common.ResultNormal, nil
		}
		log.Error(err, "failed to get route", "route", routeKey)
		return common.ResultRequeueAfter10sec, nil
	}

	if !r.ipCacheWarmedOnStartup.Load() { // wait for Gateway ipCache warm
		log.Debug("Deferring Route DNS reconcile until Gateway ipCache startup warm completes", "Route", req.NamespacedName)
		return ctrl.Result{RequeueAfter: ipCacheStartupWarmRequeueAfter}, nil
	}

	batch, err := r.buildRouteDNSMergedEndpoints(wrapper)
	if err != nil {
		var zoneValErr *dns.DNSZoneValidationError
		if errors.As(err, &zoneValErr) {
			r.statusUpdater.UpdateFail(ctx, route, err, "DNS record validation failed by the allowed DNS zones", nil)
			log.Error(err, "Route failed DNS zone validation, cleaning up records outside allowed zones", "Route", req.NamespacedName)
			if statusErr := r.updateRouteParentConditions(ctx, req.NamespacedName, err); statusErr != nil {
				log.Error(statusErr, "failed to update Route with DNSRecordReady condition", "route", routeKey)
				return common.ResultRequeueAfter10sec, nil
			}
			if batch != nil && len(batch.Rows) > 0 {
				if _, uErr := r.dns.CreateOrUpdateRecords(ctx, batch); uErr != nil {
					log.Error(uErr, "Unable to apply valid records despite validation errors", "Route", req.NamespacedName.String())
					return common.ResultRequeueAfter10sec, nil
				}
			} else {
				if _, derr := r.dns.DeleteRecordByOwnerNN(ctx, r.kind, req.Namespace, req.Name); derr != nil {
					log.Error(derr, "Unable to delete the existing disallowed records", "Route", req.NamespacedName.String())
					return common.ResultRequeueAfter10sec, nil
				}
			}
			return common.ResultNormal, nil
		}
		fmt.Printf("UpdateFail called! err=%v\n", err)
		r.statusUpdater.UpdateFail(ctx, route, err, "DNS record reconcile failed", nil)
		log.Error(err, "failed to build endpoints for Route", "route", routeKey)
		return common.ResultRequeueAfter10sec, nil
	}

	if batch == nil || len(batch.Rows) == 0 {
		log.Info("No DNS record are required for route, cleaning up the stale records", "kind", route.GetObjectKind(), "route", req.NamespacedName)
		_, derr := r.dns.DeleteRecordByOwnerNN(ctx, r.kind, req.Namespace, req.Name)
		if derr != nil {
			log.Error(derr, "failed to clean up DNS records for route", "route", routeKey)
			return common.ResultRequeueAfter10sec, nil
		}
		// Route no longer requires DNS — remove the nsx-operator parent status entry entirely.
		if statusErr := r.removeRouteNSXOperatorParentStatus(ctx, req.NamespacedName); statusErr != nil {
			log.Error(statusErr, "failed to remove DNS parent status for Route", "route", routeKey)
			return common.ResultRequeueAfter10sec, nil
		}
		return common.ResultNormal, nil
	}

	_, opErr := r.dns.CreateOrUpdateRecords(ctx, batch)
	if opErr != nil {
		r.statusUpdater.UpdateFail(ctx, route, opErr, "DNS record reconcile failed", nil)
		statusErr := r.updateRouteParentConditions(ctx, req.NamespacedName, opErr)
		if statusErr != nil {
			log.Error(statusErr, "failed to update Route with DNSRecordReady condition", "route", routeKey)
			return common.ResultRequeueAfter10sec, nil
		}
		log.Error(opErr, "DNS record reconcile failed", "route", routeKey)
		return common.ResultRequeueAfter10sec, nil
	}

	// Call updateRouteParentConditions even when cacheChanged=false.
	if statusErr := r.updateRouteParentConditions(ctx, req.NamespacedName, nil); statusErr != nil {
		log.Error(statusErr, "failed to update Route with DNSRecordReady condition", "route", routeKey)
		return common.ResultRequeueAfter10sec, nil
	}
	r.statusUpdater.UpdateSuccess(ctx, route, nil)
	return common.ResultNormal, nil
}

// buildRouteDNSMergedEndpoints builds owner-scoped Route DNS rows (Gateway or ListenerSet→root Gateway, same rules as aggregation).
func (r *genericRouteReconciler[PT, T, PI]) buildRouteDNSMergedEndpoints(route PT) (*dns.AggregatedDNSEndpoints, error) {
	owner := route.GetResourceRef()
	eps, _, err := r.buildRouteDNSEndpointsForAggregation(route.GetNamespace(), owner, route.GetParentRefs(), route.GetRouteParentStatus(), route.GetObjectMeta(), route.GetSpecHostnames())
	if err != nil || len(eps) == 0 {
		return nil, err
	}
	return dns.NewOwnerScopedAggregatedRouteDNS(owner, eps), nil
}

// updateRouteParentConditions adds or updates the dedicated nsx-operator parent entry in the
// route's status.parents which is used to maintain the DNSRecord configuration conditions.
func (r *genericRouteReconciler[PT, T, PI]) updateRouteParentConditions(ctx context.Context, routeKey types.NamespacedName, condErr error) error {
	if !namespaceNetworkInfoExists(ctx, r.client, routeKey.Namespace) {
		log.Info("No NetworkInfo found in route namespace; skipping DNSRecordReady status update", "Route", routeKey)
		return nil
	}
	niRef := networkInfoParentRef(routeKey.Namespace)
	cond := buildDNSRecordReadyCondition(condErr)

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		creator := r.objCreator(nil)
		route := creator.GetObject()
		if getErr := r.client.Get(ctx, routeKey, route); getErr != nil {
			return client.IgnoreNotFound(getErr)
		}

		cond.ObservedGeneration = creator.GetGeneration()
		parents := creator.GetRouteParentStatus()

		for i := range parents {
			if parents[i].ControllerName == nsxOperatorGatewayDNSController {
				if meta.SetStatusCondition(&parents[i].Conditions, cond) {
					return r.client.Status().Update(ctx, route)
				}
				return nil
			}
		}

		parents = append(parents, gatewayv1.RouteParentStatus{
			ControllerName: nsxOperatorGatewayDNSController,
			ParentRef:      niRef,
			Conditions:     []metav1.Condition{cond},
		})
		creator.SetRouteParentStatus(parents)
		return r.client.Status().Update(ctx, route)
	})
}

// removeRouteNSXOperatorParentStatus removes the dedicated nsx-operator parent entry from
// status.parents when the route no longer requires any DNS records.
func (r *genericRouteReconciler[PT, T, PI]) removeRouteNSXOperatorParentStatus(ctx context.Context, routeKey types.NamespacedName) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		creator := r.objCreator(nil)
		route := creator.GetObject()
		if getErr := r.client.Get(ctx, routeKey, route); getErr != nil {
			return client.IgnoreNotFound(getErr)
		}
		parents := creator.GetRouteParentStatus()
		idx := -1
		for i := range parents {
			if parents[i].ControllerName == nsxOperatorGatewayDNSController {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil // entry already absent
		}
		creator.SetRouteParentStatus(slices.Delete(parents, idx, idx+1))
		return r.client.Status().Update(ctx, route)
	})
}

// namespaceNetworkInfoExists reports whether the NetworkInfo CR for ns exists.
// The namespace controller always creates it with Name == Namespace.
func namespaceNetworkInfoExists(ctx context.Context, c client.Client, ns string) bool {
	ni := &v1alpha1.NetworkInfo{}
	err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: ns}, ni)
	if err == nil {
		return true
	}
	if !apierrors.IsNotFound(err) {
		log.Error(err, "Failed to get NetworkInfo for namespace", "Namespace", ns)
	}
	return false
}

// networkInfoParentRef builds a ParentReference for the namespace's NetworkInfo CR.
// Name == Namespace by convention (set by the namespace controller).
func networkInfoParentRef(ns string) gatewayv1.ParentReference {
	group := gatewayv1.Group(v1alpha1.GroupVersion.Group)
	kind := gatewayv1.Kind("NetworkInfo")
	namespace := gatewayv1.Namespace(ns)
	return gatewayv1.ParentReference{
		Group:     &group,
		Kind:      &kind,
		Name:      gatewayv1.ObjectName(ns),
		Namespace: &namespace,
	}
}

func newRouteReconciler[PT RouteObject[T], T any, PI Object[T]](
	r *GatewayReconciler,
	kind string,
	objCreator func(*T) PT,
	newList func() client.ObjectList,
) *genericRouteReconciler[PT, T, PI] {
	gr := &genericRouteReconciler[PT, T, PI]{
		client:                 r.Client,
		dns:                    r.DNS,
		ipCache:                r.ipCache,
		ipCacheWarmedOnStartup: &r.ipCacheWarmedOnStartup,
		kind:                   kind,
		objCreator:             objCreator,
		resyncCh:               make(chan event.TypedGenericEvent[*T], 256),
		newList:                newList,
	}
	if svc, ok := r.DNS.(*dns.DNSRecordService); ok && svc != nil && svc.NSXConfig != nil {
		updater := common.NewStatusUpdater(
			r.Client,
			svc.NSXConfig,
			r.Recorder,
			common.MetricResTypeGateway,
			"ProjectDnsRecord",
			kind,
		)
		gr.statusUpdater = &updater
	} else {
		gr.statusUpdater = r.StatusUpdater
	}
	return gr
}

func (r *genericRouteReconciler[PT, T, PI]) setRouteIndexField(mgr ctrl.Manager, listenerSetEnabled bool) error {
	var obj = r.objCreator(nil).GetObject()
	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), obj, routeParentGatewayIndex, r.routeParentGatewayIndexFunc); err != nil {
		log.Error(err, "Failed to register Route cache indexer", "controller", "Gateway", "type", r.kind)
		return err
	}
	if listenerSetEnabled {
		if err := mgr.GetFieldIndexer().IndexField(context.TODO(), obj, routeParentListenerSetIndex, r.routeParentListenerSetIndexFunc); err != nil {
			log.Error(err, "Failed to register Route ListenerSet cache indexer", "controller", "Gateway", "type", r.kind)
			return err
		}
	}
	return nil
}

func (r *genericRouteReconciler[PT, T, PI]) routeDNSRequestsForListenerSet(newList func() client.ObjectList) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []reconcile.Request {
		ls, ok := o.(*gatewayv1.ListenerSet)
		if !ok {
			return nil
		}
		key := types.NamespacedName{Namespace: ls.Namespace, Name: ls.Name}.String()
		list := newList()
		if err := r.client.List(ctx, list, client.MatchingFields{routeParentListenerSetIndex: key}); err != nil {
			log.Error(err, "List routes for ListenerSet watch", "kind", r.kind, "ListenerSet", key)
			return nil
		}
		return reconcileRequestsFromRouteList(list)
	}
}

// resolveParentRefToRootGatewayNN returns root Gateway NN for ref, or (_, false) if unknown or not in ipCache.
func (r *genericRouteReconciler[PT, T, PI]) resolveParentRefToRootGatewayNN(routeNS string, ref *gatewayv1.ParentReference) (types.NamespacedName, bool) {
	if gwNN, ok := extdnssrc.GatewayNamespacedNameFromParentRef(ref, routeNS); ok {
		return gwNN, true
	}
	lsNN, ok := extdnssrc.ListenerSetNamespacedNameFromParentRef(ref, routeNS)
	if !ok {
		return types.NamespacedName{}, false
	}
	if r.ipCache == nil {
		return types.NamespacedName{}, false
	}
	gwNN, ok := r.ipCache.rootGatewayForCachedListenerSet(lsNN)
	if !ok {
		return types.NamespacedName{}, false
	}
	return gwNN, true
}

// distinctAcceptedRootGatewayNNs returns unique root Gateway NNs for Accepted parent refs.
func (r *genericRouteReconciler[PT, T, PI]) distinctAcceptedRootGatewayNNs(routeNS string,
	refs []gatewayv1.ParentReference, status []gatewayv1.RouteParentStatus) []types.NamespacedName {
	parents := make(map[string]types.NamespacedName)
	for i := range refs {
		ref := refs[i]
		if !extdnssrc.RouteAcceptedForParentRef(status, routeNS, ref) {
			continue
		}
		gwNN, ok := r.resolveParentRefToRootGatewayNN(routeNS, &ref)
		if !ok {
			continue
		}
		parents[gwNN.String()] = gwNN
	}
	return slices.Collect(maps.Values(parents))
}

// inferRouteDNSHostnamesFromAcceptedParents fills hostnames from listener admission.
func (r *genericRouteReconciler[PT, T, PI]) inferRouteDNSHostnamesFromAcceptedParents(routeNS string, parentRefs []gatewayv1.ParentReference,
	parentStatus []gatewayv1.RouteParentStatus, objMeta *metav1.ObjectMeta) ([]string, error) {
	seen := sets.New[string]()
	for i := range parentRefs {
		ref := parentRefs[i]
		if !extdnssrc.RouteAcceptedForParentRef(parentStatus, routeNS, ref) {
			continue
		}
		gwNN, ok := r.resolveParentRefToRootGatewayNN(routeNS, &ref)
		if !ok {
			continue
		}
		ent, ok := r.ipCache.get(gwNN)
		if !ok || len(ent.IPs) == 0 {
			continue
		}
		filters := extdnssrc.AdmissionHostnameFiltersForRouteParentFromRows(ent.AdmissionRows, &ref, routeNS)
		inh, err := extdnssrc.RouteHostnamesMatchingAdmission(filters, objMeta, []string{""}, servicecommon.AnnotationDNSHostnameSourceKey, servicecommon.AnnotationDNSHostnameKey)
		if err != nil {
			return nil, err
		}
		seen.Insert(inh...)
	}
	return sets.List(seen), nil
}

// appendAggregatedRouteDNSEndpointForHostname appends one ExternalDNS endpoint for hostname h into eps.
func (r *genericRouteReconciler[PT, T, PI]) buildRouteDNSEndpointsForHostname(routeNS, h string,
	parentRefs []gatewayv1.ParentReference, parentStatus []gatewayv1.RouteParentStatus,
	allowWild, allowMultiGatewayTargetMerge bool) []*extdns.Endpoint {
	if strings.HasPrefix(h, "*.") && !allowWild {
		return nil
	}
	var matches []parentGatewayMatch
	for i := range parentRefs {
		ref := parentRefs[i]
		if !extdnssrc.RouteAcceptedForParentRef(parentStatus, routeNS, ref) {
			continue
		}
		gwNN, ok := r.resolveParentRefToRootGatewayNN(routeNS, &ref)
		if !ok {
			continue
		}
		ent, ok := r.ipCache.get(gwNN)
		if !ok || len(ent.IPs) == 0 {
			continue
		}
		filters := extdnssrc.AdmissionHostnameFiltersForRouteParentFromRows(ent.AdmissionRows, &ref, routeNS)
		f, okMatch := extdnssrc.BestMatchingAdmissionFilter(filters, h)
		if !okMatch {
			continue
		}
		matches = append(matches, parentGatewayMatch{nn: gwNN, filter: f, ips: ent.IPs})
	}
	if len(matches) == 0 {
		return nil
	}

	refF := matches[0].filter
	allSame := true
	for _, m := range matches[1:] {
		if m.filter != refF {
			allSame = false
			break
		}
	}
	var label string
	var targets extdns.Targets
	if len(matches) >= 2 && allSame && allowMultiGatewayTargetMerge {
		keys := make([]string, len(matches))
		for i := range matches {
			keys[i] = matches[i].nn.String()
		}
		slices.Sort(keys)
		label = strings.Join(keys, ",")
		targets = matches[0].ips
		for _, m := range matches[1:] {
			targets = mergeTargetsUnion(targets, m.ips)
		}
	} else {
		best := matches[0]
		for _, m := range matches[1:] {
			if extdnssrc.HostnameMoreSpecific(m.filter, best.filter) {
				best = m
			} else if !extdnssrc.HostnameMoreSpecific(best.filter, m.filter) && m.nn.String() < best.nn.String() {
				best = m
			}
		}
		label = best.nn.String()
		targets = best.ips
	}
	return buildEndpoints([]string{h}, targets, label)
}

// buildRouteDNSEndpointsForAggregation builds validated EndpointRows for a Route (hostnames → endpoints → ValidateEndpointsByZone).
func (r *genericRouteReconciler[PT, T, PI]) buildRouteDNSEndpointsForAggregation(routeNS string, owner *dns.ResourceRef, parentRefs []gatewayv1.ParentReference,
	parentStatus []gatewayv1.RouteParentStatus, objMeta *metav1.ObjectMeta,
	specHostnames []gatewayv1.Hostname) ([]dns.EndpointRow, map[string]string, error) {
	distinctGWs := r.distinctAcceptedRootGatewayNNs(routeNS, parentRefs, parentStatus)
	if len(distinctGWs) == 0 {
		return nil, nil, nil // no accepted parent Gateway in ipCache
	}

	allowMultiGatewayTargetMerge := len(distinctGWs) >= 2

	desiredHostnames := extdnssrc.RouteHostnames(
		objMeta,
		extdnssrc.RouteSpecHostnames(specHostnames),
		servicecommon.AnnotationDNSHostnameSourceKey,
		servicecommon.AnnotationDNSHostnameKey,
		false,
	)
	var uniqHostnames []string
	if len(desiredHostnames) > 0 {
		uniqHostnames = append([]string(nil), desiredHostnames...)
	} else {
		inferedHostnames, err := r.inferRouteDNSHostnamesFromAcceptedParents(routeNS, parentRefs, parentStatus, objMeta)
		if err != nil {
			return nil, nil, err
		}
		uniqHostnames = append(uniqHostnames, inferedHostnames...)
	}

	if len(uniqHostnames) == 0 {
		return nil, nil, nil
	}

	allowWild := extdnssrc.RouteHostnameWildcardAllowed(objMeta, servicecommon.AnnotationDNSHostnameSourceKey, servicecommon.AnnotationDNSHostnameKey)

	var eps []*extdns.Endpoint
	for _, h := range uniqHostnames {
		eps = append(eps, r.buildRouteDNSEndpointsForHostname(routeNS, h, parentRefs, parentStatus, allowWild, allowMultiGatewayTargetMerge)...)
	}
	rows, allowed, err := r.dns.ValidateEndpointsByZone(objMeta.GetNamespace(), owner, eps)
	return rows, allowed, err
}

// buildEndpoints returns extdns endpoints per hostname; sets EndpointLabelParentGateway to gatewayLabel (comma-separated if merged).
func buildEndpoints(hostnames []string, targets extdns.Targets, parentGatewayLabel string) []*extdns.Endpoint {
	ttl := extdns.TTL(0)
	var out []*extdns.Endpoint
	for _, h := range hostnames {
		for _, ep := range extdns.EndpointsForHostname(h, targets, ttl) {
			if parentGatewayLabel != "" {
				ep.WithLabel(dns.EndpointLabelParentGateway, parentGatewayLabel)
			}
			out = append(out, ep)
		}
	}
	return out
}

type parentGatewayMatch struct {
	nn     types.NamespacedName
	filter string
	ips    extdns.Targets
}

func mergeTargetsUnion(a, b extdns.Targets) extdns.Targets {
	out := make([]string, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return extdns.NewTargets(out...)
}

func (r *genericRouteReconciler[PT, T, PI]) resyncRouteDNS(ctx context.Context, gwNN types.NamespacedName, lsList []gatewayv1.ListenerSet) {
	if r.resyncCh == nil {
		return
	}

	uniqueRoutes := make(map[types.NamespacedName]*T)

	collectRouteFn := func(item *T) {
		obj := r.objCreator(item)
		nn := client.ObjectKeyFromObject(obj)
		if _, exists := uniqueRoutes[nn]; !exists {
			uniqueRoutes[nn] = item
		}
	}

	// 1. Collect routes attached directly to the Gateway
	err := loopObjectList[T](ctx, r.client, r.newList(), collectRouteFn, client.MatchingFields{routeParentGatewayIndex: gwNN.String()})
	if err != nil {
		log.Error(err, "Failed to list routes for resync", "routeKind", r.kind, "Gateway", gwNN.String())
	}

	// 2. Collect routes attached to ListenerSets
	for i := range lsList {
		lsIndex := types.NamespacedName{Namespace: lsList[i].Namespace, Name: lsList[i].Name}.String()
		err := loopObjectList[T](ctx, r.client, r.newList(), collectRouteFn, client.MatchingFields{routeParentListenerSetIndex: lsIndex})
		if err != nil {
			log.Error(err, "Failed to list routes for ListenerSet resync", "routeKind", r.kind, "ListenerSet", lsIndex)
		}
	}

	// 3. Enqueue routes to be reconciled
	for nn, obj := range uniqueRoutes {
		select {
		case r.resyncCh <- event.TypedGenericEvent[*T]{Object: obj}:
		default:
			log.Warn("Failed to enqueue Route DNS resync", "routeKind", r.kind, "Route", nn.String())
		}
	}
}

// registerWatcher adds watches to the Gateway controller builder for this Route type.
// It sets up a raw source channel to receive manual resync events, and watches the Route
// resources themselves, as well as ListenerSets and NetworkInfos if they affect this Route type.
func (r *genericRouteReconciler[PT, T, PI]) registerWatcher(mgr ctrl.Manager, opts controller.Options, listenerSetEnabled bool) error {
	watchCh := make(chan event.TypedGenericEvent[PI], 256)
	creator := r.objCreator(nil)
	b := ctrl.NewControllerManagedBy(mgr).For(creator.GetObject(), builder.WithPredicates(r.predicateFuncsRouteDNS())).WithOptions(opts)

	b.WatchesRawSource(source.Channel(watchCh, &handler.TypedEnqueueRequestForObject[PI]{}))

	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		for {
			select {
			case <-ctx.Done():
				return nil
			case e := <-r.resyncCh:
				watchCh <- event.TypedGenericEvent[PI]{Object: any(e.Object).(PI)}
			}
		}
	})); err != nil {
		return err
	}

	if listenerSetEnabled {
		b.Watches(
			&gatewayv1.ListenerSet{},
			handler.EnqueueRequestsFromMapFunc(r.routeDNSRequestsForListenerSet(r.newList)),
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		)
	}
	b.Watches(
		&v1alpha1.NetworkInfo{},
		handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
			return networkInfoToRouteDNSRequests[T, PI](ctx, r.client, obj, r.kind, r.newList())
		}),
		builder.WithPredicates(predicateNetworkInfoAllowedDNSDomainsChanged()),
	)

	return b.Complete(r)
}

func (r *genericRouteReconciler[PT, T, PI]) getKind() string {
	return r.kind
}

// fetchExistingOwnerNNSet lists listObj via loopObjectList and returns object NNs.
func (r *genericRouteReconciler[PT, T, PI]) fetchExistingOwnerNNSet(ctx context.Context, c client.Client) (sets.Set[types.NamespacedName], error) {
	nnSet := sets.New[types.NamespacedName]()
	err := loopObjectList[T](ctx, c, r.newList(), func(item *T) {
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
