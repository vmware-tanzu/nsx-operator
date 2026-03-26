/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
)

const (
	listenerSetParentGatewayIndex = "listenerSetParentGateway"
	// DNS condition type and reasons for Gateway/ListenerSet status.
	conditionTypeDNSReady     = "DNSReady"
	reasonDNSRecordConfigured = "DNSRecordConfigured"
	reasonDNSRecordFailed     = "DNSRecordFailed"
)

var (
	log                    = logger.Log
	ResultNormal           = common.ResultNormal
	filteredGatewayClasses = sets.New[string](common.ManagedK8sGatewayClassIstio)
)

// statusUpdater is an interface for test
type statusUpdater interface {
	UpdateSuccess(ctx context.Context, obj client.Object, setStatusFn common.UpdateSuccessStatusFn, args ...interface{})
	UpdateFail(ctx context.Context, obj client.Object, err error, msg string, setStatusFn common.UpdateFailStatusFn, args ...interface{})
	DeleteSuccess(namespacedName types.NamespacedName, obj client.Object)
	IncreaseSyncTotal()
	IncreaseUpdateTotal()
	IncreaseDeleteTotal()
	IncreaseDeleteSuccessTotal()
	IncreaseDeleteFailTotal()
	DeleteFail(namespacedName types.NamespacedName, obj client.Object, err error)
}

// GatewayReconciler watches Gateway API resources and reconciles Gateways.
// ListenerSet events are mapped back to their parent Gateways via parentRefs.
type GatewayReconciler struct {
	Client        client.Client
	Scheme        *apimachineryruntime.Scheme
	Recorder      record.EventRecorder
	Service       *dns.DNSRecordService
	StatusUpdater statusUpdater
}

// Reconcile is the main reconciliation loop for Gateway objects.
// It fetches the Gateway referenced by req, validates whether it should be processed, and then applies the desired state.
// Existing DNS records on the Gateway is deleted when
// - the Gateway has a non-zero DeletionTimestamp or the Gateway is not found.
// - the Gateway has no valid IP address
// - the Gateway is updated to a different class which is not managed.
func (r *GatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startTime := time.Now()
	defer func() {
		log.Info("Finished reconciling Gateway", "Gateway", req.NamespacedName, "duration(ms)", time.Since(startTime).Milliseconds())
	}()

	r.StatusUpdater.IncreaseSyncTotal()
	gw := &gatewayv1.Gateway{}
	if err := r.Client.Get(ctx, req.NamespacedName, gw); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Gateway not found", "Gateway", req.NamespacedName)
			gw.SetName(req.Name)
			gw.SetNamespace(req.Namespace)
			return r.deleteAllDNSRecords(ctx, gw, req)
		}
		log.Error(err, "Failed to fetch Gateway", "Gateway", req.NamespacedName)
		return common.ResultRequeueAfter10sec, err
	}

	if !shouldProcessGateway(gw) {
		// Gateway is no longer in a managed GatewayClass (e.g. class changed away from managed).
		// The update predicate still fires when old was managed, so we must clean up any DNS records
		// that were created before the class change.
		log.Info("Gateway is no longer managed, deleting DNS records", "Gateway", req.NamespacedName)
		return r.deleteAllDNSRecords(ctx, gw, req)
	}

	if !gw.DeletionTimestamp.IsZero() {
		log.Info("Reconciling Gateway delete", "Gateway", req.NamespacedName)
		return r.deleteAllDNSRecords(ctx, gw, req)
	}

	if !hasUsableGatewayIP(gw) {
		log.Info("Gateway has no valid address, DNS records should be deleted", "Gateway", req.NamespacedName)
		return r.deleteAllDNSRecords(ctx, gw, req)
	}

	r.StatusUpdater.IncreaseUpdateTotal()
	desiredDNSRecords, err := r.buildDNSRecordsForGateway(ctx, gw)
	if err != nil {
		log.Error(err, "Failed to build DNS records for Gateway", "Gateway", req.NamespacedName.String())
		return common.ResultRequeueAfter10sec, err
	}
	existingOwners := make([]*dns.ResourceRef, 0)
	var lastErr error
	for _, rec := range desiredDNSRecords {
		existingOwners = append(existingOwners, rec.Owner)
		updateErr := r.Service.CreateOrUpdateDNSRecords(ctx, rec)
		if updateErr != nil {
			if rec.Owner.Kind == dns.ResourceKindListenerSet {
				log.Error(updateErr, fmt.Sprintf("Failed to configure DNS records for %s", rec.Owner.Kind), "Gateway", req.NamespacedName.String(), "ListenerSet", rec.Owner.GetNamespace()+"/"+rec.Owner.GetName())
			} else {
				log.Error(updateErr, fmt.Sprintf("Failed to configure DNS records for %s", rec.Owner.Kind), "Gateway", req.NamespacedName.String())
			}
			lastErr = updateErr
		}

		// Update resource conditions.
		r.updateDNSRecordCondition(ctx, rec.Owner, updateErr)
	}

	// Delete the existing DNS records on the current Gateway but the owner resource does not exist.
	delErr := r.Service.DeleteOrphanedDNSRecordsInGateway(ctx, gw.Namespace, gw.Name, existingOwners)
	if delErr != nil {
		log.Error(delErr, "Failed to delete the orphaned DNS records")
	}

	if lastErr != nil || delErr != nil {
		if lastErr != nil {
			r.StatusUpdater.UpdateFail(ctx, gw, lastErr, "DNS record create/update failed", nil)
		}
		return common.ResultRequeueAfter10sec, lastErr
	}

	r.StatusUpdater.UpdateSuccess(ctx, gw, nil)
	log.Info("Reconciling Gateway", "Gateway", req.NamespacedName, "generation", gw.Generation, "dnsRecords", len(desiredDNSRecords))
	return ResultNormal, nil
}

