/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsxserviceaccount

import (
	"context"
	"errors"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	nsxvmwarecomv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/nsxserviceaccount"
)

const proxyLabelKey = "mgmt-proxy.antrea-nsx.vmware.com"

var (
	proxyServicePred = predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldObj := e.ObjectOld.(*corev1.Service)
			newObj := e.ObjectNew.(*corev1.Service)

			newLabels := e.ObjectNew.GetLabels()
			oldLabels := e.ObjectOld.GetLabels()
			_, oldObjHasLabel := oldLabels[proxyLabelKey]
			_, newObjHasLabel := newLabels[proxyLabelKey]

			// Reconcile all NSXSA if label "mgmt-proxy.antrea-nsx.vmware.com" added to or removed from existing Service
			if oldObjHasLabel != newObjHasLabel {
				return true
			}

			// Reconcile all NSXSA if existing Service with label "mgmt-proxy.antrea-nsx.vmware.com" is updated with Service LB VIP
			if oldObjHasLabel && newObjHasLabel {
				// Service type updated to LB
				if oldObj.Spec.Type != corev1.ServiceTypeLoadBalancer && newObj.Spec.Type == corev1.ServiceTypeLoadBalancer {
					return true
				}
				// Service type changed from LB to some other type
				if oldObj.Spec.Type == corev1.ServiceTypeLoadBalancer && newObj.Spec.Type != corev1.ServiceTypeLoadBalancer {
					return true
				}
				// Service LB VIP updated
				if oldObj.Spec.Type == corev1.ServiceTypeLoadBalancer && newObj.Spec.Type == corev1.ServiceTypeLoadBalancer {
					if len(oldObj.Status.LoadBalancer.Ingress) != len(newObj.Status.LoadBalancer.Ingress) {
						return true
					}
					for i := range newObj.Status.LoadBalancer.Ingress {
						if oldObj.Status.LoadBalancer.Ingress[i].IP != newObj.Status.LoadBalancer.Ingress[i].IP {
							return true
						}
					}
				}
			}
			return false
		},

		// Allow create events for Service with label "mgmt-proxy.antrea-nsx.vmware.com"
		CreateFunc: func(e event.CreateEvent) bool {
			labels := e.Object.GetLabels()
			if _, ok := labels[proxyLabelKey]; ok {
				return true
			}
			return false
		},

		// Allow delete events for Service with label "mgmt-proxy.antrea-nsx.vmware.com"
		DeleteFunc: func(e event.DeleteEvent) bool {
			labels := e.Object.GetLabels()
			if _, ok := labels[proxyLabelKey]; ok {
				return true
			}
			return false
		},

		GenericFunc: func(genericEvent event.GenericEvent) bool {
			return false
		},
	}
)

var (
	log                     = logger.Log
	ResultNormal            = common.ResultNormal
	ResultRequeue           = common.ResultRequeue
	ResultRequeueAfter5mins = common.ResultRequeueAfter5mins
	MetricResType           = common.MetricResTypeNSXServiceAccount
	count                   = uint16(0)
	ca                      []byte
)

