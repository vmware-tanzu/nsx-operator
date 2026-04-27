/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"cmp"
	"context"
	"net"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	crmanager "sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
	extdnssrc "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/source"
)

const (
	listenerSetParentGatewayIndex = "listenerSetParentGateway" // ListenerSet -> parent Gateway cache index
	conditionTypeDNSConfig        = "DNSConfig"                // per-parent Route DNS status
	reasonDNSRecordConfigured     = "DNSRecordConfigured"
	reasonDNSRecordFailed         = "DNSRecordFailed"
	gatewayAPIGroupVersion        = "gateway.networking.k8s.io/v1"
)

var (
	log                     = logger.Log
	filteredGatewayClasses  = sets.New[string](common.ManagedK8sGatewayClassIstio, common.ManagedK8sGatewayClassAviLB)
	gatewaySetupWithManager = func(r *GatewayReconciler, mgr ctrl.Manager) error {
		return r.setupWithManager(mgr)
	}
)

// GatewayReconciler reconciles Gateway API Gateways: ipCache, annotation DNS, DNS store GC, Route resync hooks.
type GatewayReconciler struct {
	Client        client.Client
	Scheme        *apimachineryruntime.Scheme
	Recorder      record.EventRecorder
	Service       *dns.DNSRecordService
	StatusUpdater StatusUpdater

	apiResources           gatewayAPIResources // from discovery: which Gateway API kinds exist
	discoveryClient        discovery.DiscoveryInterface
	ipCache                *gatewayIPCache
	ipCacheWarmedOnStartup atomic.Bool // set after startup warm; Route DNS waits on this

	httpRouteDNSResyncCh chan event.TypedGenericEvent[*gatewayv1.HTTPRoute]
	grpcRouteDNSResyncCh chan event.TypedGenericEvent[*gatewayv1.GRPCRoute]
	tlsRouteDNSResyncCh  chan event.TypedGenericEvent[*gatewayv1.TLSRoute]
}

// Reconcile syncs Gateway → ipCache, direct DNS from annotations, store GC, and Route resync triggers.
func (r *GatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startTime := time.Now()
	defer func() {
		log.Debug("Finished reconciling Gateway", "Gateway", req.NamespacedName, "duration(ms)", time.Since(startTime).Milliseconds())
	}()

	log.Debug("Reconcile started", "Gateway", req.NamespacedName)
	r.StatusUpdater.IncreaseSyncTotal()
	gw := &gatewayv1.Gateway{}
	if err := r.Client.Get(ctx, req.NamespacedName, gw); err != nil {
		if apierrors.IsNotFound(err) { // delete path
			log.Info("Gateway not found", "Gateway", req.NamespacedName)
			gw.SetName(req.Name)
			gw.SetNamespace(req.Namespace)
			return r.deleteUnmanagedGateway(ctx, gw, req, false)
		}
		log.Error(err, "Failed to fetch Gateway", "Gateway", req.NamespacedName)
		return common.ResultRequeueAfter10sec, err
	}

	log.Debug("Gateway loaded", "Gateway", req.NamespacedName, "class", gw.Spec.GatewayClassName,
		"hasDeletionTimestamp", !gw.DeletionTimestamp.IsZero(), "addressCount", len(gw.Status.Addresses), "listenerCount", len(gw.Spec.Listeners))

	if !gw.DeletionTimestamp.IsZero() {
		log.Info("Reconciling Gateway delete", "Gateway", req.NamespacedName)
		return r.deleteUnmanagedGateway(ctx, gw, req, false)
	}

	if !shouldProcessGateway(gw) {
		log.Info("Gateway is no longer managed, deleting DNS records", "Gateway", req.NamespacedName)
		return r.deleteUnmanagedGateway(ctx, gw, req, true)
	}

	if !hasUsableGatewayIP(gw) {
		log.Info("Gateway has no valid address, DNS records should be deleted", "Gateway", req.NamespacedName)
		return r.deleteUnmanagedGateway(ctx, gw, req, true)
	}

	nn := req.NamespacedName
	var ls []gatewayv1.ListenerSet
	var err error
	if r.apiResources.listenerSet {
		ls, err = r.listSortedListenerSetsForGateway(ctx, nn)
		if err != nil {
			log.Error(err, "Failed to list ListenerSets for Gateway DNS cache", "Gateway", nn.String())
			return common.ResultRequeueAfter10sec, err
		}
	}

	r.StatusUpdater.IncreaseUpdateTotal()

	entry, gatewayDNSCacheChanged := r.refreshGatewayIPCache(gw, ls)
	gwEndpoints, err := r.collectGatewayEndpointsByAnnotation(gw, entry.IPs)
	if err != nil {
		log.Error(err, "Failed to build DNS endpoints for Gateway", "Gateway", req.NamespacedName.String())
		if uerr := r.updateGatewayDNSReadyCondition(ctx, req.NamespacedName, err); uerr != nil {
			log.Error(uerr, "Failed to update Gateway DNSReady condition", "Gateway", req.NamespacedName.String())
		}
		r.StatusUpdater.UpdateFail(ctx, gw, err, "DNS record build failed", nil)
		return common.ResultRequeueAfter10sec, err
	}

	if gwEndpoints == nil {
		log.Debug("No DNS Records are claimed for Gateway", "Gateway", req.NamespacedName)
		return common.ResultNormal, nil
	}

	cacheChanged, updateErr := r.Service.CreateOrUpdateDNSRecords(ctx, gwEndpoints)
	if updateErr != nil {
		r.StatusUpdater.UpdateFail(ctx, gw, updateErr, "DNS record create/update failed", nil)
		return common.ResultRequeueAfter10sec, updateErr
	}

	if cacheChanged {
		if uerr := r.updateGatewayDNSReadyCondition(ctx, req.NamespacedName, nil); uerr != nil {
			log.Error(uerr, "Failed to update Gateway DNSReady condition", "Gateway", req.NamespacedName.String())
			return common.ResultRequeueAfter10sec, uerr
		}
	}

	r.StatusUpdater.UpdateSuccess(ctx, gw, nil)
	log.Info("Reconciling Gateway", "Gateway", req.NamespacedName, "dnsRecordsCount", len(gwEndpoints.Rows))
	if gatewayDNSCacheChanged {
		r.enqueueAttachedRoutesForGatewayDNSFromAPI(ctx, nn)
	}
	return common.ResultNormal, nil
}

