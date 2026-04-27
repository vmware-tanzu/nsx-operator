/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"context"
	"fmt"
	"slices"
	"strings"
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
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
	extdnssrc "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/source"
)

const routeParentGatewayIndex = "routeParentGateway" // Route -> Gateway parent index

const ipCacheStartupWarmRequeueAfter = 500 * time.Millisecond // Route DNS requeue until ipCache warm

func routeParentGatewayIndexFunc(obj client.Object) []string {
	var ns string
	var refs []gatewayv1.ParentReference
	switch o := obj.(type) {
	case *gatewayv1.HTTPRoute:
		ns, refs = o.Namespace, o.Spec.ParentRefs
	case *gatewayv1.GRPCRoute:
		ns, refs = o.Namespace, o.Spec.ParentRefs
	case *gatewayv1.TLSRoute:
		ns, refs = o.Namespace, o.Spec.ParentRefs
	default:
		return nil
	}
	parents := parentGatewaysFromParentRefs(ns, refs)
	if len(parents) == 0 {
		return nil
	}
	keys := make([]string, len(parents))
	for i, p := range parents {
		keys[i] = p.String()
	}
	return keys
}

func (a routeReconcilerAdapter[PT, T]) mergeRouteParentDNSCondition(_ context.Context, cond metav1.Condition, routeNS string,
	observedGen int64, parents []gatewayv1.RouteParentStatus) bool {
	c := cond
	c.ObservedGeneration = observedGen
	changed := false
	for i := range parents {
		ref := &parents[i].ParentRef
		managed, supportedKind := a.reconciler.routeParentManagedInIPCache(routeNS, ref)
		if !supportedKind {
			continue // not Gateway / ListenerSet parent
		}
		if managed {
			if meta.SetStatusCondition(&parents[i].Conditions, c) {
				changed = true
			}
		} else {
			if meta.RemoveStatusCondition(&parents[i].Conditions, conditionTypeDNSConfig) {
				changed = true
			}
		}
	}
	return changed
}

// distinctAcceptedRootGatewayNNs returns unique root Gateway NNs for Accepted parent refs.
func (r *GatewayReconciler) distinctAcceptedRootGatewayNNs(routeNS string,
	refs []gatewayv1.ParentReference, status []gatewayv1.RouteParentStatus) []types.NamespacedName {
	seen := sets.New[string]()
	var out []types.NamespacedName
	for i := range refs {
		ref := refs[i]
		if !extdnssrc.RouteAcceptedForParentRef(status, routeNS, ref) {
			continue
		}
		gwNN, ok := r.resolveParentRefToRootGatewayNN(routeNS, &ref)
		if !ok {
			continue
		}
		if seen.Has(gwNN.String()) {
			continue
		}
		seen.Insert(gwNN.String())
		out = append(out, gwNN)
	}
	return out
}

// inferRouteDNSHostnamesFromAcceptedParents fills hostnames from listener admission when uniq is still empty.
func (r *GatewayReconciler) inferRouteDNSHostnamesFromAcceptedParents(routeNS string, parentRefs []gatewayv1.ParentReference,
	parentStatus []gatewayv1.RouteParentStatus, objMeta *metav1.ObjectMeta,
	seen sets.Set[string], uniq []string) ([]string, error) {
	if len(uniq) > 0 {
		return uniq, nil
	}
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
		for _, h := range inh {
			h = strings.TrimSpace(h)
			if h == "" || seen.Has(h) {
				continue
			}
			seen.Insert(h)
			uniq = append(uniq, h)
		}
	}
	return uniq, nil
}

// appendAggregatedRouteDNSEndpointForHostname appends one ExternalDNS endpoint for hostname h into eps.
func (r *GatewayReconciler) appendAggregatedRouteDNSEndpointForHostname(routeNS, h string,
	parentRefs []gatewayv1.ParentReference, parentStatus []gatewayv1.RouteParentStatus,
	allowWild, allowMultiGatewayTargetMerge bool, eps *[]*extdns.Endpoint) error {
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
	buildEndpoints(eps, []string{h}, targets, label)
	return nil
}

