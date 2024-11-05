/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package service

import (
	"context"
	"os"
	"time"

	v1 "k8s.io/api/core/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/version"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	log           = &logger.Log
	ResultNormal  = common.ResultNormal
	ResultRequeue = common.ResultRequeue
	MetricResType = common.MetricResTypeServiceLb
)

// ServiceLbReconciler reconciles a Service LoadBalancer object
type ServiceLbReconciler struct {
	Client   client.Client
	Scheme   *apimachineryruntime.Scheme
	Service  *servicecommon.Service
	Recorder record.EventRecorder
}

func updateSuccess(r *ServiceLbReconciler, c context.Context, lbService *v1.Service) {
	r.setServiceLbStatus(c, lbService)
	r.Recorder.Event(lbService, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "LoadBalancer service has been successfully updated")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResType)
}

func (r *ServiceLbReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	service := &v1.Service{}
	log.Info("Reconciling LB CR", "service", req.NamespacedName)
	startTime := time.Now()
	defer func() {
		log.Info("Finished reconciling LB service", "LBService", req.NamespacedName, "duration(ms)", time.Since(startTime).Milliseconds())
	}()

	if err := r.Client.Get(ctx, req.NamespacedName, service); err != nil {
		log.Error(err, "Failed to fetch LB service", "req", req.NamespacedName)
		return ResultNormal, client.IgnoreNotFound(err)
	}

	if service.Spec.Type == v1.ServiceTypeLoadBalancer {
		log.Info("Reconciling LB service", "LBService", req.NamespacedName)
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, MetricResType)

		if service.ObjectMeta.DeletionTimestamp.IsZero() {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateTotal, MetricResType)
			updateSuccess(r, ctx, service)
		}
	}

	return ResultNormal, nil
}

func (r *ServiceLbReconciler) setServiceLbStatus(ctx context.Context, lbService *v1.Service) {
	ipMode := v1.LoadBalancerIPModeProxy
	statusUpdated := false
	// If nsx.vmware.com/ingress-ip-mode label with values proxy or vip,
	// the LoadBalancer serivice ipMode status would be set to whatever the label is set to,
	// Otherwise, it's set to Proxy by default when unset or other invalid values.
	if labelIpMode, ok := lbService.Labels[servicecommon.LabelLbIngressIpMode]; ok {
		if labelIpMode == servicecommon.LabelLbIngressIpModeVipValue {
			ipMode = v1.LoadBalancerIPModeVIP
		}
	}
	for i, ing := range lbService.Status.LoadBalancer.Ingress {
		if ing.IP != "" {
			if ing.IPMode == nil || *(ing.IPMode) != ipMode {
				lbService.Status.LoadBalancer.Ingress[i].IPMode = &ipMode
				statusUpdated = true
			}
		}
	}

	if statusUpdated {
		r.Client.Status().Update(ctx, lbService)
		log.V(1).Info("Updated LB service status ipMode", "Name", lbService.Name, "Namespace", lbService.Namespace, "ipMode", ipMode)
	}
}

func (r *ServiceLbReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Service{}).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Complete(r)
}

// Start setup manager
func (r *ServiceLbReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}

	return nil
}

func isServiceLbStatusIpModeSupported(c *rest.Config) bool {
	version129, _ := version.ParseGeneric("v1.29.0")

	clientset, err := clientset.NewForConfig(c)
	if err != nil {
		log.Error(err, "Failed to create clientset")
		return false
	}

	serverVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		log.Error(err, "Failed to get server Kubernetes version")
		return false
	}

	runningVersion, err := version.ParseGeneric(serverVersion.String())
	if err != nil {
		log.Error(err, "Failed to parse server Kubernetes version", "K8sVersion", runningVersion.String())
		return false
	}

	log.Info("Running server Kubernetes version is", "K8sVersion", runningVersion.String())
	return runningVersion.AtLeast(version129)
}

func StartServiceLbController(mgr ctrl.Manager, commonService servicecommon.Service) {
	if isServiceLbStatusIpModeSupported(mgr.GetConfig()) {

		serviceLbReconciler := ServiceLbReconciler{
			Client:   mgr.GetClient(),
			Scheme:   mgr.GetScheme(),
			Recorder: mgr.GetEventRecorderFor("serviceLb-controller"),
		}
		serviceLbReconciler.Service = &commonService
		if err := serviceLbReconciler.Start(mgr); err != nil {
			log.Error(err, "Failed to create controller", "controller", "ServiceLb")
			os.Exit(1)
		}
	} else {
		log.Info("Service LB controller isn't started since load balancer service ipMode supporting needs K8s version at least 1.29.0")
	}
}