// NSXServiceAccountReconciler reconciles a NSXServiceAccount object.
// Requires NSXT 4.0.1
//
// create/delete event will be processed by Reconcile
//
// update event with realized resource missing NSX resources will be processed by Reconcile since NSXT 4.1.2
//
// # GarbageCollector will clean up stale NSX resources and Secret on every GCInterval
//
// GarbageCollector will check and make all Secrets' CA up-to-date on first GC run
//
// GarbageCollector will check and rotate client cert if needed on every GCValidationInterval*GCInterval since NSXT 4.1.3
type NSXServiceAccountReconciler struct {
	client.Client
	Scheme        *apimachineryruntime.Scheme
	Service       *nsxserviceaccount.NSXServiceAccountService
	Recorder      record.EventRecorder
	StatusUpdater common.StatusUpdater
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.0/pkg/reconcile
func (r *NSXServiceAccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &nsxvmwarecomv1alpha1.NSXServiceAccount{}
	log.Info("reconciling CR", "nsxserviceaccount", req.NamespacedName)

	r.StatusUpdater.IncreaseSyncTotal()

	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		log.Error(err, "unable to fetch NSXServiceAccount CR", "req", req.NamespacedName)
		return ResultNormal, client.IgnoreNotFound(err)
	}

	// Since NSXServiceAccount service can only be activated from NSX 4.1.0 onwards,
	// So need to check NSX version before starting NSXServiceAccount reconcile
	if !r.Service.NSXClient.NSXCheckVersion(nsx.ServiceAccount) {
		err := errors.New("NSX version check failed, NSXServiceAccount feature is not supported")
		r.StatusUpdater.UpdateFail(ctx, obj, err, "", updateNSXServiceAccountStatuswithError)
		// if NSX version check fails, it will be put back to reconcile queue and be reconciled after 5 minutes
		return ResultRequeueAfter5mins, nil
	}

	if obj.ObjectMeta.DeletionTimestamp.IsZero() {
		r.StatusUpdater.IncreaseUpdateTotal()
		if !controllerutil.ContainsFinalizer(obj, servicecommon.NSXServiceAccountFinalizerName) {
			controllerutil.AddFinalizer(obj, servicecommon.NSXServiceAccountFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "add finalizer", "nsxserviceaccount", req.NamespacedName)
				r.StatusUpdater.UpdateFail(ctx, obj, err, "", updateNSXServiceAccountStatuswithError)
				return ResultRequeue, err
			}
			log.Debug("added finalizer on CR", "nsxserviceaccount", req.NamespacedName)
		}

		if nsxserviceaccount.IsNSXServiceAccountRealized(&obj.Status) {
			if r.Service.NSXClient.NSXCheckVersion(nsx.ServiceAccountRestore) {
				if err := r.Service.RestoreRealizedNSXServiceAccount(ctx, obj); err != nil {
					log.Error(err, "update realized failed, would retry exponentially", "nsxserviceaccount", req.NamespacedName)
					r.StatusUpdater.IncreaseDeleteFailTotal()
					return ResultRequeue, err
				}
			}
			// update ProxyEndpoints if it has changed.
			if err := r.Service.UpdateProxyEndpointsIfNeeded(ctx, obj); err != nil {
				r.StatusUpdater.UpdateFail(ctx, obj, err, "", updateNSXServiceAccountStatuswithError)
				return ResultRequeue, err
			}
			r.StatusUpdater.UpdateSuccess(ctx, obj, updateNSXServiceAccountStatus)
			return ResultNormal, nil
		}
		if err := r.Service.CreateOrUpdateNSXServiceAccount(ctx, obj); err != nil {
			r.StatusUpdater.UpdateFail(ctx, obj, err, "", updateNSXServiceAccountStatuswithError)
			return ResultRequeue, err
		}
		r.StatusUpdater.UpdateSuccess(ctx, obj, updateNSXServiceAccountStatus)
	} else {
		if controllerutil.ContainsFinalizer(obj, servicecommon.NSXServiceAccountFinalizerName) {
			r.StatusUpdater.IncreaseDeleteTotal()
			if err := r.Service.DeleteNSXServiceAccount(ctx, types.NamespacedName{
				Namespace: obj.Namespace,
				Name:      obj.Name,
			}, obj.UID); err != nil {
				r.StatusUpdater.DeleteFail(req.NamespacedName, obj, err)
				return ResultRequeue, err
			}
			controllerutil.RemoveFinalizer(obj, servicecommon.NSXServiceAccountFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "removing finalizer failed, would retry exponentially", "nsxserviceaccount", req.NamespacedName)
				r.StatusUpdater.DeleteFail(req.NamespacedName, obj, err)
				return ResultRequeue, err
			}
			r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
		} else {
			// only print a message because it's not a normal case
			log.Info("finalizers cannot be recognized", "nsxserviceaccount", req.NamespacedName)
		}
	}

	return ResultNormal, nil
}

// setupWithManager sets up the controller with the Manager.
func (r *NSXServiceAccountReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&nsxvmwarecomv1alpha1.NSXServiceAccount{}).
		WithEventFilter(predicate.Funcs{
			DeleteFunc: func(e event.DeleteEvent) bool {
				// Suppress Delete events to avoid filtering them out in the Reconcile function
				return false
			},
		}).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Watches(
			&corev1.Service{},
			handler.EnqueueRequestsFromMapFunc(r.serviceMapFunc),
			builder.WithPredicates(proxyServicePred),
		).
		Complete(r)
}