// buildRouteDNSEndpointsForAggregation builds validated EndpointRows for a Route (hostnames → endpoints → ValidateEndpointsByDNSZone).
func (r *GatewayReconciler) buildRouteDNSEndpointsForAggregation(rw dns.RouteDNSWrite, routeNS string, owner *dns.ResourceRef, parentRefs []gatewayv1.ParentReference,
	parentStatus []gatewayv1.RouteParentStatus, objMeta *metav1.ObjectMeta,
	specHostnames []gatewayv1.Hostname) ([]dns.EndpointRow, error) {
	distinctGWs := r.distinctAcceptedRootGatewayNNs(routeNS, parentRefs, parentStatus)
	if len(distinctGWs) == 0 {
		return nil, nil // no accepted parent Gateway in ipCache
	}

	allowMultiGatewayTargetMerge := len(distinctGWs) >= 2

	desiredHostnames := extdnssrc.RouteHostnames(
		objMeta,
		extdnssrc.RouteSpecHostnames(specHostnames),
		servicecommon.AnnotationDNSHostnameSourceKey,
		servicecommon.AnnotationDNSHostnameKey,
		false,
	)
	seen := sets.New[string]()
	var uniqhostnames []string
	for _, h := range desiredHostnames {
		h = strings.TrimSpace(h)
		if h == "" || seen.Has(h) {
			continue
		}
		seen.Insert(h)
		uniqhostnames = append(uniqhostnames, h)
	}
	var err error
	uniqhostnames, err = r.inferRouteDNSHostnamesFromAcceptedParents(routeNS, parentRefs, parentStatus, objMeta, seen, uniqhostnames)
	if err != nil {
		return nil, err
	}
	if len(uniqhostnames) == 0 {
		return nil, nil
	}
	allowWild := extdnssrc.RouteHostnameWildcardAllowed(objMeta, servicecommon.AnnotationDNSHostnameSourceKey, servicecommon.AnnotationDNSHostnameKey)

	var eps []*extdns.Endpoint
	for _, h := range uniqhostnames {
		if err := r.appendAggregatedRouteDNSEndpointForHostname(routeNS, h, parentRefs, parentStatus, allowWild, allowMultiGatewayTargetMerge, &eps); err != nil {
			return nil, err
		}
	}
	return rw.ValidateEndpointsByDNSZone(objMeta.GetNamespace(), owner, eps)
}

// routeReconcilerAdapter implements reconcile.Reconciler for one Route kind (HTTP/GRPC/TLS) using wrapper PT.
type routeReconcilerAdapter[PT RouteObject[T], T any] struct {
	reconciler *GatewayReconciler
	kind       string
	wrapper    func() PT
	dnsWriter  dns.RouteDNSWrite
}

// Reconcile runs reconcileRouteDNS with a fresh PT from wrapper.
func (a routeReconcilerAdapter[PT, T]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	wrapper := a.wrapper()
	return a.reconcileRouteDNS(ctx, req, wrapper)
}

// reconcileRouteDNS upserts or deletes DNS store rows for one Route; returns ctrl.Result, err.
func (a routeReconcilerAdapter[PT, T]) reconcileRouteDNS(ctx context.Context, req ctrl.Request, wrapper PT) (ctrl.Result, error) {
	routeKey := req.NamespacedName.String()
	route := wrapper.GetBaseObject()

	if err := a.reconciler.Client.Get(ctx, req.NamespacedName, route); err != nil {
		if apierrors.IsNotFound(err) { // Route gone: drop DNS owner rows
			if _, derr := a.dnsWriter.DeleteDNSRecordByOwnerNN(ctx, a.kind, req.Namespace, req.Name); derr != nil {
				return common.ResultRequeueAfter10sec, fmt.Errorf("reconcile DNS for route %s: delete store records: %w", routeKey, derr)
			}
			return common.ResultNormal, nil
		}
		return common.ResultRequeueAfter10sec, fmt.Errorf("reconcile DNS for route %s: get route: %w", routeKey, err)
	}

	if !a.reconciler.ipCacheWarmedOnStartup.Load() { // wait for Gateway ipCache warm
		log.Debug("Deferring Route DNS reconcile until Gateway ipCache startup warm completes", "Route", req.NamespacedName)
		return ctrl.Result{Requeue: true, RequeueAfter: ipCacheStartupWarmRequeueAfter}, nil
	}

	batch, err := a.buildRouteDNSMergedEndpoints(wrapper)
	if err != nil {
		a.reconciler.StatusUpdater.UpdateFail(ctx, route, err, "DNS record reconcile failed", nil)
		return common.ResultRequeueAfter10sec, fmt.Errorf("reconcile DNS for route %s: build endpoints: %w", routeKey, err)
	}

	if batch == nil { // no endpoints for this Route
		log.Info("No DNS record are required for route, deleting the stale records", "kind", route.GetObjectKind(), "route", req.NamespacedName)
		if _, derr := a.dnsWriter.DeleteDNSRecordByOwnerNN(ctx, a.kind, req.Namespace, req.Name); derr != nil {
			return common.ResultRequeueAfter10sec, fmt.Errorf("reconcile DNS for route %s: delete store records: %w", routeKey, derr)
		}
		return common.ResultNormal, nil
	}

	cacheChanged, opErr := a.dnsWriter.CreateOrUpdateDNSRecords(ctx, batch)
	if opErr != nil {
		a.reconciler.StatusUpdater.UpdateFail(ctx, route, opErr, "DNS record reconcile failed", nil)
		statusErr := a.updateRouteParentConditions(ctx, req.NamespacedName, opErr)
		if statusErr != nil {
			return common.ResultRequeueAfter10sec, fmt.Errorf("reconcile DNS for route %s: update Route DNSConfig status: %w", routeKey, statusErr)
		}
		return common.ResultRequeueAfter10sec, opErr
	}
	if !cacheChanged {
		a.reconciler.StatusUpdater.UpdateSuccess(ctx, route, nil)
		return common.ResultNormal, nil
	}
	statusErr := a.updateRouteParentConditions(ctx, req.NamespacedName, nil)
	if statusErr != nil {
		return common.ResultRequeueAfter10sec, fmt.Errorf("reconcile DNS for route %s: update Route DNSConfig status: %w", routeKey, statusErr)
	}
	a.reconciler.StatusUpdater.UpdateSuccess(ctx, route, nil)
	return common.ResultNormal, nil
}

