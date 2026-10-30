/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dnsrecord

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"golang.org/x/net/idna"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/priorityqueue"
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
	// Internal request marker for retrying configuration-to-record mapping.
	// This is not a Kubernetes Namespace; underscores are invalid in Namespace names.
	networkConfigRetryNamespace = "_dnsconfig"
)

// DNSRecordReconciler reconciles a DNSRecord object
// +kubebuilder:rbac:groups=crd.nsx.vmware.com,resources=dnsrecords,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=crd.nsx.vmware.com,resources=dnsrecords/status,verbs=get;update;patch
type DNSRecordReconciler struct {
	Client        client.Client
	APIReader     client.Reader
	Scheme        *runtime.Scheme
	Service       *dns.DNSRecordService
	Recorder      record.EventRecorder
	StatusUpdater common.StatusUpdater

	queue            workqueue.TypedRateLimitingInterface[ctrl.Request]
	usePriorityQueue *bool
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
		APIReader:     mgr.GetAPIReader(),
		Scheme:        mgr.GetScheme(),
		Service:       dnsRecordService,
		Recorder:      recorder,
		StatusUpdater: common.NewStatusUpdater(mgr.GetClient(), nsxConfig, recorder, common.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}
}

func (r *DNSRecordReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if req.Namespace == networkConfigRetryNamespace {
		return r.reconcileNetworkConfig(ctx, req.Name)
	}
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
	if config.IsPerNamespaceProvidersSupported() {
		namespace := &corev1.Namespace{}
		err := r.Client.Get(ctx, types.NamespacedName{Name: obj.Namespace}, namespace)
		if err != nil && !apierrors.IsNotFound(err) {
			return ResultRequeue, fmt.Errorf("fetching Namespace %s for DNSRecord: %w", obj.Namespace, err)
		}
		if apierrors.IsNotFound(err) || !config.IsVPCNamespace(namespace) {
			validationErr := fmt.Errorf("Namespace %s is not an active VPC Namespace", obj.Namespace)
			return r.rejectInvalidRecord(ctx, obj, validationErr, "DNSRecord Namespace validation failed")
		}
	}

	switch obj.Spec.RecordType {
	case v1alpha1.DNSRecordTypeA, v1alpha1.DNSRecordTypeAAAA:
	default:
		err := fmt.Errorf("unsupported DNS record type %q: only A and AAAA are supported", obj.Spec.RecordType)
		return r.rejectInvalidRecord(ctx, obj, err, "DNS record type validation failed")
	}

	if obj.Spec.TTL != nil && (*obj.Spec.TTL < dns.MinRecordTTL || *obj.Spec.TTL > dns.MaxRecordTTL) {
		err := fmt.Errorf("DNS record TTL must be between %d and %d seconds, got %d", dns.MinRecordTTL, dns.MaxRecordTTL, *obj.Spec.TTL)
		return r.rejectInvalidRecord(ctx, obj, err, "DNS record TTL validation failed")
	}
	if err := validateRecordValues(obj.Spec.RecordType, obj.Spec.RecordValues); err != nil {
		return r.rejectInvalidRecord(ctx, obj, err, "DNS record value validation failed")
	}

	computedFQDN := calculateFQDN(obj.Spec.RecordName, obj.Spec.DomainName)
	if strings.HasPrefix(computedFQDN, "*.") {
		err := fmt.Errorf("wildcard DNS names are not supported: %q", computedFQDN)
		return r.rejectInvalidRecord(ctx, obj, err, "DNS record name validation failed")
	}
	if err := validateRecordFQDN(computedFQDN); err != nil {
		return r.rejectInvalidRecord(ctx, obj, err, "DNS record name validation failed")
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
		var validationErr *dns.DNSZoneValidationError
		if errors.As(err, &validationErr) {
			return r.rejectInvalidRecord(ctx, obj, err, "DNS zone validation failed")
		}
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
		reason := ReasonDNSRecordFailed
		message := "Failed to create or update DNS record on NSX"
		var validationErr *dns.DNSZoneValidationError
		if errors.As(err, &validationErr) {
			// Another owner may release the name without changing this DNSRecord.
			// Keep retrying and preserve the previously published record meanwhile.
			reason = ReasonDNSRecordValidationFailed
			message = "DNS record conflict or build validation failed"
		}
		statusErr := r.updateFailureStatus(ctx, obj, reason, err, message)
		return ResultRequeue, errors.Join(err, statusErr)
	}

	return r.updateSuccessStatus(ctx, obj)
}

