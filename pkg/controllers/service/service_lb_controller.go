/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package service

import (
	"context"
	"os"

	"github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/version"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/vpcnetwork"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log                  = &logger.Log
	ResultNormal         = common.ResultNormal
	ResultRequeue        = common.ResultRequeue
	MetricResType        = common.MetricResTypeServiceLb
	LBServiceClassForVPC = "vmware.com/nsx_vpc"
)

// ServiceLbReconciler reconciles a Service LoadBalancer object
type ServiceLbReconciler struct {
	Client          client.Client
	Scheme          *apimachineryruntime.Scheme
	Service         *servicecommon.Service
	Recorder        record.EventRecorder
	NetworkProvider vpcnetwork.VPCNetworkProvider
}

func updateSuccess(r *ServiceLbReconciler, c *context.Context, lbService *v1.Service) {
	r.setServiceLbStatus(c, lbService)
	r.Recorder.Event(lbService, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "LoadBalancer service has been successfully updated")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResType)
}

func (r *ServiceLbReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.NetworkProvider != nil {
		return r.NetworkProvider.ReconcileWithVPCFilters("lb Service", ctx, req, r.reconcile)
	}
	return r.reconcile(ctx, req)
}

func (r *ServiceLbReconciler) reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	service := &v1.Service{}

	if err := r.Client.Get(ctx, req.NamespacedName, service); err != nil {
		log.Error(err, "unable to fetch lb service", "req", req.NamespacedName)
		return ResultNormal, client.IgnoreNotFound(err)
	}

	if service.Spec.Type != v1.ServiceTypeLoadBalancer {
		return ResultNormal, nil
	}

	systemNS, err := util.IsVPCSystemNamespace(r.Client, req.Namespace, nil)
	if err != nil {
		log.Error(err, "unable to check Namespace with system annotation on lb service", "service", req.Namespace)
		return ResultNormal, client.IgnoreNotFound(err)
	}

	// Ignore the LB Service in system Namespaces if configured with a different LoadBalancerClass from VPC.
	if systemNS && !(service.Spec.LoadBalancerClass != nil && *service.Spec.LoadBalancerClass == LBServiceClassForVPC) {
		log.Info("LB Service is using a non-vpc class", "req", req, "lbClass", service.Spec.LoadBalancerClass)
		return ResultNormal, nil
	}

	log.Info("reconciling lb service", "lbService", req.NamespacedName)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, MetricResType)

	if service.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateTotal, MetricResType)
		updateSuccess(r, &ctx, service)
	}

	return ResultNormal, nil
}

func (r *ServiceLbReconciler) setServiceLbStatus(ctx *context.Context, lbService *v1.Service) {
	ipMode := v1.LoadBalancerIPModeProxy
	statusUpdated := false
	// If tanzu.vmware.com/ingress-ip-mode label with values proxy or vip,
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
		r.Client.Status().Update(*ctx, lbService)
		log.V(1).Info("updated LB service status ipMode", "Name", lbService.Name, "Namespace", lbService.Namespace, "ipMode", ipMode)
	}
}

func (r *ServiceLbReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Service{}).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Watches(
			&v1alpha1.Network{},
			&vpcnetwork.EnqueueRequestForNetwork{Client: r.Client, Lister: r.listLBServices},
			builder.WithPredicates(vpcnetwork.PredicateFuncsByNetwork),
		).
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

func (r *ServiceLbReconciler) listLBServices(ns string) ([]types.NamespacedName, error) {
	serviceList := &v1.ServiceList{}
	err := r.Client.List(context.Background(), serviceList, client.InNamespace(ns))
	if err != nil {
		return nil, err
	}
	nsNames := make([]types.NamespacedName, 0)
	for _, svc := range serviceList.Items {
		if svc.Spec.Type != v1.ServiceTypeLoadBalancer {
			continue
		}
		// Only process LoadBalancer type Service.
		nsNames = append(nsNames, types.NamespacedName{
			Namespace: svc.Namespace,
			Name:      svc.Name,
		})
	}
	return nsNames, nil
}

func isServiceLbStatusIpModeSupported(c *rest.Config) bool {
	version129, _ := version.ParseGeneric("v1.29.0")

	clientset, err := clientset.NewForConfig(c)
	if err != nil {
		log.Error(err, "failed to create clientset")
		return false
	}

	serverVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		log.Error(err, "failed to get server Kubernetes version")
		return false
	}

	runningVersion, err := version.ParseGeneric(serverVersion.String())
	if err != nil {
		log.Error(err, "unexpected error parsing server Kubernetes version", "K8sVersion", runningVersion.String())
		return false
	}

	log.Info("running server Kubernetes version is", "K8sVersion", runningVersion.String())
	return runningVersion.AtLeast(version129)
}

func StartServiceLbController(mgr ctrl.Manager, commonService servicecommon.Service, networkProvider vpcnetwork.VPCNetworkProvider) {
	if isServiceLbStatusIpModeSupported(mgr.GetConfig()) {

		serviceLbReconciler := ServiceLbReconciler{
			Client:          mgr.GetClient(),
			Scheme:          mgr.GetScheme(),
			Recorder:        mgr.GetEventRecorderFor("serviceLb-controller"),
			NetworkProvider: networkProvider,
		}
		serviceLbReconciler.Service = &commonService
		if err := serviceLbReconciler.Start(mgr); err != nil {
			log.Error(err, "failed to create controller", "controller", "ServiceLb")
			os.Exit(1)
		}
	} else {
		log.Info("service Lb controller isn't started since load balancer service ipMode supporting needs K8s version at least 1.29.0")
	}
}
