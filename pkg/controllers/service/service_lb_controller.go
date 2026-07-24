/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package service

import (
	"context"
	"fmt"
	"os"
	"time"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/version"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
)

var (
	log           = logger.Log
	ResultNormal  = common.ResultNormal
	ResultRequeue = common.ResultRequeue
	MetricResType = common.MetricResTypeServiceLb
)

// ServiceLbReconciler reconciles a Service LoadBalancer object
type ServiceLbReconciler struct {
	Client   client.Client
	Scheme   *apimachineryruntime.Scheme
	Service  *servicecommon.Service
	DNS      dns.DNSRecordProvider
	Recorder record.EventRecorder
}

func updateSuccess(r *ServiceLbReconciler, c context.Context, lbService *v1.Service) error {
	err := r.setServiceLbStatus(c, lbService)
	if err == nil {
		r.Recorder.Event(lbService, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "LoadBalancer service has been successfully updated")
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResType)
		return nil
	}
	r.Recorder.Event(lbService, v1.EventTypeWarning, common.ReasonFailUpdate, "Failed to update LoadBalancer service")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResType)
	return err
}

func (r *ServiceLbReconciler) deleteDNSForService(ctx context.Context, namespace, name string, op string) error {
	if _, err := r.DNS.DeleteRecordByOwnerNN(ctx, dns.ResourceKindService, namespace, name); err != nil {
		log.Error(err, "Failed to delete DNS records for Service", "Namespace", namespace, "Name", name, "Operation", op)
		return fmt.Errorf("deleting DNS records for %s: %w", op, err)
	}
	return nil
}

func (r *ServiceLbReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	service := &v1.Service{}
	startTime := time.Now()
	defer func() {
		log.Info("Finished reconciling LB service", "LBService", req.NamespacedName, "duration(ms)", time.Since(startTime).Milliseconds())
	}()

	if err := r.Client.Get(ctx, req.NamespacedName, service); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Not found LB service", "req", req.NamespacedName)
			if err := r.deleteDNSForService(ctx, req.Namespace, req.Name, "deleted Service"); err != nil {
				return common.ResultRequeueAfter10sec, nil
			}
			return ResultNormal, nil
		}
		log.Error(err, "Failed to fetch LB service", "req", req.NamespacedName)
		return common.ResultRequeueAfter10sec, nil
	}

	if service.Spec.Type != v1.ServiceTypeLoadBalancer || !service.ObjectMeta.DeletionTimestamp.IsZero() {
		// Try to delete DNS records for Service when it is not a LoadBalancer or is marked for deletion
		if err := r.clearDNSAndConditionForService(ctx, req.NamespacedName, "non-LB or terminating Service"); err != nil {
			return common.ResultRequeueAfter10sec, nil
		}
		return ResultNormal, nil
	}

	log.Info("Reconciling LB service", "LBService", req.NamespacedName)
	log.Debug("Reconciling LB Service", "name", service.Name, "version", service.ResourceVersion, "status", service.Status)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, MetricResType)

	var dnsErr error
	if err := r.reconcileLoadBalancerServiceDNS(ctx, service); err != nil {
		log.Error(err, "Failed to reconcile DNS for LoadBalancer Service", "Name", service.Name, "Namespace", service.Namespace)
		dnsErr = fmt.Errorf("reconciling DNS: %w", err)
	}

	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateTotal, MetricResType)
	// Even if DNS reconciliation fails, we must proceed to update the Service status.
	// This ensures that successfully allocated LoadBalancer external IPs are properly
	// reflected in the Service status, allowing traffic to flow while DNS issues are resolved.
	if err := updateSuccess(r, ctx, service); err != nil {
		log.Error(err, "Failed to update LB service", "Name", service.Name, "Namespace", service.Namespace)
		return common.ResultRequeueAfter10sec, nil
	}

	if dnsErr != nil {
		return common.ResultRequeueAfter10sec, nil
	}

	return ResultNormal, nil
}

