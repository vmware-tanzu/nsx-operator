/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dnsrecord

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
)

var (
	log           = logger.Log
	ResultNormal  = common.ResultNormal
	ResultRequeue = common.ResultRequeue
)

const (
	ReasonDNSRecordReady            = "DNSRecordReady"
	ReasonDNSRecordFailed           = "DNSRecordFailed"
	ReasonDNSRecordValidationFailed = "DNSRecordValidationFailed"
)

// DNSRecordReconciler reconciles a DNSRecord object
// +kubebuilder:rbac:groups=crd.nsx.vmware.com,resources=dnsrecords,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=crd.nsx.vmware.com,resources=dnsrecords/status,verbs=get;update;patch
type DNSRecordReconciler struct {
	Client        client.Client
	Scheme        *runtime.Scheme
	Service       *dns.DNSRecordService
	Recorder      record.EventRecorder
	StatusUpdater common.StatusUpdater
}

func calculateFQDN(recordName, domainName string) string {
	recordName = strings.TrimSpace(recordName)
	domainName = strings.TrimSpace(domainName)
	domainName = strings.TrimSuffix(domainName, ".")
	if recordName == "" || recordName == "@" {
		return strings.ToLower(domainName)
	}
	recordName = strings.TrimSuffix(recordName, ".")
	return strings.ToLower(fmt.Sprintf("%s.%s", recordName, domainName))
}

func NewDNSRecordReconciler(mgr ctrl.Manager, dnsRecordService *dns.DNSRecordService) *DNSRecordReconciler {
	if dnsRecordService == nil {
		log.Error(nil, "dnsRecordService must not be nil for DNSRecordReconciler")
		return nil
	}
	recorder := mgr.GetEventRecorderFor("dnsrecord-controller") //nolint:staticcheck // record.EventRecorder; StatusUpdater not on events.EventRecorder yet
	var nsxConfig *config.NSXOperatorConfig
	if dnsRecordService.NSXConfig != nil {
		nsxConfig = dnsRecordService.NSXConfig
	} else {
		nsxConfig = &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}
	}
	return &DNSRecordReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		Service:       dnsRecordService,
		Recorder:      recorder,
		StatusUpdater: common.NewStatusUpdater(mgr.GetClient(), nsxConfig, recorder, common.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}
}