// deleteAllDNSRecords removes all DNS records for gw, updating metrics regardless of outcome.
// Called when the Gateway is deleted, transitions to an unmanaged class, or loses its IP address.
func (r *GatewayReconciler) deleteAllDNSRecords(ctx context.Context, gw *gatewayv1.Gateway, req ctrl.Request) (ctrl.Result, error) {
	r.StatusUpdater.IncreaseDeleteTotal()
	if err := r.Service.DeleteAllDNSRecordsInGateway(ctx, gw.Namespace, gw.Name); err != nil {
		r.StatusUpdater.DeleteFail(req.NamespacedName, gw, err)
		log.Error(err, "Failed to delete DNS records for Gateway", "Gateway", req.NamespacedName)
		return common.ResultRequeueAfter10sec, err
	}
	r.StatusUpdater.DeleteSuccess(req.NamespacedName, gw)
	return ResultNormal, nil
}

// PredicateFuncsGateway defines the event filters for Gateway objects.
// It limits reconciliation to:
// - Gateways with a managed GatewayClass
// - Updates where Addresses or Listeners have changed
// - Create/Delete events for managed Gateways only.
var PredicateFuncsGateway = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		gw := e.Object.(*gatewayv1.Gateway)
		return shouldProcessGateway(gw) && hasUsableGatewayIP(gw)
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldObj := e.ObjectOld.(*gatewayv1.Gateway)
		newObj := e.ObjectNew.(*gatewayv1.Gateway)
		log.Debug("Receive K8s Gateway update event", "Name", oldObj.Name, "Namespace", oldObj.Namespace)
		if !shouldProcessGateway(oldObj) && !shouldProcessGateway(newObj) {
			return false
		}
		if reflect.DeepEqual(oldObj.Status.Addresses, newObj.Status.Addresses) &&
			reflect.DeepEqual(oldObj.Spec.Listeners, newObj.Spec.Listeners) {
			return false
		}
		return true
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		gw := e.Object.(*gatewayv1.Gateway)
		return shouldProcessGateway(gw)
	},
}

// buildDNSReadyCondition returns a metav1.Condition for DNSReady from CreateOrUpdateDNSRecords result.
func buildDNSReadyCondition(err error) metav1.Condition {
	cond := metav1.Condition{
		Type:               conditionTypeDNSReady,
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

// updateDNSRecordCondition sets the DNSReady condition on the resource that owns the DNS record:
// when owner is Gateway, the condition is set on the Gateway; when owner is ListenerSet, on the ListenerSet.
// If CreateOrUpdateDNSRecords returned an error, status is False and message is the error string;
// otherwise status is True.
func (r *GatewayReconciler) updateDNSRecordCondition(ctx context.Context, owner *dns.ResourceRef, err error) {
	cond := buildDNSReadyCondition(err)
	ownerKey := types.NamespacedName{Namespace: owner.GetNamespace(), Name: owner.GetName()}
	switch owner.Kind {
	case dns.ResourceKindGateway:
		if uerr := r.updateGatewayStatusCondition(ctx, ownerKey, cond); uerr != nil {
			log.Error(uerr, "Failed to update Gateway DNSReady condition", "Gateway", ownerKey)
		}
	case dns.ResourceKindListenerSet:
		if uerr := r.updateListenerSetStatusCondition(ctx, ownerKey, cond); uerr != nil {
			log.Error(uerr, "Failed to update ListenerSet DNSReady condition", "ListenerSet", ownerKey)
		}
	default:
		log.V(1).Info("updateDNSRecordCondition: unsupported owner kind, skipping", "kind", owner.Kind, "owner", owner.GetNamespace()+"/"+owner.GetName())
	}
}

func (r *GatewayReconciler) updateGatewayStatusCondition(ctx context.Context, key types.NamespacedName, cond metav1.Condition) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &gatewayv1.Gateway{}
		if err := r.Client.Get(ctx, key, latest); err != nil {
			return err
		}
		cond.ObservedGeneration = latest.Generation
		latest.Status.Conditions = mergeDNSReadyCondition(latest.Status.Conditions, cond)
		return r.Client.Status().Update(ctx, latest)
	})
}

