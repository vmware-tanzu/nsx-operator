/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync/atomic"
	"time"

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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	crmanager "sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
	extdnssrc "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/source"
)

const (
	listenerSetParentGatewayIndex = "listenerSetParentGateway" // ListenerSet -> parent Gateway cache index
	conditionTypeDNSRecordReady   = "DNSRecordReady"           // per-parent Route DNS status
	reasonDNSRecordConfigured     = "DNSRecordConfigured"
	reasonDNSRecordFailed         = "DNSRecordFailed"

	// nsxOperatorGatewayDNSController is the controllerName used in the dedicated nsx-operator
	// parent entry added to Route status.parents. The parentRef in that entry points to the
	// namespace's NetworkInfo CR, so nsx-operator never modifies parent entries owned by other
	// controllers (e.g., entries that point to Gateway objects).
	nsxOperatorGatewayDNSController = gatewayv1.GatewayController("nsx-operator.nsx.vmware.com/gateway-dns")

	routeParentListenerSetIndex = "routeParentListenerSet" // Route -> ListenerSet parent refs
)

var (
	log                     = logger.Log
	filteredGatewayClasses  = sets.New[string](common.ManagedK8sGatewayClassIstio, common.ManagedK8sGatewayClassAviLB)
	gatewaySetupWithManager = func(r *GatewayReconciler, mgr ctrl.Manager) error {
		return r.setupWithManager(mgr)
	}
)

// GatewayReconciler reconciles Gateway API Gateways: ipCache, annotation DNS, Route resync hooks.
type GatewayReconciler struct {
	Client        client.Client
	Scheme        *apimachineryruntime.Scheme
	Recorder      record.EventRecorder
	DNS           dns.DNSRecordProvider
	StatusUpdater StatusUpdater

	apiResources           gatewayAPIResources // from discovery: which Gateway API kinds exist
	discoveryClient        discovery.DiscoveryInterface
	ipCache                *gatewayIPCache
	ipCacheWarmedOnStartup atomic.Bool // set after startup warm; Route DNS waits on this
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
			gw.SetName(req.Name)
			gw.SetNamespace(req.Namespace)
			res, derr := r.clearGatewayDNSRecordsAndCache(ctx, gw, req, "Gateway not found")
			if derr != nil {
				log.Error(derr, "Failed to delete unmanaged gateway", "Gateway", req.NamespacedName)
				return common.ResultRequeueAfter10sec, nil
			}
			return res, nil
		}
		log.Error(err, "Failed to fetch Gateway", "Gateway", req.NamespacedName)
		return common.ResultRequeueAfter10sec, nil
	}

	log.Debug("Gateway loaded", "Gateway", req.NamespacedName, "class", gw.Spec.GatewayClassName,
		"hasDeletionTimestamp", !gw.DeletionTimestamp.IsZero(), "addressCount", len(gw.Status.Addresses), "listenerCount", len(gw.Spec.Listeners))

	handleGatewayCleanup := func(reason string) (ctrl.Result, error) {
		res, derr := r.clearGatewayDNSRecordsAndCache(ctx, gw, req, reason)
		if derr != nil {
			log.Error(derr, "Failed to delete unmanaged gateway", "Gateway", req.NamespacedName)
			return common.ResultRequeueAfter10sec, nil
		}
		return res, nil
	}

	if !gw.DeletionTimestamp.IsZero() {
		return handleGatewayCleanup("Reconciling Gateway deletion")
	}

	if !shouldProcessGateway(gw) {
		return handleGatewayCleanup("Gateway is no longer managed, deleting DNS records")
	}

	if !hasUsableGatewayIP(gw) {
		return handleGatewayCleanup("Gateway has no valid address, DNS records should be deleted")
	}

	nn := req.NamespacedName
	var ls []gatewayv1.ListenerSet
	var err error
	if r.apiResources.listenerSetEnabled {
		ls, err = r.listSortedListenerSetsForGateway(ctx, nn)
		if err != nil {
			log.Error(err, "Failed to list ListenerSets for Gateway DNS cache", "Gateway", nn.String())
			return common.ResultRequeueAfter10sec, nil
		}
	}

	r.StatusUpdater.IncreaseUpdateTotal()

	entry, gatewayDNSCacheChanged := r.upsertGatewayIPCache(gw, ls)
	// Trigger Route DNS resyncs whenever the ipCache entry changes.
	if gatewayDNSCacheChanged {
		r.enqueueAttachedRoutesForGatewayDNSFromAPI(ctx, nn)
	}

	var dnsErr error
	if err := r.reconcileGatewayDNS(ctx, gw); err != nil {
		log.Error(err, "Failed to reconcile DNS for Gateway", "Gateway", req.NamespacedName)
		dnsErr = fmt.Errorf("reconciling DNS: %w", err)
	}

	if dnsErr != nil {
		r.StatusUpdater.UpdateFail(ctx, gw, dnsErr, "reconciling DNS record failed", nil)
		log.Info("Reconciling Gateway failed", "Gateway", req.NamespacedName, "error", dnsErr)
		return common.ResultRequeueAfter10sec, nil
	}

	r.StatusUpdater.UpdateSuccess(ctx, gw, nil)
	log.Info("Reconciling Gateway", "Gateway", req.NamespacedName, "IPs", entry.IPs)

	return common.ResultNormal, nil
}