// deleteUnmanagedGateway deletes Gateway DNS rows, drops ipCache, optionally clears DNSConfig status; returns Result, err.
func (r *GatewayReconciler) deleteUnmanagedGateway(ctx context.Context, gw *gatewayv1.Gateway, req ctrl.Request, clearDNSConfigConditions bool) (ctrl.Result, error) {
	gwNN := types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}
	hadIPCacheEntry := r.ipCache.delete(gwNN)
	if hadIPCacheEntry {
		r.enqueueAttachedRoutesForGatewayDNSFromAPI(ctx, gwNN)
	}

	r.StatusUpdater.IncreaseDeleteTotal()
	if _, err := r.Service.DeleteDNSRecordByOwnerNN(ctx, dns.ResourceKindGateway, gw.Namespace, gw.Name); err != nil {
		r.StatusUpdater.DeleteFail(req.NamespacedName, gw, err)
		log.Error(err, "Failed to delete DNS records for Gateway", "Gateway", req.NamespacedName)
		return common.ResultRequeueAfter10sec, err
	}
	if clearDNSConfigConditions {
		if uerr := r.removeGatewayDNSConfigCondition(ctx, gwNN); uerr != nil {
			log.Error(uerr, "Failed to clear Gateway DNSReady condition", "Gateway", req.NamespacedName)
			return common.ResultRequeueAfter10sec, uerr
		}
	}

	r.StatusUpdater.DeleteSuccess(req.NamespacedName, gw)
	return common.ResultNormal, nil
}