func (r *ServiceLbReconciler) setServiceLbStatus(ctx context.Context, lbService *v1.Service) error {
	ipMode := v1.LoadBalancerIPModeProxy
	// If nsx.vmware.com/ingress-ip-mode label with values proxy or vip,
	// the LoadBalancer service ipMode status would be set to whatever the label is set to,
	// Otherwise, it's set to Proxy by default when unset or other invalid values.
	if labelIpMode, ok := lbService.Labels[servicecommon.LabelLbIngressIpMode]; ok {
		if labelIpMode == servicecommon.LabelLbIngressIpModeVipValue {
			ipMode = v1.LoadBalancerIPModeVIP
		}
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		svc := &v1.Service{}
		if err := r.Client.Get(ctx, types.NamespacedName{Name: lbService.Name, Namespace: lbService.Namespace}, svc); err != nil {
			return client.IgnoreNotFound(err)
		}

		statusUpdated := false
		for i, ing := range svc.Status.LoadBalancer.Ingress {
			if ing.IP != "" {
				if ing.IPMode == nil || *(ing.IPMode) != ipMode {
					svc.Status.LoadBalancer.Ingress[i].IPMode = &ipMode
					statusUpdated = true
				}
			}
		}

		if statusUpdated {
			err := r.Client.Status().Update(ctx, svc)
			if err != nil {
				log.Error(err, "Failed to update LB service status ipMode", "Name", svc.Name, "Namespace", svc.Namespace, "ipMode", ipMode)
				return err
			}
			log.Info("Updated LB service status ipMode", "Name", svc.Name, "Namespace", svc.Namespace, "ipMode", ipMode)
		}
		return nil
	})
}

func (r *ServiceLbReconciler) setupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&v1.Service{}).
		Watches(
			&v1alpha1.NetworkInfo{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueLBServiceRequestsFromNetworkInfo),
			builder.WithPredicates(predicateNetworkInfoAllowedDNSDomainsChanged()),
		).
		WithEventFilter(common.VPCNamespacePredicate(r.Client)).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			})
	return b.Complete(r)
}

// Start setup manager
func (r *ServiceLbReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}

	return nil
}

func isServiceLbStatusIpModeSupported(c *rest.Config) (bool, error) {
	version129, _ := version.ParseGeneric("v1.29.0")

	clientset, err := clientset.NewForConfig(c)
	if err != nil {
		log.Error(err, "Failed to create clientset")
		return false, err
	}

	serverVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		log.Error(err, "Failed to get server Kubernetes version")
		return false, err
	}

	runningVersion, err := version.ParseGeneric(serverVersion.String())
	if err != nil {
		log.Error(err, "Failed to parse server Kubernetes version", "K8sVersion", runningVersion.String())
		return false, err
	}

	log.Info("Running server Kubernetes version is", "K8sVersion", runningVersion.String())
	return runningVersion.AtLeast(version129), nil
}

func (r *ServiceLbReconciler) RestoreReconcile() error {
	return nil
}

func (r *ServiceLbReconciler) StartController(mgr ctrl.Manager, _ webhook.Server) error {
	if err := r.Start(mgr); err != nil {
		log.Error(err, "Failed to create controller", "controller", "ServiceLb")
		return err
	}
	err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		stop := make(chan bool)
		go func() {
			<-ctx.Done()
			close(stop)
		}()
		common.GenericGarbageCollector(stop, servicecommon.GCInterval, r.CollectGarbage)
		return nil
	}))
	if err != nil {
		log.Error(err, "Failed to add LB GC to manager")
		return err
	}
	return nil
}

func (r *ServiceLbReconciler) CollectGarbage(ctx context.Context) error {
	return r.collectDNSGarbage(ctx)
}

func NewServiceLbReconciler(mgr ctrl.Manager, commonService servicecommon.Service, dnsRecordService *dns.DNSRecordService) *ServiceLbReconciler {
	supported, err := isServiceLbStatusIpModeSupported(mgr.GetConfig())
	if err != nil {
		log.Error(err, "Failed to check if Service LB status ipMode is supported")
		os.Exit(1)
	}

	if supported {
		var dnsProv dns.DNSRecordProvider
		if dnsRecordService != nil {
			dnsProv = dnsRecordService
		}
		serviceLbReconciler := &ServiceLbReconciler{
			Client:   mgr.GetClient(),
			Scheme:   mgr.GetScheme(),
			DNS:      dnsProv,
			Recorder: mgr.GetEventRecorderFor("serviceLb-controller"), //nolint:staticcheck // record.EventRecorder; StatusUpdater not on events.EventRecorder yet
		}
		serviceLbReconciler.Service = &commonService
		return serviceLbReconciler
	}
	log.Info("Service LB controller isn't started since load balancer service ipMode supporting needs K8s version at least 1.29.0")
	return nil
}