func (r *NSXServiceAccountReconciler) serviceMapFunc(ctx context.Context, _ client.Object) []reconcile.Request {
	var requests []reconcile.Request
	nsxServiceAccountList := &nsxvmwarecomv1alpha1.NSXServiceAccountList{}

	err := retry.OnError(retry.DefaultRetry, func(err error) bool {
		return err != nil
	}, func() error {
		err := r.Client.List(ctx, nsxServiceAccountList)
		return err
	})
	if err != nil {
		log.Error(err, "failed to list NSXServiceAccount in Service handler")
		return requests
	}

	for _, nsxserviceaccount := range nsxServiceAccountList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: nsxserviceaccount.GetNamespace(),
				Name:      nsxserviceaccount.GetName(),
			},
		})
	}
	return requests
}

// Start setup manager and launch GC
func (r *NSXServiceAccountReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}
	return nil
}

// CollectGarbage collect NSXServiceAccount which has been removed from crd.
// it implements the interface GarbageCollector method.
func (r *NSXServiceAccountReconciler) CollectGarbage(ctx context.Context) error {
	log.Info("nsx service account garbage collector started")
	ca = r.Service.NSXConfig.GetCACert()
	nsxServiceAccountList := &nsxvmwarecomv1alpha1.NSXServiceAccountList{}
	var gcSuccessCount, gcErrorCount uint32
	var err error
	nsxServiceAccountUIDSet := r.Service.ListNSXServiceAccountRealization()
	if len(nsxServiceAccountUIDSet) == 0 {
		return nil
	}
	err = r.Client.List(ctx, nsxServiceAccountList)
	if err != nil {
		log.Error(err, "failed to list NSXServiceAccount CR")
		return err
	}
	gcSuccessCount, gcErrorCount = r.garbageCollector(nsxServiceAccountUIDSet, nsxServiceAccountList)
	log.Debug("gc collects NSXServiceAccount CR", "success", gcSuccessCount, "error", gcErrorCount)
	count, ca = r.validateRealized(count, ca, nsxServiceAccountList)
	if gcErrorCount > 0 {
		return fmt.Errorf("errors found in NSXServiceAccount garbage collection: %d", gcErrorCount)
	}
	return nil
}

func (r *NSXServiceAccountReconciler) RestoreReconcile() error {
	return nil
}

func (r *NSXServiceAccountReconciler) StartController(mgr ctrl.Manager, _ webhook.Server) error {
	log.Info("Starting NSXServiceAccountController")
	if err := r.Start(mgr); err != nil {
		log.Error(err, "Failed to create controller", "controller", "NSXServiceAccount")
		return err
	}
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, r.CollectGarbage)
	return nil
}

func NewNSXServiceAccountReconciler(mgr ctrl.Manager, commonService servicecommon.Service) *NSXServiceAccountReconciler {
	log.Info("Initializing NSXServiceAccountController")
	nsxServiceAccountReconcile := &NSXServiceAccountReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("nsxserviceaccount-controller"),
	}
	nsxServiceAccountService, err := nsxserviceaccount.InitializeNSXServiceAccount(commonService)
	if err != nil {
		log.Error(err, "Failed to initialize service", "controller", "NSXServiceAccount")
		os.Exit(1)
	}
	nsxServiceAccountReconcile.Service = nsxServiceAccountService
	nsxServiceAccountReconcile.StatusUpdater = common.NewStatusUpdater(nsxServiceAccountReconcile.Client, nsxServiceAccountReconcile.Service.NSXConfig, nsxServiceAccountReconcile.Recorder, common.MetricResTypeNSXServiceAccount, "ServiceAccount", "NSXServiceAccount")
	return nsxServiceAccountReconcile
}