// enqueueAttachedRoutesForGatewayDNSFromAPI enqueues Route DNS resyncs for Routes attached to gwNN.
func (r *GatewayReconciler) enqueueAttachedRoutesForGatewayDNSFromAPI(ctx context.Context, gwNN types.NamespacedName) {
	resyncRouteDNS[gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](ctx, r.Client, gwNN, r.apiResources.httpRoute, r.httpRouteDNSResyncCh, "HTTPRoute", &gatewayv1.HTTPRouteList{})
	resyncRouteDNS[gatewayv1.GRPCRoute, *gatewayv1.GRPCRoute](ctx, r.Client, gwNN, r.apiResources.grpcRoute, r.grpcRouteDNSResyncCh, "GRPCRoute", &gatewayv1.GRPCRouteList{})
	resyncRouteDNS[gatewayv1.TLSRoute, *gatewayv1.TLSRoute](ctx, r.Client, gwNN, r.apiResources.tlsRoute, r.tlsRouteDNSResyncCh, "TLSRoute", &gatewayv1.TLSRouteList{})
	if !r.apiResources.listenerSet {
		return
	}
	lsList, err := r.listSortedListenerSetsForGateway(ctx, gwNN)
	if err != nil {
		log.Error(err, "Failed to list ListenerSets for Route DNS resync", "Gateway", gwNN.String())
		return
	}
	for i := range lsList {
		idx := types.NamespacedName{Namespace: lsList[i].Namespace, Name: lsList[i].Name}.String()
		resyncRouteDNSByListenerSetIndex[gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](ctx, r.Client, idx, r.apiResources.httpRoute, r.httpRouteDNSResyncCh, "HTTPRoute", &gatewayv1.HTTPRouteList{})
		resyncRouteDNSByListenerSetIndex[gatewayv1.GRPCRoute, *gatewayv1.GRPCRoute](ctx, r.Client, idx, r.apiResources.grpcRoute, r.grpcRouteDNSResyncCh, "GRPCRoute", &gatewayv1.GRPCRouteList{})
		resyncRouteDNSByListenerSetIndex[gatewayv1.TLSRoute, *gatewayv1.TLSRoute](ctx, r.Client, idx, r.apiResources.tlsRoute, r.tlsRouteDNSResyncCh, "TLSRoute", &gatewayv1.TLSRouteList{})
	}
}

// resyncRouteDNS lists Routes by parent Gateway index and enqueues each to ch (no-op if disabled or ch nil).
func resyncRouteDNS[Item any, PT Object[Item]](
	ctx context.Context,
	c client.Client,
	gwNN types.NamespacedName,
	enabled bool,
	ch chan event.TypedGenericEvent[PT],
	routeKind string,
	list client.ObjectList,
) {
	if !enabled || ch == nil {
		return
	}

	enqueueRouteFn := func(item *Item) {
		obj := PT(item)
		nn := client.ObjectKeyFromObject(obj)
		// drop resync if channel is full
		select {
		case ch <- event.TypedGenericEvent[PT]{Object: obj}:
			return
		default:
			log.Warn("Failed to enqueue Route DNS resync", "routeKind", routeKind, "Route", nn.String())
			return
		}
	}

	indexVal := gwNN.String()
	err := loopObjectList[Item](ctx, c, list, enqueueRouteFn, client.MatchingFields{routeParentGatewayIndex: indexVal})
	if err != nil {
		log.Error(err, "Failed to list routes for resync", "routeKind", routeKind, "Gateway", indexVal)
	}
}

// resyncRouteDNSByListenerSetIndex lists Routes by ListenerSet parent index and enqueues each to ch.
func resyncRouteDNSByListenerSetIndex[Item any, PT Object[Item]](
	ctx context.Context,
	c client.Client,
	lsIndex string,
	enabled bool,
	ch chan event.TypedGenericEvent[PT],
	routeKind string,
	list client.ObjectList,
) {
	if !enabled || ch == nil {
		return
	}
	enqueueRouteFn := func(item *Item) {
		obj := PT(item)
		nn := client.ObjectKeyFromObject(obj)
		// drop resync if channel is full
		select {
		case ch <- event.TypedGenericEvent[PT]{Object: obj}:
			return
		default:
			log.Warn("Failed to enqueue Route DNS resync", "routeKind", routeKind, "Route", nn.String())
			return
		}
	}
	err := loopObjectList[Item](ctx, c, list, enqueueRouteFn, client.MatchingFields{routeParentListenerSetIndex: lsIndex})
	if err != nil {
		log.Error(err, "Failed to list routes for listener-set resync", "routeKind", routeKind, "ListenerSet", lsIndex)
	}
}

