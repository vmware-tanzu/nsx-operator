/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package vpc

import (
	"context"
	"os"
	"runtime"
	"time"

	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	commonservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

var (
	log                     = logger.Log
	ResultNormal            = common.ResultNormal
	ResultRequeue           = common.ResultRequeue
	ResultRequeueAfter5mins = common.ResultRequeueAfter5mins
	MetricResType           = common.MetricResTypeVPC
)

// VPCReconciler VPCReconcile reconciles a VPC object
type VPCReconciler struct {
	Client  client.Client
	Scheme  *apimachineryruntime.Scheme
	Service *vpc.VPCService
}

func (r *VPCReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.VPC{}
	log.Info("reconciling VPC CR", "VPC", req.NamespacedName)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, common.MetricResTypeVPC)

	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		log.Error(err, "unable to fetch VPC CR", "req", req.NamespacedName)
		return common.ResultNormal, client.IgnoreNotFound(err)
	}

	if obj.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateTotal, common.MetricResTypeVPC)
		if !controllerutil.ContainsFinalizer(obj, commonservice.VPCFinalizerName) {
			controllerutil.AddFinalizer(obj, commonservice.VPCFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "add finalizer", "VPC", req.NamespacedName)
				updateFail(r.Service.NSXConfig, &ctx, obj, &err, r.Client)
				return common.ResultRequeue, err
			}
			log.V(1).Info("added finalizer on VPC CR", "VPC", req.NamespacedName)
		}

		createdVpc, nc, err := r.Service.CreateorUpdateVPC(obj)
		if err != nil {
			log.Error(err, "operate failed, would retry exponentially", "VPC", req.NamespacedName)
			updateFail(r.Service.NSXConfig, &ctx, obj, &err, r.Client)
			return common.ResultRequeueAfter10sec, err
		}
		err = r.Service.CreateOrUpdateAVIRule(createdVpc, obj.Namespace)
		if err != nil {
			log.Error(err, "operate failed, would retry exponentially", "VPC", req.NamespacedName)
			updateFail(r.Service.NSXConfig, &ctx, obj, &err, r.Client)
			return common.ResultRequeueAfter10sec, err
		}

		snatIP, path, cidr := "", "", ""
		// currently, auto snat is not exposed, and use default value True
		// checking autosnat to support future extension in vpc configuration
		if *createdVpc.ServiceGateway.AutoSnat {
			snatIP, err = r.Service.GetDefaultSNATIP(*createdVpc)
			if err != nil {
				log.Error(err, "failed to read default SNAT ip from VPC", "VPC", createdVpc.Id)
				return common.ResultRequeueAfter10sec, err
			}
		}

		// if lb vpc enabled, read avi subnet path and cidr
		// nsx bug, if set LoadBalancerVpcEndpoint.Enabled to false, when read this vpc back,
		// LoadBalancerVpcEndpoint.Enabled will become a nil pointer.
		if createdVpc.LoadBalancerVpcEndpoint.Enabled != nil && *createdVpc.LoadBalancerVpcEndpoint.Enabled {
			path, cidr, err = r.Service.GetAVISubnetInfo(*createdVpc)
			if err != nil {
				log.Error(err, "failed to read lb subnet path and cidr", "VPC", createdVpc.Id)
				return common.ResultRequeueAfter10sec, err
			}
		}

		updateSuccess(r.Service.NSXConfig, &ctx, obj, r.Client, *createdVpc.Path, snatIP, path, cidr, nc.PrivateIPv4CIDRs)
	} else {
		if controllerutil.ContainsFinalizer(obj, commonservice.VPCFinalizerName) {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, common.MetricResTypeVPC)
			vpcs := r.Service.GetVPCsByNamespace(obj.GetNamespace())
			// if nsx resource do not exist, continue to remove finalizer, or the crd can not be removed
			if len(vpcs) == 0 {
				// when nsx vpc not found in vpc store, skip deleting NSX VPC
				log.Info("can not find VPC in store, skip deleting NSX VPC, remove finalizer from VPC CR")
			} else {
				vpc := vpcs[0]
				if err := r.Service.DeleteVPC(*vpc.Path); err != nil {
					log.Error(err, "failed to delete VPC CR, would retry exponentially", "VPC", req.NamespacedName)
					deleteFail(r.Service.NSXConfig, &ctx, obj, &err, r.Client)
					return common.ResultRequeueAfter10sec, err
				}

				if err := r.Service.DeleteIPBlockInVPC(vpc); err != nil {
					log.Error(err, "failed to delete private ip blocks for VPC", "VPC", req.NamespacedName)
				}
			}

			controllerutil.RemoveFinalizer(obj, commonservice.VPCFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				deleteFail(r.Service.NSXConfig, &ctx, obj, &err, r.Client)
				return common.ResultRequeue, err
			}
			log.V(1).Info("removed finalizer", "VPC", req.NamespacedName)
			deleteSuccess(r.Service.NSXConfig, &ctx, obj)
		} else {
			// only print a message because it's not a normal case
			log.Info("finalizers cannot be recognized", "VPC", req.NamespacedName)
		}
	}
	return common.ResultNormal, nil
}