func (r *GatewayReconciler) updateListenerSetStatusCondition(ctx context.Context, key types.NamespacedName, cond metav1.Condition) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		ls := &gatewayv1.ListenerSet{}
		if err := r.Client.Get(ctx, key, ls); err != nil {
			return err
		}
		cond.ObservedGeneration = ls.Generation
		ls.Status.Conditions = mergeDNSReadyCondition(ls.Status.Conditions, cond)
		return r.Client.Status().Update(ctx, ls)
	})
}

// mergeDNSReadyCondition updates or appends the DNSReady condition per gateway-api merge rules.
func mergeDNSReadyCondition(conditions []metav1.Condition, newCond metav1.Condition) []metav1.Condition {
	for i := range conditions {
		if (conditions)[i].Type == conditionTypeDNSReady {
			(conditions)[i].Status = newCond.Status
			(conditions)[i].Reason = newCond.Reason
			(conditions)[i].Message = newCond.Message
			(conditions)[i].LastTransitionTime = metav1.Now()
			(conditions)[i].ObservedGeneration = newCond.ObservedGeneration
			return conditions
		}
	}
	conditions = append(conditions, newCond)
	return conditions
}

// shouldProcessGateway returns true if the given Gateway should
// be handled by this controller. The decision is based on whether
// its GatewayClassName is in the filteredGatewayClasses set.
func shouldProcessGateway(gw *gatewayv1.Gateway) bool {
	return filteredGatewayClasses.Has(string(gw.Spec.GatewayClassName))
}

func getGatewayReference(gw *gatewayv1.Gateway) *dns.ResourceRef {
	return &dns.ResourceRef{
		Kind:   dns.ResourceKindGateway,
		Object: gw.GetObjectMeta(),
	}
}

func getListenerSetReference(ls gatewayv1.ListenerSet) *dns.ResourceRef {
	return &dns.ResourceRef{
		Kind:   dns.ResourceKindListenerSet,
		Object: ls.GetObjectMeta(),
	}
}

func (r *GatewayReconciler) buildDNSRecordsForGateway(ctx context.Context, gw *gatewayv1.Gateway) ([]*dns.Record, error) {
	if gw == nil {
		return nil, fmt.Errorf("invalid Gateway is provided")
	}
	ips := collectIPsFromGateway(gw)
	if len(ips) == 0 {
		return nil, nil
	}
	var records []*dns.Record

	gwHostnames := collectHostnamesInListeners(gw.Spec.Listeners)
	gwRef := getGatewayReference(gw)

	if len(gwHostnames) > 0 {
		gwNSType, err := r.getNamespaceType(ctx, gw.Namespace)
		if err != nil {
			return nil, err
		}
		records = append(records, &dns.Record{
			Addresses:       ips,
			Hostnames:       gwHostnames,
			AddressProvider: gwRef,
			Owner:           gwRef,
			ForSVService:    gwNSType == common.SVServiceNs,
		})
	}

	listenerSetList, err := r.listListenerSetsForGateway(ctx, types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name})
	if err != nil {
		return nil, err
	}

	for i := range listenerSetList {
		ls := listenerSetList[i]
		hostnames := collectHostnamesFromListenerSet(ls)
		if len(hostnames) == 0 {
			continue
		}
		lsNSType, err := r.getNamespaceType(ctx, ls.Namespace)
		if err != nil {
			return nil, err
		}
		records = append(records, &dns.Record{
			Addresses:       ips,
			Hostnames:       hostnames,
			AddressProvider: gwRef,
			Owner:           getListenerSetReference(ls),
			ForSVService:    lsNSType == common.SVServiceNs,
		})
	}

	return records, nil
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

func (r *GatewayReconciler) listListenerSetsForGateway(
	ctx context.Context,
	gwNamespacedName types.NamespacedName,
) ([]gatewayv1.ListenerSet, error) {
	lsList := &gatewayv1.ListenerSetList{}
	if err := r.Client.List(
		ctx,
		lsList,
		client.MatchingFields{listenerSetParentGatewayIndex: gwNamespacedName.String()},
	); err != nil {
		return nil, err
	}
	return lsList.Items, nil
}