func buildDNSConfigCondition(err error) metav1.Condition {
	cond := metav1.Condition{
		Type:               conditionTypeDNSConfig,
		LastTransitionTime: metav1.Now(),
	}
	if err != nil {
		cond.Status = metav1.ConditionFalse
		cond.Reason = reasonDNSRecordFailed
		cond.Message = err.Error()
	} else {
		cond.Status = metav1.ConditionTrue
		cond.Reason = reasonDNSRecordConfigured
	}
	return cond
}

func (r *GatewayReconciler) updateGatewayDNSReadyCondition(ctx context.Context, ownerKey types.NamespacedName, err error) error {
	cond := buildDNSConfigCondition(err)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gw := &gatewayv1.Gateway{}
		if getErr := r.Client.Get(ctx, ownerKey, gw); getErr != nil {
			return client.IgnoreNotFound(getErr)
		}
		c := cond
		c.ObservedGeneration = gw.GetGeneration()
		if !meta.SetStatusCondition(&gw.Status.Conditions, c) {
			return nil
		}
		return r.Client.Status().Update(ctx, gw)
	})
}

// removeGatewayDNSConfigCondition strips DNSConfig from Gateway status; ignores NotFound.
func (r *GatewayReconciler) removeGatewayDNSConfigCondition(ctx context.Context, key types.NamespacedName) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gw := &gatewayv1.Gateway{}
		if err := r.Client.Get(ctx, key, gw); err != nil {
			return client.IgnoreNotFound(err)
		}
		if !meta.RemoveStatusCondition(&gw.Status.Conditions, conditionTypeDNSConfig) {
			return nil
		}
		return r.Client.Status().Update(ctx, gw)
	})
}

func ipsToTargets(ips []net.IP) extdns.Targets {
	s := make([]string, 0, len(ips))
	for _, ip := range ips {
		if ip == nil {
			continue // skip nil slot
		}
		s = append(s, ip.String())
	}
	return extdns.NewTargets(s...)
}

func (r *GatewayReconciler) refreshGatewayIPCache(gw *gatewayv1.Gateway, ls []gatewayv1.ListenerSet) (*gatewayDNSCacheEntry, bool) {
	nn := types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}
	entry := gatewayDNSCacheEntry{
		IPs:             ipsToTargets(collectIPsFromGateway(gw)),
		AdmissionRows:   extdnssrc.BuildAdmissionHostCacheRows(gw, ls),
		GatewayResource: nn,
	}

	changed := r.ipCache.put(nn, entry)
	return &entry, changed
}

func (r *GatewayReconciler) getNamespaceType(ctx context.Context, namespace string) (common.NameSpaceType, error) {
	obj := &v1.Namespace{}
	ns := types.NamespacedName{Name: namespace}
	if err := r.Client.Get(ctx, ns, obj); err != nil {
		log.Error(err, "Unable to fetch Namespace", "Namespace", ns)
		return common.NormalNs, err
	}
	return common.GetNamespaceType(obj, nil), nil
}