// clearGatewayDNSRecordsAndCache deletes Gateway DNS rows, drops ipCache, optionally clears DNSRecordReady status; returns Result, err.
func (r *GatewayReconciler) clearGatewayDNSRecordsAndCache(ctx context.Context, gw *gatewayv1.Gateway, req ctrl.Request, logPrefix string) (ctrl.Result, error) {
	log.Info(logPrefix, "Gateway", req.NamespacedName)
	gwNN := types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}
	hadIPCacheEntry := r.ipCache.delete(gwNN)
	if hadIPCacheEntry {
		r.enqueueAttachedRoutesForGatewayDNSFromAPI(ctx, gwNN)
	}

	r.StatusUpdater.IncreaseDeleteTotal()
	if _, err := r.DNS.DeleteRecordByOwnerNN(ctx, dns.ResourceKindGateway, gw.Namespace, gw.Name); err != nil {
		r.StatusUpdater.DeleteFail(req.NamespacedName, gw, err)
		log.Error(err, "Failed to delete DNS records for Gateway", "Gateway", req.NamespacedName)
		return common.ResultRequeueAfter10sec, err
	}
	if uerr := r.removeGatewayDNSConfigCondition(ctx, gwNN); uerr != nil {
		log.Error(uerr, "Failed to clear Gateway DNSRecordReady condition", "Gateway", req.NamespacedName)
		return common.ResultRequeueAfter10sec, uerr
	}

	r.StatusUpdater.DeleteSuccess(req.NamespacedName, gw)
	return common.ResultNormal, nil
}

// enqueueAttachedRoutesForGatewayDNSFromAPI enqueues Route DNS resyncs for Routes attached to gwNN.
func (r *GatewayReconciler) enqueueAttachedRoutesForGatewayDNSFromAPI(ctx context.Context, gwNN types.NamespacedName) {
	var lsList []gatewayv1.ListenerSet
	if r.apiResources.listenerSetEnabled {
		var err error
		lsList, err = r.listSortedListenerSetsForGateway(ctx, gwNN)
		if err != nil {
			log.Error(err, "Failed to list ListenerSets for Route DNS resync", "Gateway", gwNN.String())
		}
	}

	for _, rr := range r.apiResources.routeReconcilers {
		rr.resyncRouteDNS(ctx, gwNN, lsList)
	}
}