func (r *DNSRecordReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.DNSRecord{}
	log.Info("reconciling DNSRecord CR", "dnsrecord", req.NamespacedName)
	r.StatusUpdater.IncreaseSyncTotal()

	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			if _, err := r.Service.DeleteRecordByOwnerNN(ctx, dns.ResourceKindDNSRecord, req.Namespace, req.Name); err != nil {
				log.Error(err, "Failed to delete DNS record from NSX on NotFound", "DNSRecord", req.NamespacedName)
				r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
				return ResultRequeue, err
			}
			r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
			return ResultNormal, nil
		}
		log.Error(err, "unable to fetch DNSRecord CR", "req", req.NamespacedName)
		return ResultRequeue, err
	}

	if !obj.ObjectMeta.DeletionTimestamp.IsZero() {
		r.StatusUpdater.IncreaseDeleteTotal()
		if _, err := r.Service.DeleteRecordByOwnerNN(ctx, dns.ResourceKindDNSRecord, obj.Namespace, obj.Name); err != nil {
			log.Error(err, "Failed to delete DNS record from NSX", "DNSRecord", req.NamespacedName)
			r.StatusUpdater.DeleteFail(req.NamespacedName, obj, err)
			return ResultRequeue, err
		}
		r.StatusUpdater.DeleteSuccess(req.NamespacedName, obj)
		return ResultNormal, nil
	}

	r.StatusUpdater.IncreaseUpdateTotal()

	switch obj.Spec.RecordType {
	case v1alpha1.DNSRecordTypeA, v1alpha1.DNSRecordTypeAAAA:
	default:
		err := fmt.Errorf("unsupported DNS record type %q: only A and AAAA are supported", obj.Spec.RecordType)
		if statusErr := r.updateFailureStatus(ctx, obj, ReasonDNSRecordValidationFailed, err, "DNS record type validation failed"); statusErr != nil {
			return ResultRequeue, statusErr
		}
		return ResultNormal, nil
	}

	if obj.Spec.TTL != nil && (*obj.Spec.TTL < dns.MinRecordTTL || *obj.Spec.TTL > dns.MaxRecordTTL) {
		err := fmt.Errorf("DNS record TTL must be between %d and %d seconds, got %d", dns.MinRecordTTL, dns.MaxRecordTTL, *obj.Spec.TTL)
		if statusErr := r.updateFailureStatus(ctx, obj, ReasonDNSRecordValidationFailed, err, "DNS record TTL validation failed"); statusErr != nil {
			return ResultRequeue, statusErr
		}
		return ResultNormal, nil
	}

	computedFQDN := calculateFQDN(obj.Spec.RecordName, obj.Spec.DomainName)
	if strings.HasPrefix(computedFQDN, "*.") {
		err := fmt.Errorf("wildcard DNS names are not supported: %q", computedFQDN)
		if statusErr := r.updateFailureStatus(ctx, obj, ReasonDNSRecordValidationFailed, err, "DNS record name validation failed"); statusErr != nil {
			return ResultRequeue, statusErr
		}
		return ResultNormal, nil
	}
	if obj.Status.FQDN != computedFQDN {
		obj.Status.FQDN = computedFQDN
		if err := r.Client.Status().Update(ctx, obj); err != nil {
			log.Error(err, "Failed to update FQDN on DNSRecord CR", "DNSRecord", req.NamespacedName)
			statusErr := r.updateFailureStatus(ctx, obj, ReasonDNSRecordFailed, err, "Failed to update FQDN")
			return ResultRequeue, errors.Join(err, statusErr)
		}
	}

	ttl := int32(dns.DefaultRecordTtL)
	if obj.Spec.TTL != nil {
		ttl = *obj.Spec.TTL
	}

	ep := &extdns.Endpoint{
		DNSName:    computedFQDN,
		RecordType: string(obj.Spec.RecordType),
		Targets:    obj.Spec.RecordValues,
		RecordTTL:  extdns.TTL(ttl),
	}

	owner := &dns.ResourceRef{
		Kind:   dns.ResourceKindDNSRecord,
		Object: obj,
	}

	rows, _, err := r.Service.ValidateEndpointsByZone(obj.Namespace, owner, []*extdns.Endpoint{ep})
	if err != nil {
		log.Error(err, "Failed to validate DNSRecord against DNS zones", "DNSRecord", req.NamespacedName)
		statusErr := r.updateFailureStatus(ctx, obj, ReasonDNSRecordValidationFailed, err, "DNS zone validation failed")
		return ResultRequeue, errors.Join(err, statusErr)
	}

	if len(rows) != 1 {
		err := fmt.Errorf("expected one validated DNS record, got %d", len(rows))
		statusErr := r.updateFailureStatus(ctx, obj, ReasonDNSRecordValidationFailed, err, "DNS zone validation returned no record")
		return ResultRequeue, errors.Join(err, statusErr)
	}

	batch := dns.NewOwnerScopedAggregatedRouteDNS(owner, rows)
	if _, err := r.Service.CreateOrUpdateRecords(ctx, batch); err != nil {
		log.Error(err, "Failed to sync DNSRecord to NSX", "DNSRecord", req.NamespacedName)
		statusErr := r.updateFailureStatus(ctx, obj, ReasonDNSRecordFailed, err, "Failed to create or update DNS record on NSX")
		return ResultRequeue, errors.Join(err, statusErr)
	}

	return r.updateSuccessStatus(ctx, obj)
}

func setDNSRecordReadyStatusTrue(k8sClient client.Client, ctx context.Context, record *v1alpha1.DNSRecord, transitionTime metav1.Time) error {
	cond := metav1.Condition{
		Type:               string(v1alpha1.Ready),
		Status:             metav1.ConditionTrue,
		Reason:             ReasonDNSRecordReady,
		Message:            "NSX DNS record successfully created/updated",
		ObservedGeneration: record.Generation,
		LastTransitionTime: transitionTime,
	}
	return updateDNSRecordStatusConditions(k8sClient, ctx, record, cond)
}

func setDNSRecordReadyStatusFalse(k8sClient client.Client, ctx context.Context, record *v1alpha1.DNSRecord, transitionTime metav1.Time, reason string, err error) error {
	cond := metav1.Condition{
		Type:               string(v1alpha1.Ready),
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            fmt.Sprintf("Failed to process DNSRecord: %v", err),
		ObservedGeneration: record.Generation,
		LastTransitionTime: transitionTime,
	}
	return updateDNSRecordStatusConditions(k8sClient, ctx, record, cond)
}

func (r *DNSRecordReconciler) updateFailureStatus(ctx context.Context, obj *v1alpha1.DNSRecord, reason string, err error, message string) error {
	if reason == "" {
		reason = ReasonDNSRecordFailed
	}
	r.StatusUpdater.UpdateFail(ctx, obj, err, message, nil)
	return setDNSRecordReadyStatusFalse(r.Client, ctx, obj, metav1.Now(), reason, err)
}