func validateRecordValues(recordType v1alpha1.DNSRecordType, values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("DNS record must have at least one address")
	}
	seen := make(map[netip.Addr]struct{}, len(values))
	for _, value := range values {
		addr, err := netip.ParseAddr(value)
		if err != nil || addr.Zone() != "" ||
			(recordType == v1alpha1.DNSRecordTypeA && !addr.Is4()) ||
			(recordType == v1alpha1.DNSRecordTypeAAAA && (!addr.Is6() || addr.Is4In6())) {
			return fmt.Errorf("invalid %s record address %q", recordType, value)
		}
		if _, exists := seen[addr]; exists {
			return fmt.Errorf("duplicate %s record address %q", recordType, value)
		}
		seen[addr] = struct{}{}
	}
	return nil
}

func validateRecordFQDN(fqdn string) error {
	if fqdn == "" {
		return fmt.Errorf("DNS record FQDN must not be empty")
	}
	asciiLength := 0
	for _, label := range strings.Split(fqdn, ".") {
		if label == "" {
			return fmt.Errorf("DNS record FQDN %q has an empty label", fqdn)
		}
		asciiLabel := label
		if !strings.Contains(label, "_") {
			var err error
			asciiLabel, err = idna.Lookup.ToASCII(label)
			if err != nil {
				return fmt.Errorf("invalid DNS record FQDN label %q: %w", label, err)
			}
		}
		if len(asciiLabel) > 63 {
			return fmt.Errorf("DNS record FQDN label %q exceeds 63 bytes", label)
		}
		asciiLength += len(asciiLabel) + 1
	}
	if asciiLength-1 > 253 {
		return fmt.Errorf("DNS record FQDN %q exceeds 253 bytes", fqdn)
	}
	return nil
}

func (r *DNSRecordReconciler) rejectInvalidRecord(ctx context.Context, obj *v1alpha1.DNSRecord, validationErr error, message string) (ctrl.Result, error) {
	// A permanent validation failure must not leave an older version published.
	if _, err := r.Service.DeleteRecordByOwnerNN(ctx, dns.ResourceKindDNSRecord, obj.Namespace, obj.Name); err != nil {
		statusErr := r.updateFailureStatus(ctx, obj, ReasonDNSRecordFailed, err, "Failed to remove invalid DNS record from NSX")
		return ResultRequeue, errors.Join(err, statusErr)
	}
	if err := r.updateFailureStatus(ctx, obj, ReasonDNSRecordValidationFailed, validationErr, message); err != nil {
		return ResultRequeue, err
	}
	return ResultNormal, nil
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
	updated.Status.FQDN = calculateFQDN(updated.Spec.RecordName, updated.Spec.DomainName)
	fqdnChanged := updated.Status.FQDN != record.Status.FQDN
	conditionChanged := meta.SetStatusCondition(&updated.Status.Conditions, cond)
	if !fqdnChanged && !conditionChanged {
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
	vpcPredicate := common.VPCNamespacePredicate(r.Client)
	r.usePriorityQueue = mgr.GetControllerOptions().UsePriorityQueue
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.DNSRecord{}, builder.WithPredicates(predicateDNSRecordDesiredStateChanged(), vpcPredicate)).
		Watches(
			&v1alpha1.VPCNetworkConfiguration{},
			handler.EnqueueRequestsFromMapFunc(r.networkConfigToDNSRecordRequests),
			builder.WithPredicates(predicateNetworkConfigDNSSettingsChanged()),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: common.NumReconcile(),
			NewQueue:                r.getQueue,
		}).
		Complete(r)
}

func (r *DNSRecordReconciler) getQueue(name string, rateLimiter workqueue.TypedRateLimiter[ctrl.Request]) workqueue.TypedRateLimitingInterface[ctrl.Request] {
	// Preserve controller-runtime's default queue selection and rate limiter.
	if r.usePriorityQueue == nil || *r.usePriorityQueue {
		r.queue = priorityqueue.New(name, func(opts *priorityqueue.Opts[ctrl.Request]) {
			opts.RateLimiter = rateLimiter
		})
	} else {
		r.queue = workqueue.NewTypedRateLimitingQueueWithConfig(rateLimiter, workqueue.TypedRateLimitingQueueConfig[ctrl.Request]{Name: name})
	}
	return r.queue
}

func predicateDNSRecordDesiredStateChanged() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			_, ok := e.Object.(*v1alpha1.DNSRecord)
			return ok
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			_, ok := e.Object.(*v1alpha1.DNSRecord)
			return ok
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldRecord, oldOK := e.ObjectOld.(*v1alpha1.DNSRecord)
			newRecord, newOK := e.ObjectNew.(*v1alpha1.DNSRecord)
			if !oldOK || !newOK {
				return false
			}
			return oldRecord.Generation != newRecord.Generation ||
				(oldRecord.DeletionTimestamp.IsZero() && !newRecord.DeletionTimestamp.IsZero())
		},
		GenericFunc: func(e event.GenericEvent) bool {
			_, ok := e.Object.(*v1alpha1.DNSRecord)
			return ok
		},
	}
}