func (r *GatewayReconciler) listSortedListenerSetsForGateway(ctx context.Context, gwNamespacedName types.NamespacedName) ([]gatewayv1.ListenerSet, error) {
	lsList := &gatewayv1.ListenerSetList{}
	if err := r.Client.List(
		ctx,
		lsList,
		client.MatchingFields{listenerSetParentGatewayIndex: gwNamespacedName.String()},
	); err != nil {
		return nil, err
	}
	listenerSets := lsList.Items
	slices.SortFunc(listenerSets, func(a, b gatewayv1.ListenerSet) int {
		if c := cmp.Compare(a.Namespace, b.Namespace); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return listenerSets, nil
}

func collectIPsFromGateway(gw *gatewayv1.Gateway) []net.IP {
	var ips []net.IP
	gwNamespaceName := types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}
	for _, addr := range gw.Status.Addresses {
		if addr.Type == nil || *addr.Type == gatewayv1.IPAddressType {
			value := strings.TrimSpace(addr.Value)
			if ip := net.ParseIP(value); ip != nil {
				ips = append(ips, ip)
			} else {
				log.Warn("Invalid Gateway address for DNS records (parse failed)", "Gateway", gwNamespaceName.String(), "value", value)
			}
		} else {
			log.Info("Ignore the unsupported K8s Gateway address type for DNS records", "Gateway", gwNamespaceName.String(), "address type", *addr.Type)
		}
	}
	return ips
}

// hasUsableGatewayIP reports whether Gateway status has at least one parseable IP address.
func hasUsableGatewayIP(gw *gatewayv1.Gateway) bool {
	for _, addr := range gw.Status.Addresses {
		if addr.Type != nil && *addr.Type != gatewayv1.IPAddressType {
			continue
		}
		if net.ParseIP(strings.TrimSpace(addr.Value)) != nil {
			return true
		}
	}
	return false
}

// gatewayAPIResources records which gateway.networking.k8s.io/v1 resources exist in the cluster.
type gatewayAPIResources struct {
	gateway     bool
	listenerSet bool
	httpRoute   bool
	grpcRoute   bool
	tlsRoute    bool
}

func (r *GatewayReconciler) checkGatewayCRDs(mgr ctrl.Manager) error {
	if r.discoveryClient == nil {
		var err error
		r.discoveryClient, err = discovery.NewDiscoveryClientForConfig(mgr.GetConfig())
		if err != nil {
			log.Error(err, "Failed to create discovery client", "controller", "Gateway")
			return err
		}
	}
	resourceList, err := r.discoveryClient.ServerResourcesForGroupVersion(gatewayAPIGroupVersion)
	if err != nil {
		if apierrors.IsNotFound(err) { // API group absent
			return nil
		}
		return err
	}
	r.apiResources = gatewayAPIResources{}

	if resourceList == nil {
		return nil
	}
	for _, res := range resourceList.APIResources {
		switch res.Name {
		case "gateways":
			r.apiResources.gateway = true
		case "listenersets":
			r.apiResources.listenerSet = true
		case "httproutes":
			r.apiResources.httpRoute = true
		case "grpcroutes":
			r.apiResources.grpcRoute = true
		case "tlsroutes":
			r.apiResources.tlsRoute = true
		}
	}
	return nil
}

func (r *GatewayReconciler) StartController(mgr ctrl.Manager, _ webhook.Server) error {
	err := r.checkGatewayCRDs(mgr)
	if err != nil {
		log.Error(err, "Failed to check Gateway API CRDs", "controller", "Gateway")
		return err
	}

	if !r.apiResources.gateway {
		log.Info("Gateway API CRDs are not installed in the cluster, skipping Gateway controller start")
		return nil
	}

	log.Debug("Gateway StartController: Gateway API present", "listenerSet", r.apiResources.listenerSet,
		"httpRoute", r.apiResources.httpRoute, "grpcRoute", r.apiResources.grpcRoute, "tlsRoute", r.apiResources.tlsRoute)

	if setupErr := gatewaySetupWithManager(r, mgr); setupErr != nil {
		log.Error(setupErr, "Failed to create controller", "controller", "Gateway")
		return setupErr
	}

	if addErr := mgr.Add(crmanager.RunnableFunc(func(ctx context.Context) error {
		return r.warmGatewayIPCacheOnStartup(ctx)
	})); addErr != nil {
		log.Error(addErr, "Failed to register Gateway ipCache startup sync")
		return addErr
	}
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, r.CollectGarbage)
	return nil
}

func NewGatewayReconciler(mgr ctrl.Manager, service *dns.DNSRecordService) *GatewayReconciler {
	r := &GatewayReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("gateway-controller"),
		Service:  service,
		ipCache:  NewGatewayIPCache(),
	}
	if service != nil && service.NSXConfig != nil {
		updater := common.NewStatusUpdater(
			r.Client,
			service.NSXConfig,
			r.Recorder,
			common.MetricResTypeGateway,
			"Record",
			"Gateway",
		)
		r.StatusUpdater = &updater
	}
	return r
}

