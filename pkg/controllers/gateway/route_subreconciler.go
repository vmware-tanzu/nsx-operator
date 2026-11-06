/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"context"
	"errors"
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
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
	extdnssrc "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/source"
)

const (
	routeParentGatewayIndex        = "routeParentGateway" // Route -> Gateway parent index
	ipCacheStartupWarmRequeueAfter = 2 * time.Second      // Route DNS requeue until ipCache warm
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
				return common.RequeueResultFromReconcileError(derr), nil
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

	observedGeneration := route.GetGeneration()
	batch, hostnameCondition, err := r.buildRouteDNSMergedEndpoints(wrapper)
	if err != nil {
		var zoneValErr *dns.DNSZoneValidationError
		if errors.As(err, &zoneValErr) {
			r.statusUpdater.UpdateFail(ctx, route, err, "DNS record validation failed by the allowed DNS zones", nil)
			log.Error(err, "Route failed DNS zone validation, cleaning up records outside allowed zones", "Route", req.NamespacedName)
			if statusErr := r.updateRouteConditions(ctx, req.NamespacedName, observedGeneration, err, hostnameCondition, true); statusErr != nil {
				log.Error(statusErr, "failed to update Route with DNSRecordReady condition", "route", routeKey)
				return common.ResultRequeueAfter10sec, nil
			}
			if batch != nil && len(batch.Rows) > 0 {
				if _, uErr := r.dns.CreateOrUpdateRecords(ctx, batch); uErr != nil {
					log.Error(uErr, "Unable to apply valid records despite validation errors", "Route", req.NamespacedName.String())
					return common.RequeueResultFromReconcileError(uErr), nil
				}
			} else {
				if _, derr := r.dns.DeleteRecordByOwnerNN(ctx, r.kind, req.Namespace, req.Name); derr != nil {
					log.Error(derr, "Unable to delete the existing disallowed records", "Route", req.NamespacedName.String())
					return common.RequeueResultFromReconcileError(derr), nil
				}
			}
			return common.ResultNormal, nil
		}
		r.statusUpdater.UpdateFail(ctx, route, err, "DNS record reconcile failed", nil)
		log.Error(err, "failed to build endpoints for Route", "route", routeKey)
		if statusErr := r.updateRouteConditions(ctx, req.NamespacedName, observedGeneration, err, hostnameCondition, true); statusErr != nil {
			log.Error(statusErr, "failed to update Route with DNSRecordReady condition", "route", routeKey)
			return common.ResultRequeueAfter10sec, nil
		}
		return common.ResultRequeueAfter10sec, nil
	}

	if batch == nil || len(batch.Rows) == 0 {
		log.Info("No DNS record are required for route, cleaning up the stale records", "kind", route.GetObjectKind(), "route", req.NamespacedName)
		_, derr := r.dns.DeleteRecordByOwnerNN(ctx, r.kind, req.Namespace, req.Name)
		if statusErr := r.updateRouteConditions(ctx, req.NamespacedName, observedGeneration, derr, hostnameCondition, false); statusErr != nil {
			log.Error(statusErr, "failed to update Route status conditions", "route", routeKey)
			return common.ResultRequeueAfter10sec, nil
		}
		if derr != nil {
			log.Error(derr, "failed to clean up DNS records for route", "route", routeKey)
			return common.RequeueResultFromReconcileError(derr), nil
		}
		return common.ResultNormal, nil
	}
	_, opErr := r.dns.CreateOrUpdateRecords(ctx, batch)
	if statusErr := r.updateRouteConditions(ctx, req.NamespacedName, observedGeneration, opErr, hostnameCondition, true); statusErr != nil {
		log.Error(statusErr, "failed to update Route status conditions", "route", routeKey)
		return common.ResultRequeueAfter10sec, nil
	}
	if opErr != nil {
		r.statusUpdater.UpdateFail(ctx, route, opErr, "DNS record reconcile failed", nil)
		log.Error(opErr, "DNS record reconcile failed", "route", routeKey)
		return common.RequeueResultFromReconcileError(opErr), nil
	}

	r.statusUpdater.UpdateSuccess(ctx, route, nil)
	return common.ResultNormal, nil
}