func collectIPsFromGateway(gw *gatewayv1.Gateway) []net.IP {
	var ips []net.IP
	if gw == nil {
		return ips
	}
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

func collectHostnamesInListeners(gwListeners []gatewayv1.Listener) []string {
	var hostnames []string
	for _, l := range gwListeners {
		if l.Hostname != nil {
			h := strings.TrimSpace(string(*l.Hostname))
			if h != "" {
				hostnames = append(hostnames, h)
			}
		}
	}
	return hostnames
}

// hasUsableGatewayIP returns true if the Gateway has at least one parseable IP address (IPAddressType).
// Used to decide whether we can create DNS records (need IP) or should delete existing ones.
func hasUsableGatewayIP(gw *gatewayv1.Gateway) bool {
	return len(collectIPsFromGateway(gw)) > 0
}

func (r *GatewayReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1.Gateway{}, builder.WithPredicates(PredicateFuncsGateway)).
		Watches(
			&gatewayv1.ListenerSet{},
			listenerSetToGateway,
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: common.NumReconcile()}).
		Complete(r)
}

// Start setup manager
func (r *GatewayReconciler) Start(mgr ctrl.Manager) error {
	return r.setupWithManager(mgr)
}

func (r *GatewayReconciler) RestoreReconcile() error {
	return nil
}

func (r *GatewayReconciler) CollectGarbage(ctx context.Context) error {
	cachedGatewaySet := r.Service.ListGatewayNamespacedName()
	gwList := gatewayv1.GatewayList{}
	err := r.Client.List(ctx, &gwList)
	if err != nil {
		log.Error(err, "failed to list K8s Gateways CR")
		return err
	}

	CRGatewayMap := make(map[types.NamespacedName]gatewayv1.Gateway, 0)
	for i := range gwList.Items {
		gw := gwList.Items[i]
		if !shouldProcessGateway(&gw) {
			continue
		}
		CRGatewayMap[types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}] = gw
	}

	var errList []error
	for elem := range cachedGatewaySet {
		gwCR, found := CRGatewayMap[elem]
		// Delete all the DNS records if the corresponding Gateway does not exist.
		if !found {
			log.Info("GC collected nsx DNS records for Gateway", "Gateway", elem.String())
			if err = r.Service.DeleteAllDNSRecordsInGateway(ctx, elem.Namespace, elem.Name); err != nil {
				r.StatusUpdater.DeleteFail(elem, nil, err)
				log.Error(err, "Failed to delete nsx DNS records for Gateway", "Gateway", elem.String())
				errList = append(errList, err)
			} else {
				r.StatusUpdater.DeleteSuccess(elem, nil)
			}
		} else {
			existingOwners := make([]*dns.ResourceRef, 0)
			if len(collectHostnamesInListeners(gwCR.Spec.Listeners)) > 0 {
				existingOwners = append(existingOwners, getGatewayReference(&gwCR))
			}

			listenerSetList, err := r.listListenerSetsForGateway(ctx, types.NamespacedName{Namespace: gwCR.Namespace, Name: gwCR.Name})
			if err != nil {
				log.Error(err, "Failed to list K8s ListenerSet referred to the existing Gateway", "Gateway", elem.String())
				errList = append(errList, err)
				continue
			}

			for i := range listenerSetList {
				ls := listenerSetList[i]
				if len(collectHostnamesFromListenerSet(ls)) > 0 {
					existingOwners = append(existingOwners, getListenerSetReference(ls))
				}
			}

			// Delete the DNS records configured on the given Gateway but owner does not exist.
			if err := r.Service.DeleteOrphanedDNSRecordsInGateway(ctx, gwCR.Namespace, gwCR.Name, existingOwners); err != nil {
				r.StatusUpdater.DeleteFail(elem, nil, err)
				log.Error(err, "Failed to delete nsx DNS records attached to Gateway without owner", "Gateway", elem)
				errList = append(errList, err)
			}
		}
	}

	if len(errList) > 0 {
		return fmt.Errorf("errors found in K8s Gateway garbage collection: %s", errList)
	}
	return nil
}

func (r *GatewayReconciler) StartController(mgr ctrl.Manager, _ webhook.Server) error {
	if err := r.Start(mgr); err != nil {
		log.Error(err, "Failed to create controller", "controller", "Gateway")
		return err
	}
	// Register a listenerset->gateway index in the informer cache
	if err := mgr.GetFieldIndexer().IndexField(
		context.TODO(),
		&gatewayv1.ListenerSet{},
		listenerSetParentGatewayIndex,
		listenerSetParentGatewayIndexFunc,
	); err != nil {
		log.Error(err, "Failed to register cache indexer", "controller", "Gateway")
		return err
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
	}
	if service != nil && service.NSXConfig != nil {
		statusUpdater := common.NewStatusUpdater(
			r.Client,
			service.NSXConfig,
			r.Recorder,
			common.MetricResTypeGateway,
			"DNSRecord",
			"Gateway",
		)
		r.StatusUpdater = &statusUpdater
	}
	return r
}