func buildDNSRecordReadyCondition(err error) metav1.Condition {
	cond := metav1.Condition{
		Type:               conditionTypeDNSRecordReady,
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
	cond := buildDNSRecordReadyCondition(err)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gw := &gatewayv1.Gateway{}
		if getErr := r.Client.Get(ctx, ownerKey, gw); getErr != nil {
			return client.IgnoreNotFound(getErr)
		}

		patch := client.MergeFrom(gw.DeepCopy())
		c := cond
		c.ObservedGeneration = gw.GetGeneration()
		if !meta.SetStatusCondition(&gw.Status.Conditions, c) {
			return nil
		}
		return r.Client.Status().Patch(ctx, gw, patch)
	})
}

// removeGatewayDNSConfigCondition strips DNSRecordReady from Gateway status; ignores NotFound.
func (r *GatewayReconciler) removeGatewayDNSConfigCondition(ctx context.Context, key types.NamespacedName) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gw := &gatewayv1.Gateway{}
		if err := r.Client.Get(ctx, key, gw); err != nil {
			return client.IgnoreNotFound(err)
		}

		patch := client.MergeFrom(gw.DeepCopy())
		if !meta.RemoveStatusCondition(&gw.Status.Conditions, conditionTypeDNSRecordReady) {
			return nil
		}
		return r.Client.Status().Patch(ctx, gw, patch)
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

func (r *GatewayReconciler) upsertGatewayIPCache(gw *gatewayv1.Gateway, ls []gatewayv1.ListenerSet) (*gatewayDNSCacheEntry, bool) {
	nn := types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}
	entry := &gatewayDNSCacheEntry{
		IPs:             ipsToTargets(collectIPsFromGateway(gw)),
		AdmissionRows:   extdnssrc.BuildAdmissionHostCacheRows(gw, ls),
		GatewayResource: nn,
	}

	changed := r.ipCache.put(nn, entry)
	return entry, changed
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
	// Sort ListenerSets to ensure deterministic cache entry comparisons and prevent false-positive Route DNS resyncs.
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
		if addr.Type != nil && *addr.Type != gatewayv1.IPAddressType {
			log.Info("Ignore the unsupported K8s Gateway address type for DNS records", "Gateway", gwNamespaceName.String(), "address type", *addr.Type)
			continue
		}
		value := strings.TrimSpace(addr.Value)
		if ip := net.ParseIP(value); ip != nil {
			ips = append(ips, ip)
		} else {
			log.Warn("Invalid Gateway address for DNS records (parse failed)", "Gateway", gwNamespaceName.String(), "value", value)
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
	gatewayEnabled     bool
	listenerSetEnabled bool
	routeReconcilers   []routeReconciler
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
	resourceList, err := r.discoveryClient.ServerResourcesForGroupVersion(gatewayv1.GroupVersion.String())
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
			r.apiResources.gatewayEnabled = true
		case "listenersets":
			r.apiResources.listenerSetEnabled = true
		case "httproutes":
			rr := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](
				r, dns.ResourceKindHTTPRoute, newHTTPRoute, func() client.ObjectList { return &gatewayv1.HTTPRouteList{} })
			r.apiResources.routeReconcilers = append(r.apiResources.routeReconcilers, rr)
		case "grpcroutes":
			rr := newRouteReconciler[*GRPCRoute, gatewayv1.GRPCRoute, *gatewayv1.GRPCRoute](
				r, dns.ResourceKindGRPCRoute, newGRPCRoute, func() client.ObjectList { return &gatewayv1.GRPCRouteList{} })
			r.apiResources.routeReconcilers = append(r.apiResources.routeReconcilers, rr)
		case "tlsroutes":
			rr := newRouteReconciler[*TLSRoute, gatewayv1.TLSRoute, *gatewayv1.TLSRoute](
				r, dns.ResourceKindTLSRoute, newTLSRoute, func() client.ObjectList { return &gatewayv1.TLSRouteList{} })
			r.apiResources.routeReconcilers = append(r.apiResources.routeReconcilers, rr)
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

	if !r.apiResources.gatewayEnabled {
		log.Info("Gateway API CRDs are not installed in the cluster, skipping Gateway controller start")
		return nil
	}

	log.Info("Gateway StartController: Gateway API present", "listenerSet", r.apiResources.listenerSetEnabled)

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