// buildRouteDNSMergedEndpoints builds owner-scoped Route DNS rows (Gateway or ListenerSet→root Gateway, same rules as aggregation).
func (r *genericRouteReconciler[PT, T, PI]) buildRouteDNSMergedEndpoints(route PT) (*dns.AggregatedDNSEndpoints, *metav1.Condition, error) {
	owner := route.GetResourceRef()
	if owner == nil {
		return nil, nil, nil
	}
	rows, _, admission, err := r.buildRouteDNSEndpointsForAggregation(route.GetNamespace(), owner, route.GetParentRefs(), route.GetRouteParentStatus(), route.GetObjectMeta(), route.GetSpecHostnames())
	hostnameCondition := buildRouteHostnameAcceptedCondition(admission)
	if len(rows) == 0 {
		return nil, hostnameCondition, err
	}
	return dns.NewOwnerScopedAggregatedRouteDNS(owner, rows), hostnameCondition, err
}

// updateRouteConditions updates HostnameAccepted and reports DNS readiness only for
// generated records or an operation failure. Conditions refer to the generation
// processed by this reconcile, even if the Route changes before the status write.
// Successful empty batches remove DNSRecordReady.
func (r *genericRouteReconciler[PT, T, PI]) updateRouteConditions(ctx context.Context, routeKey types.NamespacedName, observedGeneration int64, condErr error, hostnameCondition *metav1.Condition, hasDNSRecords bool) error {
	dnsConditionEnabled := namespaceNetworkInfoExists(ctx, r.client, routeKey.Namespace)
	if !dnsConditionEnabled && hostnameCondition == nil {
		log.Info("No NetworkInfo found in route namespace; skipping DNSRecordReady status update", "Route", routeKey)
		return nil
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		creator := r.objCreator(nil)
		route := creator.GetObject()
		if getErr := r.client.Get(ctx, routeKey, route); getErr != nil {
			return client.IgnoreNotFound(getErr)
		}

		parents := creator.GetRouteParentStatus()
		conditions := make([]metav1.Condition, 0, 2)
		hostnameRejected := hostnameCondition != nil && hostnameCondition.Status == metav1.ConditionFalse
		dnsConditionRequired := !hostnameRejected && (hasDNSRecords || condErr != nil)
		if dnsConditionEnabled && dnsConditionRequired {
			dnsCondition := buildDNSRecordReadyCondition(condErr)
			dnsCondition.ObservedGeneration = observedGeneration
			conditions = append(conditions, dnsCondition)
		}
		if hostnameCondition != nil {
			hostnameCond := *hostnameCondition
			hostnameCond.ObservedGeneration = observedGeneration
			conditions = append(conditions, hostnameCond)
		}

		for i := range parents {
			if parents[i].ControllerName == nsxOperatorGatewayDNSController {
				changed := false
				if !dnsConditionRequired {
					changed = meta.RemoveStatusCondition(&parents[i].Conditions, conditionTypeDNSRecordReady) || changed
				}
				for _, condition := range conditions {
					changed = meta.SetStatusCondition(&parents[i].Conditions, condition) || changed
				}
				if changed {
					return r.client.Status().Update(ctx, route)
				}
				return nil
			}
		}

		parents = append(parents, gatewayv1.RouteParentStatus{
			ControllerName: nsxOperatorGatewayDNSController,
			ParentRef:      networkInfoParentRef(routeKey.Namespace),
			Conditions:     conditions,
		})
		creator.SetRouteParentStatus(parents)
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
	parentStatus []gatewayv1.RouteParentStatus) []string {
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
		if r.ipCache == nil {
			continue
		}
		ent, ok := r.ipCache.get(gwNN)
		if !ok {
			continue
		}
		filters := extdnssrc.AdmissionHostnameFiltersForRouteParentFromRows(ent.AdmissionRows, &ref, routeNS)
		inh := extdnssrc.RouteHostnamesMatchingAdmission(filters, []string{""})
		seen.Insert(inh...)
	}
	return sets.List(seen)
}