// registerGatewayDNSFieldIndexes registers cache field indexes for Gateway and Route DNS watches.
func (r *GatewayReconciler) registerGatewayDNSFieldIndexes(mgr ctrl.Manager) error {
	if r.apiResources.listenerSet {
		if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &gatewayv1.ListenerSet{}, listenerSetParentGatewayIndex, listenerSetParentGatewayIndexFunc); err != nil {
			log.Error(err, "Failed to register ListenerSet cache indexer", "controller", "Gateway")
			return err
		}
	} else {
		log.Info("ListenerSet CRD is not installed, Gateway controller will not process ListenerSet resources")
	}

	if err := r.setIndexField(mgr, &gatewayv1.HTTPRoute{}, "HTTPRoute", r.apiResources.httpRoute); err != nil {
		return err
	}
	if err := r.setIndexField(mgr, &gatewayv1.GRPCRoute{}, "GRPCRoute", r.apiResources.grpcRoute); err != nil {
		return err
	}
	if err := r.setIndexField(mgr, &gatewayv1.TLSRoute{}, "TLSRoute", r.apiResources.tlsRoute); err != nil {
		return err
	}
	return nil
}

func (r *GatewayReconciler) setIndexField(mgr ctrl.Manager, obj client.Object, objLogType string, apiEnabled bool) error {
	if !apiEnabled {
		return nil
	}
	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), obj, routeParentGatewayIndex, routeParentGatewayIndexFunc); err != nil {
		log.Error(err, "Failed to register Route cache indexer", "controller", "Gateway", "type", objLogType)
		return err
	}
	if r.apiResources.listenerSet {
		if err := mgr.GetFieldIndexer().IndexField(context.TODO(), obj, routeParentListenerSetIndex, routeParentListenerSetIndexFunc); err != nil {
			log.Error(err, "Failed to register Route listenerSet cache indexer", "controller", "Gateway", "type", objLogType)
			return err
		}
	}
	return nil
}

func (r *GatewayReconciler) setupWithManager(mgr ctrl.Manager) error {
	if err := r.registerGatewayDNSFieldIndexes(mgr); err != nil {
		return err
	}

	b := ctrl.NewControllerManagedBy(mgr).For(&gatewayv1.Gateway{}, builder.WithPredicates(predicateFuncsGateway))
	if r.apiResources.listenerSet {
		b = b.Watches(&gatewayv1.ListenerSet{}, r.listenerSetEnqueueHandler(), builder.WithPredicates(predicate.GenerationChangedPredicate{}))
	}

	if err := b.WithOptions(controller.Options{MaxConcurrentReconciles: common.NumReconcile()}).
		Complete(r); err != nil {
		return err
	}
	return r.registerRouteDNSControllers(mgr)
}

// warmGatewayIPCacheOnStartup loads ipCache from API, sets ipCacheWarmedOnStartup, enqueues Route resyncs; returns err.
func (r *GatewayReconciler) warmGatewayIPCacheOnStartup(ctx context.Context) error {
	if !r.apiResources.gateway {
		return nil // runnable registered only when Gateway API present
	}
	gwList := &gatewayv1.GatewayList{}
	if err := r.Client.List(ctx, gwList); err != nil {
		return err
	}
	if err := filterUsableGatewayCRs(gwList, func(gw *gatewayv1.Gateway) error {
		nn := types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}
		var ls []gatewayv1.ListenerSet
		if r.apiResources.listenerSet {
			var err error
			ls, err = r.listSortedListenerSetsForGateway(ctx, nn)
			if err != nil {
				return err
			}
		}
		r.refreshGatewayIPCache(gw, ls)
		return nil
	}); err != nil {
		return err
	}
	r.ipCacheWarmedOnStartup.Store(true)
	r.enqueueAllRoutesForDNSResyncOnStartup(ctx)
	return nil
}

func filterUsableGatewayCRs(gwList *gatewayv1.GatewayList, mutateFn func(gw *gatewayv1.Gateway) error) error {
	for i := range gwList.Items {
		gw := &gwList.Items[i]
		if !shouldProcessGateway(gw) {
			continue
		}
		if !hasUsableGatewayIP(gw) {
			continue
		}
		if err := mutateFn(gw); err != nil {
			return err
		}
	}
	return nil
}

func (r *GatewayReconciler) enqueueAllRoutesForDNSResyncOnStartup(ctx context.Context) {
	for _, gwNN := range r.ipCache.listGatewayNamespacedNames() {
		r.enqueueAttachedRoutesForGatewayDNSFromAPI(ctx, gwNN)
	}
}

func (r *GatewayReconciler) RestoreReconcile() error {
	return nil
}