func NewGatewayReconciler(mgr ctrl.Manager, dnsProv dns.DNSRecordProvider) *GatewayReconciler {
	r := &GatewayReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("gateway-controller"), //nolint:staticcheck // record.EventRecorder; StatusUpdater not on events.EventRecorder yet
		DNS:      dnsProv,
		ipCache:  NewGatewayIPCache(),
	}
	if svc, ok := dnsProv.(*dns.DNSRecordService); ok && svc != nil && svc.NSXConfig != nil {
		updater := common.NewStatusUpdater(r.Client, svc.NSXConfig, r.Recorder, common.MetricResTypeGateway, "ProjectDnsRecord", "Gateway")
		r.StatusUpdater = &updater
	}
	return r
}

// registerGatewayDNSFieldIndexes registers cache field indexes for Gateway and Route DNS watches.
func (r *GatewayReconciler) registerGatewayDNSFieldIndexes(mgr ctrl.Manager) error {
	if r.apiResources.listenerSetEnabled {
		if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &gatewayv1.ListenerSet{}, listenerSetParentGatewayIndex, listenerSetParentGatewayIndexFunc); err != nil {
			log.Error(err, "Failed to register ListenerSet cache indexer", "controller", "Gateway")
			return err
		}
	} else {
		log.Info("ListenerSet CRD is not installed, Gateway controller will not process ListenerSet resources")
	}

	for _, rr := range r.apiResources.routeReconcilers {
		if err := rr.setRouteIndexField(mgr, r.apiResources.listenerSetEnabled); err != nil {
			return err
		}
	}

	return nil
}

func (r *GatewayReconciler) setupWithManager(mgr ctrl.Manager) error {
	if err := r.registerGatewayDNSFieldIndexes(mgr); err != nil {
		return err
	}

	b := ctrl.NewControllerManagedBy(mgr).For(&gatewayv1.Gateway{}, builder.WithPredicates(predicateFuncsGateway)).
		Watches(
			&v1alpha1.NetworkInfo{},
			handler.EnqueueRequestsFromMapFunc(r.networkInfoToGatewayDNSRequests),
			builder.WithPredicates(predicateNetworkInfoAllowedDNSDomainsChanged()),
		)

	if r.apiResources.listenerSetEnabled {
		b = b.Watches(&gatewayv1.ListenerSet{}, r.listenerSetEnqueueHandler(), builder.WithPredicates(predicate.GenerationChangedPredicate{}))
	}

	if err := r.registerRouteWatchers(mgr); err != nil {
		return err
	}

	return b.WithOptions(controller.Options{MaxConcurrentReconciles: common.NumReconcile()}).Complete(r)
}

// warmGatewayIPCacheOnStartup loads ipCache from API, sets ipCacheWarmedOnStartup, enqueues Route resyncs; returns err.
func (r *GatewayReconciler) warmGatewayIPCacheOnStartup(ctx context.Context) error {
	if !r.apiResources.gatewayEnabled {
		return nil // runnable registered only when Gateway API present
	}
	gwList := &gatewayv1.GatewayList{}
	if err := r.Client.List(ctx, gwList); err != nil {
		return err
	}
	if err := filterUsableGatewayCRs(gwList, func(gw *gatewayv1.Gateway) error {
		nn := types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}
		var ls []gatewayv1.ListenerSet
		if r.apiResources.listenerSetEnabled {
			var err error
			ls, err = r.listSortedListenerSetsForGateway(ctx, nn)
			if err != nil {
				return err
			}
		}
		r.upsertGatewayIPCache(gw, ls)
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

// registerRouteWatchers registers Route DNS reconcilers for installed Route CRDs.
func (r *GatewayReconciler) registerRouteWatchers(mgr ctrl.Manager) error {
	opts := controller.Options{MaxConcurrentReconciles: common.NumReconcile()}
	for _, rr := range r.apiResources.routeReconcilers {
		if err := rr.registerWatcher(mgr, opts, r.apiResources.listenerSetEnabled); err != nil {
			return err
		}
	}
	return nil
}
