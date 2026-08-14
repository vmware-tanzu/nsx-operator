/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dnsrecord

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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

// DNSRecordReconciler reconciles a DNSRecord object
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
	recorder := mgr.GetEventRecorderFor("dnsrecord-controller") //nolint:staticcheck // record.EventRecorder; StatusUpdater not on events.EventRecorder yet
	var nsxConfig *config.NSXOperatorConfig
	if dnsRecordService != nil && dnsRecordService.NSXConfig != nil {
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
			if r.Service != nil {
				if _, err := r.Service.DeleteRecordByOwnerNN(ctx, dns.ResourceKindDNSRecord, req.Namespace, req.Name); err != nil {
					log.Error(err, "Failed to delete DNS record from NSX on NotFound", "DNSRecord", req.NamespacedName)
					r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
					return ResultRequeue, err
				}
			}
			r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
			return ResultNormal, nil
		}
		log.Error(err, "unable to fetch DNSRecord CR", "req", req.NamespacedName)
		return ResultRequeue, err
	}

	if !obj.ObjectMeta.DeletionTimestamp.IsZero() {
		r.StatusUpdater.IncreaseDeleteTotal()
		if r.Service != nil {
			if _, err := r.Service.DeleteRecordByOwnerNN(ctx, dns.ResourceKindDNSRecord, obj.Namespace, obj.Name); err != nil {
				log.Error(err, "Failed to delete DNS record from NSX", "DNSRecord", req.NamespacedName)
				r.StatusUpdater.DeleteFail(req.NamespacedName, obj, err)
				return ResultRequeue, err
			}
		}
		if controllerutil.ContainsFinalizer(obj, servicecommon.DNSRecordFinalizerName) {
			controllerutil.RemoveFinalizer(obj, servicecommon.DNSRecordFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "Failed to remove finalizer from DNSRecord CR", "DNSRecord", req.NamespacedName)
				r.StatusUpdater.DeleteFail(req.NamespacedName, obj, err)
				return ResultRequeue, err
			}
		}
		r.StatusUpdater.DeleteSuccess(req.NamespacedName, obj)
		return ResultNormal, nil
	}

	r.StatusUpdater.IncreaseUpdateTotal()

	if !controllerutil.ContainsFinalizer(obj, servicecommon.DNSRecordFinalizerName) {
		controllerutil.AddFinalizer(obj, servicecommon.DNSRecordFinalizerName)
		if err := r.Client.Update(ctx, obj); err != nil {
			log.Error(err, "Failed to add finalizer to DNSRecord CR", "DNSRecord", req.NamespacedName)
			r.StatusUpdater.UpdateFail(ctx, obj, err, "Failed to add finalizer", setDNSRecordReadyStatusFalse)
			return ResultRequeue, err
		}
	}

	computedFQDN := calculateFQDN(obj.Spec.RecordName, obj.Spec.DomainName)
	if obj.Spec.FQDN != computedFQDN {
		obj.Spec.FQDN = computedFQDN
		if err := r.Client.Update(ctx, obj); err != nil {
			log.Error(err, "Failed to update FQDN on DNSRecord CR", "DNSRecord", req.NamespacedName)
			r.StatusUpdater.UpdateFail(ctx, obj, err, "Failed to update FQDN", setDNSRecordReadyStatusFalse)
			return ResultRequeue, err
		}
	}

	if r.Service == nil {
		r.StatusUpdater.UpdateSuccess(ctx, obj, setDNSRecordReadyStatusTrue)
		return ResultNormal, nil
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
		r.StatusUpdater.UpdateFail(ctx, obj, err, "DNS zone validation failed", setDNSRecordReadyStatusFalse)
		return ResultRequeue, err
	}

	batch := dns.NewOwnerScopedAggregatedRouteDNS(owner, rows)
	if _, err := r.Service.CreateOrUpdateRecords(ctx, batch); err != nil {
		log.Error(err, "Failed to sync DNSRecord to NSX", "DNSRecord", req.NamespacedName)
		r.StatusUpdater.UpdateFail(ctx, obj, err, "Failed to create or update DNS record on NSX", setDNSRecordReadyStatusFalse)
		return ResultRequeue, err
	}

	r.StatusUpdater.UpdateSuccess(ctx, obj, setDNSRecordReadyStatusTrue)
	return ResultNormal, nil
}