func (r *DNSRecordReconciler) updateSuccessStatus(ctx context.Context, obj *v1alpha1.DNSRecord) (ctrl.Result, error) {
	if err := setDNSRecordReadyStatusTrue(r.Client, ctx, obj, metav1.Now()); err != nil {
		return ResultRequeue, err
	}
	r.StatusUpdater.UpdateSuccess(ctx, obj, nil)
	return ResultNormal, nil
}

func updateDNSRecordStatusConditions(k8sClient client.Client, ctx context.Context, record *v1alpha1.DNSRecord, cond metav1.Condition) error {
	updated := record.DeepCopy()
	if !meta.SetStatusCondition(&updated.Status.Conditions, cond) {
		return nil
	}
	if err := k8sClient.Status().Update(ctx, updated); err != nil {
		log.Error(err, "Failed to update DNSRecord status", "Name", record.Name, "Namespace", record.Namespace)
		return err
	}
	*record = *updated
	log.Info("Updated DNSRecord status", "Name", record.Name, "Namespace", record.Namespace, "Status", record.Status)
	return nil
}

func (r *DNSRecordReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.DNSRecord{}).
		Watches(
			&v1alpha1.NetworkInfo{},
			handler.EnqueueRequestsFromMapFunc(r.networkInfoToDNSRecordRequests),
			builder.WithPredicates(predicateNetworkInfoAllowedDNSDomainsChanged()),
		).
		WithEventFilter(common.VPCNamespacePredicate(r.Client)).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: common.NumReconcile(),
		}).
		Complete(r)
}

func (r *DNSRecordReconciler) networkInfoToDNSRecordRequests(ctx context.Context, obj client.Object) []ctrl.Request {
	ni, ok := obj.(*v1alpha1.NetworkInfo)
	if !ok || ni == nil {
		return nil
	}
	recordList := &v1alpha1.DNSRecordList{}
	if err := r.Client.List(ctx, recordList, client.InNamespace(ni.Namespace)); err != nil {
		log.Error(err, "Failed to list DNSRecords for NetworkInfo DNS domain change", "Namespace", ni.Namespace)
		return nil
	}
	var reqs []ctrl.Request
	for i := range recordList.Items {
		reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&recordList.Items[i])})
	}
	return reqs
}

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

func (r *DNSRecordReconciler) gcRunnable() manager.Runnable {
	return manager.RunnableFunc(func(ctx context.Context) error {
		stop := make(chan bool)
		go func() {
			<-ctx.Done()
			close(stop)
		}()
		common.GenericGarbageCollector(stop, servicecommon.GCInterval, r.CollectGarbage)
		return nil
	})
}

func (r *DNSRecordReconciler) StartController(mgr ctrl.Manager, _ webhook.Server) error {
	if err := r.setupWithManager(mgr); err != nil {
		log.Error(err, "Failed to create controller", "controller", "DNSRecord")
		return err
	}
	if err := mgr.Add(r.gcRunnable()); err != nil {
		log.Error(err, "Failed to add DNSRecord GC to manager")
		return err
	}
	return nil
}

func (r *DNSRecordReconciler) CollectGarbage(ctx context.Context) error {
	log.Info("DNSRecord garbage collector started")
	if r.Service.DNSRecordStore == nil {
		return nil
	}
	crdList := &v1alpha1.DNSRecordList{}
	if err := r.Client.List(ctx, crdList); err != nil {
		log.Error(err, "failed to list DNSRecord CRs")
		return err
	}
	crdNames := sets.New[types.NamespacedName]()
	for _, item := range crdList.Items {
		crdNames.Insert(types.NamespacedName{Namespace: item.Namespace, Name: item.Name})
	}

	ownersByKind := r.Service.ListRecordOwnerResource()
	cachedDNSRecords := ownersByKind[dns.ResourceKindDNSRecord]
	var gcErrors []error
	for nn := range cachedDNSRecords {
		if crdNames.Has(nn) {
			continue
		}
		log.Info("GC removing stale DNSRecord on NSX", "Namespace", nn.Namespace, "Name", nn.Name)
		r.StatusUpdater.IncreaseDeleteTotal()
		if _, err := r.Service.DeleteRecordByOwnerNN(ctx, dns.ResourceKindDNSRecord, nn.Namespace, nn.Name); err != nil {
			r.StatusUpdater.IncreaseDeleteFailTotal()
			gcErrors = append(gcErrors, fmt.Errorf("failed to delete stale DNSRecord %s: %w", nn, err))
		} else {
			r.StatusUpdater.IncreaseDeleteSuccessTotal()
		}
	}
	return errors.Join(gcErrors...)
}

func (r *DNSRecordReconciler) RestoreReconcile() error {
	return nil
}