func (r *VPCReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.VPC{}).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: runtime.NumCPU(),
			}).
		Watches(
			// For created/removed network config, add/remove from vpc network config cache.
			// For modified network config, currently only support appending ips to public ip blocks,
			// update network config in cache and update nsx vpc object.
			&source.Kind{Type: &v1alpha1.VPCNetworkConfiguration{}},
			&VPCNetworkConfigurationHandler{
				Client:     mgr.GetClient(),
				vpcService: r.Service,
			},
			builder.WithPredicates(VPCNetworkConfigurationPredicate)).
		Complete(r)
}

// Start setup manager and launch GC
func (r *VPCReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}

	go r.GarbageCollector(make(chan bool), commonservice.GCInterval)
	return nil
}

// GarbageCollector collect vpc which has been removed from crd.
// cancel is used to break the loop during UT
func (r *VPCReconciler) GarbageCollector(cancel chan bool, timeout time.Duration) {
	ctx := context.Background()
	log.Info("VPC garbage collector started")
	for {
		select {
		case <-cancel:
			return
		case <-time.After(timeout):
		}
		nsxVPCList := r.Service.ListVPC()
		if len(nsxVPCList) == 0 {
			continue
		}

		crdVPCList := &v1alpha1.VPCList{}
		err := r.Client.List(ctx, crdVPCList)
		if err != nil {
			log.Error(err, "failed to list VPC CR")
			continue
		}

		crdVPCSet := sets.NewString()
		for _, vc := range crdVPCList.Items {
			crdVPCSet.Insert(string(vc.UID))
		}

		for _, elem := range nsxVPCList {
			if crdVPCSet.Has(*elem.Id) {
				continue
			}

			log.V(1).Info("GC collected nsx VPC object", "ID", elem.Id)
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, common.MetricResTypeVPC)
			err = r.Service.DeleteVPC(*elem.Path)
			if err != nil {
				metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, common.MetricResTypeVPC)
			} else {
				metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, common.MetricResTypeVPC)
				if err := r.Service.DeleteIPBlockInVPC(elem); err != nil {
					log.Error(err, "failed to delete private ip blocks for VPC", "VPC", *elem.DisplayName)
				}
				log.Info("deleted private ip blocks for VPC", "VPC", *elem.DisplayName)
			}
		}
	}
}

func StartVPCController(mgr ctrl.Manager, commonService commonservice.Service) {
	vpcReconcile := VPCReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	if vpcService, err := vpc.InitializeVPC(commonService); err != nil {
		log.Error(err, "failed to initialize VPC commonService")
		os.Exit(1)
	} else {
		vpcReconcile.Service = vpcService
	}
	if err := vpcReconcile.Start(mgr); err != nil {
		log.Error(err, "failed to create VPC controller")
		os.Exit(1)
	}
}