func (a routeReconcilerAdapter[PT, T]) updateRouteParentConditions(ctx context.Context, routeKey types.NamespacedName, err error) error {
	cond := buildDNSConfigCondition(err)

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		wrapper := a.wrapper()
		route := wrapper.GetBaseObject()
		if err = a.reconciler.Client.Get(ctx, routeKey, route); err != nil {
			return client.IgnoreNotFound(err)
		}
		routeNS := wrapper.GetNamespace()
		parents := wrapper.GetRouteParentStatus()
		changed := a.mergeRouteParentDNSCondition(ctx, cond, routeNS, wrapper.GetGeneration(), parents)
		if !changed {
			return nil
		}
		return a.reconciler.Client.Status().Update(ctx, route)
	})
}

// registerRouteDNSControllers registers Route DNS controllers for installed Route CRDs.
func (r *GatewayReconciler) registerRouteDNSControllers(mgr ctrl.Manager) error {
	opts := controller.Options{MaxConcurrentReconciles: common.NumReconcile()}
	if r.apiResources.httpRoute {
		r.httpRouteDNSResyncCh = make(chan event.TypedGenericEvent[*gatewayv1.HTTPRoute], 256)
		httpRouteMgrBuilder := ctrl.NewControllerManagedBy(mgr).WatchesRawSource(source.Channel(r.httpRouteDNSResyncCh, &handler.TypedEnqueueRequestForObject[*gatewayv1.HTTPRoute]{}))
		if err := registerRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute](r, dns.ResourceKindHTTPRoute, func() *HTTPRoute { return &HTTPRoute{} }, httpRouteMgrBuilder, opts); err != nil {
			return err
		}
	}
	if r.apiResources.grpcRoute {
		r.grpcRouteDNSResyncCh = make(chan event.TypedGenericEvent[*gatewayv1.GRPCRoute], 256)
		grpcRouteMgrBuilder := ctrl.NewControllerManagedBy(mgr).WatchesRawSource(source.Channel(r.grpcRouteDNSResyncCh, &handler.TypedEnqueueRequestForObject[*gatewayv1.GRPCRoute]{}))
		if err := registerRouteReconciler[*GRPCRoute, gatewayv1.GRPCRoute](r, dns.ResourceKindGRPCRoute, func() *GRPCRoute { return &GRPCRoute{} }, grpcRouteMgrBuilder, opts); err != nil {
			return err
		}
	}
	if r.apiResources.tlsRoute {
		r.tlsRouteDNSResyncCh = make(chan event.TypedGenericEvent[*gatewayv1.TLSRoute], 256)
		tlsRouteMgrBuilder := ctrl.NewControllerManagedBy(mgr).WatchesRawSource(source.Channel(r.tlsRouteDNSResyncCh, &handler.TypedEnqueueRequestForObject[*gatewayv1.TLSRoute]{}))
		if err := registerRouteReconciler[*TLSRoute, gatewayv1.TLSRoute](r, dns.ResourceKindTLSRoute, func() *TLSRoute { return &TLSRoute{} }, tlsRouteMgrBuilder, opts); err != nil {
			return err
		}
	}
	return nil
}

func newRouteReconciler[PT RouteObject[T], T any](r *GatewayReconciler, kind string, wrapperFn func() PT) *routeReconcilerAdapter[PT, T] {
	return &routeReconcilerAdapter[PT, T]{reconciler: r, kind: kind, dnsWriter: r.Service, wrapper: wrapperFn}
}

func registerRouteReconciler[PT RouteObject[T], T any](r *GatewayReconciler, kind string, wrapperFn func() PT, mgrBuilder *builder.TypedBuilder[reconcile.Request], opts controller.Options) error {
	reconciler := newRouteReconciler[PT, T](r, kind, wrapperFn)
	wrapper := wrapperFn()
	b := mgrBuilder.Named("gateway-"+kind+"-dns").
		For(wrapper.GetBaseObject(), builder.WithPredicates(reconciler.predicateFuncsRouteDNS())).
		WithOptions(opts)
	if r.apiResources.listenerSet {
		b = b.Watches(
			&gatewayv1.ListenerSet{},
			handler.EnqueueRequestsFromMapFunc(r.routeDNSRequestsForListenerSet(kind)),
			builder.WithPredicates(predicate.GenerationChangedPredicate{}, predicate.AnnotationChangedPredicate{}),
		)
	}
	return b.Complete(reconciler)
}