func (r *NSXServiceAccountReconciler) validateRealized(count uint16, ca []byte, nsxServiceAccountList *nsxvmwarecomv1alpha1.NSXServiceAccountList) (uint16, []byte) {
	// Validate ca at first time
	// Validate client cert every GCValidationInterval
	if count == 0 {
		for _, account := range nsxServiceAccountList.Items {
			nsxServiceAccount := account
			if nsxserviceaccount.IsNSXServiceAccountRealized(&nsxServiceAccount.Status) {
				if err := r.Service.ValidateAndUpdateRealizedNSXServiceAccount(context.TODO(), &nsxServiceAccount, ca); err != nil {
					log.Error(err, "Failed to update realized NSXServiceAccount", "namespace", nsxServiceAccount.Namespace, "name", nsxServiceAccount.Name)
				}
			}
		}
		ca = nil
	}
	count++
	if count == servicecommon.GCValidationInterval {
		count = 0
	}
	return count, ca
}

func (r *NSXServiceAccountReconciler) garbageCollector(nsxServiceAccountUIDSet sets.Set[string], nsxServiceAccountList *nsxvmwarecomv1alpha1.NSXServiceAccountList) (gcSuccessCount, gcErrorCount uint32) {
	nsxServiceAccountCRUIDMap := map[string]types.NamespacedName{}
	for _, nsxServiceAccount := range nsxServiceAccountList.Items {
		nsxServiceAccountCRUIDMap[string(nsxServiceAccount.UID)] = types.NamespacedName{
			Namespace: nsxServiceAccount.Namespace,
			Name:      nsxServiceAccount.Name,
		}
	}

	for nsxServiceAccountUID := range nsxServiceAccountUIDSet {
		if _, ok := nsxServiceAccountCRUIDMap[nsxServiceAccountUID]; ok {
			continue
		}
		log.Debug("gc collects NSXServiceAccount CR", "UID", nsxServiceAccountUID)
		namespacedName := r.Service.GetNSXServiceAccountNameByUID(nsxServiceAccountUID)
		if namespacedName.Namespace == "" || namespacedName.Name == "" {
			log.Info("gc cannot get namespace/name, skip", "namespace", namespacedName.Namespace, "name", namespacedName.Name, "uid", nsxServiceAccountUID)
			continue
		}
		r.StatusUpdater.IncreaseDeleteTotal()
		err := r.Service.DeleteNSXServiceAccount(context.TODO(), namespacedName, types.UID(nsxServiceAccountUID))
		if err != nil {
			gcErrorCount++
			r.StatusUpdater.IncreaseDeleteFailTotal()
		} else {
			gcSuccessCount++
			r.StatusUpdater.IncreaseDeleteSuccessTotal()
		}
	}
	return
}

func updateNSXServiceAccountStatus(client client.Client, ctx context.Context, obj client.Object, _ metav1.Time, _ ...interface{}) {
	nsa := obj.(*nsxvmwarecomv1alpha1.NSXServiceAccount)
	err := client.Status().Update(ctx, obj)
	if err != nil {
		log.Error(err, "Update NSXServiceAccount failed", "Namespace", nsa.Namespace, "Name", nsa.Name, "Status", nsa.Status)
	} else {
		log.Debug("Updated NSXServiceAccount", "Namespace", nsa.Namespace, "Name", nsa.Name, "Status", nsa.Status)
	}
}

func updateNSXServiceAccountStatuswithError(client client.Client, ctx context.Context, o client.Object, _ metav1.Time, e error, _ ...interface{}) {
	obj := o.(*nsxvmwarecomv1alpha1.NSXServiceAccount)
	nsa := obj
	if e != nil {
		nsa = obj.DeepCopy()
		nsa.Status.Phase = nsxvmwarecomv1alpha1.NSXServiceAccountPhaseFailed
		nsa.Status.Reason = fmt.Sprintf("Error: %v", e)
		nsa.Status.Conditions = nsxserviceaccount.GenerateNSXServiceAccountConditions(nsa.Status.Conditions, nsa.Generation, metav1.ConditionFalse, nsxvmwarecomv1alpha1.ConditionReasonRealizationError, fmt.Sprintf("Error: %v", e))
	}
	err := client.Status().Update(ctx, nsa)
	if err != nil {
		log.Error(err, "Update NSXServiceAccount failed", "Namespace", nsa.Namespace, "Name", nsa.Name, "Status", nsa.Status)
	} else {
		log.Debug("Updated NSXServiceAccount", "Namespace", nsa.Namespace, "Name", nsa.Name, "Status", nsa.Status)
	}
}