type routeHostnameAdmissionResult struct {
	hasUsableAcceptedParentGateway bool
	unsupportedWildcard            bool
	missingAddress                 bool
	hasDNSEndpoints                bool
}

// buildRouteHostnameAcceptedCondition accepts the Route when at least one hostname
// can generate a concrete DNS endpoint through a usable parent listener. Zone
// authorization, apex restrictions, and infrastructure failures are reported by DNSRecordReady.
func buildRouteHostnameAcceptedCondition(result routeHostnameAdmissionResult) *metav1.Condition {
	status := metav1.ConditionFalse
	reason := reasonFQDNsNotMatchParentListener
	message := "No Route FQDN matches a hostname configured on its parent listeners."
	switch {
	case result.hasDNSEndpoints:
		status = metav1.ConditionTrue
		reason = reasonDNSHostnamesAccepted
		message = "At least one Route hostname matches a usable parent listener and is supported for DNS record generation."
	case !result.hasUsableAcceptedParentGateway || result.missingAddress:
		reason = reasonNoValidParentGateways
		message = messageNoValidParentGateways
	case result.unsupportedWildcard:
		reason = reasonWildcardDNSNameUnsupported
		message = "Wildcard Route hostnames are not supported for NSX DNS record generation."
	}
	return &metav1.Condition{
		Type:               conditionTypeHostnameAccepted,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}
}