func (r *DNSRecordReconciler) networkConfigToDNSRecordRequests(ctx context.Context, obj client.Object) []ctrl.Request {
	nc, ok := obj.(*v1alpha1.VPCNetworkConfiguration)
	if !ok || nc == nil {
		return nil
	}
	reqs, err := r.requestsForNetworkConfig(ctx, nc.Name)
	if err != nil {
		log.Error(err, "Failed to enqueue DNSRecords for VPCNetworkConfiguration change", "NetworkConfig", nc.Name)
		// A synthetic request is retried by the controller until the lookup succeeds.
		reqs = append(reqs, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: networkConfigRetryNamespace, Name: nc.Name}})
	}
	return reqs
}

func (r *DNSRecordReconciler) requestsForNetworkConfig(ctx context.Context, name string) ([]ctrl.Request, error) {
	// A configuration annotated nsx.vmware.com/default=true is used by Namespaces
	// without an explicit nsx.vmware.com/vpc_network_config reference. Reconcile
	// all records to cover default configuration switches and deletions too.
	recordList := &v1alpha1.DNSRecordList{}
	if err := r.Client.List(ctx, recordList); err != nil {
		return nil, fmt.Errorf("listing DNSRecords for VPCNetworkConfiguration %s: %w", name, err)
	}
	reqs := make([]ctrl.Request, 0, len(recordList.Items))
	for i := range recordList.Items {
		reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&recordList.Items[i])})
	}
	return reqs, nil
}

func (r *DNSRecordReconciler) reconcileNetworkConfig(ctx context.Context, name string) (ctrl.Result, error) {
	reqs, err := r.requestsForNetworkConfig(ctx, name)
	if err != nil {
		return ResultRequeue, err
	}
	if r.queue == nil {
		return ResultRequeue, errors.New("DNSRecord controller queue is not initialized")
	}
	for _, req := range reqs {
		if ctx.Err() != nil {
			return ResultRequeue, ctx.Err()
		}
		r.queue.Add(req)
	}
	return ResultNormal, nil
}

func predicateNetworkConfigDNSSettingsChanged() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			_, ok := e.Object.(*v1alpha1.VPCNetworkConfiguration)
			return ok
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			_, ok := e.Object.(*v1alpha1.VPCNetworkConfiguration)
			return ok
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNC, oldOK := e.ObjectOld.(*v1alpha1.VPCNetworkConfiguration)
			newNC, newOK := e.ObjectNew.(*v1alpha1.VPCNetworkConfiguration)
			return oldOK && newOK && (!slices.Equal(oldNC.Spec.DNSZones, newNC.Spec.DNSZones) ||
				oldNC.Spec.NSXProject != newNC.Spec.NSXProject ||
				servicecommon.IsDefaultNetworkConfigCR(oldNC) != servicecommon.IsDefaultNetworkConfigCR(newNC))
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
	if r.APIReader == nil {
		return fmt.Errorf("APIReader is required for DNSRecord garbage collection")
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
		log.Info("GC checking potentially stale DNSRecord on NSX", "Namespace", nn.Namespace, "Name", nn.Name)
		confirmedMissing := false
		_, err := r.Service.DeleteRecordByOwnerNNIf(ctx, dns.ResourceKindDNSRecord, nn.Namespace, nn.Name, func(ctx context.Context) (bool, error) {
			record := &v1alpha1.DNSRecord{}
			err := r.APIReader.Get(ctx, nn, record)
			if err == nil {
				return false, nil
			}
			if apierrors.IsNotFound(err) {
				confirmedMissing = true
				return true, nil
			}
			return false, fmt.Errorf("rechecking DNSRecord %s before GC deletion: %w", nn, err)
		})
		if confirmedMissing {
			r.StatusUpdater.IncreaseDeleteTotal()
		}
		if err != nil {
			if confirmedMissing {
				r.StatusUpdater.IncreaseDeleteFailTotal()
			}
			gcErrors = append(gcErrors, fmt.Errorf("failed to delete stale DNSRecord %s: %w", nn, err))
		} else if confirmedMissing {
			r.StatusUpdater.IncreaseDeleteSuccessTotal()
		}
	}
	return errors.Join(gcErrors...)
}

func (r *DNSRecordReconciler) RestoreReconcile() error {
	return nil
}