func setDNSRecordReadyStatusTrue(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, _ ...interface{}) {
	record := obj.(*v1alpha1.DNSRecord)
	cond := metav1.Condition{
		Type:               string(v1alpha1.Ready),
		Status:             metav1.ConditionTrue,
		Reason:             "DNSRecordReady",
		Message:            "NSX DNS record successfully created/updated",
		ObservedGeneration: record.Generation,
		LastTransitionTime: transitionTime,
	}
	updateDNSRecordStatusConditions(client, ctx, record, cond)
}

func setDNSRecordReadyStatusFalse(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, err error, _ ...interface{}) {
	record := obj.(*v1alpha1.DNSRecord)
	cond := metav1.Condition{
		Type:               string(v1alpha1.Ready),
		Status:             metav1.ConditionFalse,
		Reason:             "DNSRecordFailed",
		Message:            fmt.Sprintf("Failed to process DNSRecord: %v", err),
		ObservedGeneration: record.Generation,
		LastTransitionTime: transitionTime,
	}
	updateDNSRecordStatusConditions(client, ctx, record, cond)
}

func updateDNSRecordStatusConditions(k8sClient client.Client, ctx context.Context, record *v1alpha1.DNSRecord, cond metav1.Condition) {
	if meta.SetStatusCondition(&record.Status.Conditions, cond) {
		if err := k8sClient.Status().Update(ctx, record); err != nil {
			log.Error(err, "Failed to update DNSRecord status", "Name", record.Name, "Namespace", record.Namespace)
		} else {
			log.Info("Updated DNSRecord status", "Name", record.Name, "Namespace", record.Namespace, "Status", record.Status)
		}
	}
}

func (r *DNSRecordReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.DNSRecord{}).
		WithEventFilter(common.VPCNamespacePredicate(r.Client)).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: common.NumReconcile(),
		}).
		Complete(r)
}

func (r *DNSRecordReconciler) StartController(mgr ctrl.Manager, _ webhook.Server) error {
	if err := r.setupWithManager(mgr); err != nil {
		log.Error(err, "Failed to create controller", "controller", "DNSRecord")
		return err
	}
	return nil
}

func (r *DNSRecordReconciler) CollectGarbage(ctx context.Context) error {
	log.Info("DNSRecord garbage collector started")
	if r.Service == nil || r.Service.DNSRecordStore == nil {
		return nil
	}
	crdList := &v1alpha1.DNSRecordList{}
	if err := r.Client.List(ctx, crdList); err != nil {
		log.Error(err, "failed to list DNSRecord CRs")
		return err
	}
	crdNamespacesAndNames := make(map[string]struct{})
	for _, item := range crdList.Items {
		crdNamespacesAndNames[fmt.Sprintf("%s/%s", item.Namespace, item.Name)] = struct{}{}
	}

	nsxRecords := r.Service.DNSRecordStore.ListDNSRecords()
	for _, rec := range nsxRecords {
		if rec == nil {
			continue
		}
		for _, tag := range rec.Tags {
			if tag.Scope != nil && *tag.Scope == servicecommon.TagScopeDNSRecordFor && tag.Tag != nil && *tag.Tag == servicecommon.TagValueDNSRecordForDNSRecord {
				ns := ""
				name := ""
				for _, t := range rec.Tags {
					if t.Scope == nil || t.Tag == nil {
						continue
					}
					switch *t.Scope {
					case servicecommon.TagScopeDNSRecordOwnerNamespace:
						ns = *t.Tag
					case servicecommon.TagScopeDNSRecordOwnerName:
						name = *t.Tag
					}
				}
				if ns != "" && name != "" {
					key := fmt.Sprintf("%s/%s", ns, name)
					if _, exists := crdNamespacesAndNames[key]; !exists {
						log.Info("GC removing stale DNSRecord on NSX", "Namespace", ns, "Name", name)
						r.StatusUpdater.IncreaseDeleteTotal()
						if _, err := r.Service.DeleteRecordByOwnerNN(ctx, dns.ResourceKindDNSRecord, ns, name); err != nil {
							r.StatusUpdater.IncreaseDeleteFailTotal()
						} else {
							r.StatusUpdater.IncreaseDeleteSuccessTotal()
						}
					}
				}
			}
		}
	}
	return nil
}

func (r *DNSRecordReconciler) RestoreReconcile() error {
	return nil
}