// buildRouteDNSEndpointsForHostname returns DNS endpoints and whether h matches a parent listener.
func (r *genericRouteReconciler[PT, T, PI]) buildRouteDNSEndpointsForHostname(routeNS, h string,
	parentRefs []gatewayv1.ParentReference, parentStatus []gatewayv1.RouteParentStatus,
	allowMultiGatewayTargetMerge bool) ([]*extdns.Endpoint, bool) {
	wildcardDNSName := strings.HasPrefix(h, "*.")
	var matches []parentGatewayMatch
	listenerMatched := false
	for i := range parentRefs {
		ref := parentRefs[i]
		if !extdnssrc.RouteAcceptedForParentRef(parentStatus, routeNS, ref) {
			continue
		}
		gwNN, ok := r.resolveParentRefToRootGatewayNN(routeNS, &ref)
		if !ok {
			continue
		}
		if r.ipCache == nil {
			continue
		}
		ent, ok := r.ipCache.get(gwNN)
		if !ok {
			continue
		}
		filters := extdnssrc.AdmissionHostnameFiltersForRouteParentFromRows(ent.AdmissionRows, &ref, routeNS)
		f, okMatch := extdnssrc.BestMatchingAdmissionFilter(filters, h)
		if !okMatch {
			continue
		}
		listenerMatched = true
		if wildcardDNSName || len(ent.IPs) == 0 {
			continue
		}
		matches = append(matches, parentGatewayMatch{nn: gwNN, filter: f, ips: ent.IPs})
	}
	if len(matches) == 0 {
		return nil, listenerMatched
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
	return buildEndpoints([]string{h}, targets, label), listenerMatched
}

// buildRouteDNSEndpointsForAggregation builds validated EndpointRows for a Route (hostnames → endpoints → ValidateEndpointsByZone).
func (r *genericRouteReconciler[PT, T, PI]) buildRouteDNSEndpointsForAggregation(routeNS string, owner *dns.ResourceRef, parentRefs []gatewayv1.ParentReference,
	parentStatus []gatewayv1.RouteParentStatus, objMeta *metav1.ObjectMeta,
	specHostnames []gatewayv1.Hostname) ([]dns.EndpointRow, map[string]string, routeHostnameAdmissionResult, error) {
	distinctGWs := r.distinctAcceptedRootGatewayNNs(routeNS, parentRefs, parentStatus)
	admission := routeHostnameAdmissionResult{}
	if r.ipCache != nil {
		for _, gwNN := range distinctGWs {
			if entry, ok := r.ipCache.get(gwNN); ok && len(entry.IPs) > 0 {
				admission.hasUsableAcceptedParentGateway = true
				break
			}
		}
	}
	if len(distinctGWs) == 0 {
		return nil, nil, admission, nil // no accepted parent Gateway can be resolved
	}

	allowMultiGatewayTargetMerge := len(distinctGWs) >= 2

	desiredHostnames := extdnssrc.RouteHostnames(
		objMeta,
		extdnssrc.RouteSpecHostnames(specHostnames),
		false,
	)
	var uniqHostnames []string
	if len(desiredHostnames) > 0 {
		uniqHostnames = append([]string(nil), desiredHostnames...)
	} else if extdnssrc.RouteHostnameInferenceAllowed(objMeta.GetAnnotations()) {
		inferredHostnames := r.inferRouteDNSHostnamesFromAcceptedParents(routeNS, parentRefs, parentStatus)
		uniqHostnames = append(uniqHostnames, inferredHostnames...)
	}

	if len(uniqHostnames) == 0 {
		return nil, nil, admission, nil
	}

	var eps []*extdns.Endpoint
	for _, h := range uniqHostnames {
		hostnameEndpoints, listenerMatched := r.buildRouteDNSEndpointsForHostname(routeNS, h, parentRefs, parentStatus, allowMultiGatewayTargetMerge)
		eps = append(eps, hostnameEndpoints...)
		wildcard := strings.HasPrefix(h, "*.")
		admission.unsupportedWildcard = admission.unsupportedWildcard || (listenerMatched && wildcard)
		if listenerMatched && !wildcard && len(hostnameEndpoints) == 0 {
			admission.missingAddress = true
		}
	}
	rows, allowed, err := r.dns.ValidateEndpointsByZone(objMeta.GetNamespace(), owner, eps)
	admission.hasDNSEndpoints = len(eps) > 0
	return rows, allowed, admission, err
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

	collectRouteFn := func(item PI) {
		obj := r.objCreator(item)
		nn := client.ObjectKeyFromObject(obj)
		if _, exists := uniqueRoutes[nn]; !exists {
			uniqueRoutes[nn] = item
		}
	}

	// 1. Collect routes attached directly to the Gateway
	err := loopObjectList[T, PI](ctx, r.client, r.newList(), collectRouteFn, client.MatchingFields{routeParentGatewayIndex: gwNN.String()})
	if err != nil {
		log.Error(err, "Failed to list routes for resync", "routeKind", r.kind, "Gateway", gwNN.String())
	}

	// 2. Collect routes attached to ListenerSets
	for i := range lsList {
		lsIndex := types.NamespacedName{Namespace: lsList[i].Namespace, Name: lsList[i].Name}.String()
		err := loopObjectList[T, PI](ctx, r.client, r.newList(), collectRouteFn, client.MatchingFields{routeParentListenerSetIndex: lsIndex})
		if err != nil {
			log.Error(err, "Failed to list routes for ListenerSet resync", "routeKind", r.kind, "ListenerSet", lsIndex)
		}
	}

	// 3. Enqueue routes to be reconciled
	for nn, obj := range uniqueRoutes {
		select {
		case r.resyncCh <- event.TypedGenericEvent[*T]{Object: obj}:
		default:
			// Non-blocking enqueue is an intentional best-effort tradeoff. Under extreme load,
			// an update to a Gateway might fail to immediately trigger a Route reconcile.
			// The Route will eventually pick up the changes on its next resync or update.
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
		builder.WithPredicates(predicateNetworkInfoAllowedDNSRecordDomainsChanged()),
	)

	return b.Complete(r)
}

func (r *genericRouteReconciler[PT, T, PI]) getKind() string {
	return r.kind
}

// fetchExistingOwnerNNSet lists listObj via loopObjectList and returns object NNs.
func (r *genericRouteReconciler[PT, T, PI]) fetchExistingOwnerNNSet(ctx context.Context, c client.Client) (sets.Set[types.NamespacedName], error) {
	nnSet := sets.New[types.NamespacedName]()
	err := loopObjectList[T, PI](ctx, c, r.newList(), func(o PI) {
		nnSet.Insert(client.ObjectKeyFromObject(o))
	})

	if err != nil {
		return nil, err
	}
	return nnSet, nil
}
