/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkinfo

import (
	"context"
	"time"

	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	commonservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

var (
	log           = logger.Log
	MetricResType = common.MetricResTypeNetworkInfo
)

// NetworkInfoReconciler NetworkInfoReconcile reconciles a NetworkInfo object
// Actually it is more like a shell, which is used to manage nsx VPC
type NetworkInfoReconciler struct {
	Client   client.Client
	Scheme   *apimachineryruntime.Scheme
	Service  *vpc.VPCService
	Recorder record.EventRecorder
}

func (r *NetworkInfoReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.NetworkInfo{}
	log.Info("reconciling NetworkInfo CR", "NetworkInfo", req.NamespacedName)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, common.MetricResTypeNetworkInfo)

	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		log.Error(err, "unable to fetch NetworkInfo CR", "req", req.NamespacedName)
		return common.ResultNormal, client.IgnoreNotFound(err)
	}

	if obj.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateTotal, common.MetricResTypeNetworkInfo)
		if !controllerutil.ContainsFinalizer(obj, commonservice.NetworkInfoFinalizerName) {
			controllerutil.AddFinalizer(obj, commonservice.NetworkInfoFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "add finalizer", "NetworkInfo", req.NamespacedName)
				updateFail(r, &ctx, obj, &err, r.Client)
				return common.ResultRequeue, err
			}
			log.V(1).Info("added finalizer on NetworkInfo CR", "NetworkInfo", req.NamespacedName)
		}

		createdVpc, nc, err := r.Service.CreateOrUpdateVPC(obj)
		if err != nil {
			log.Error(err, "operate failed, would retry exponentially", "VPC", req.NamespacedName)
			updateFail(r, &ctx, obj, &err, r.Client)
			return common.ResultRequeueAfter10sec, err
		}

		isShared, err := r.Service.IsSharedVPCNamespaceByNS(obj.GetNamespace())
		if err != nil {
			log.Error(err, "failed to check if namespace is shared", "Namespace", obj.GetNamespace())
			return common.ResultRequeue, err
		}
		if !isShared {
			err = r.Service.CreateOrUpdateAVIRule(createdVpc, obj.Namespace)
			if err != nil {
				log.Error(err, "operate failed, would retry exponentially", "NetworkInfo", req.NamespacedName)
				updateFail(r, &ctx, obj, &err, r.Client)
				return common.ResultRequeueAfter10sec, err
			}
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

		updateSuccess(r, &ctx, obj, r.Client, *createdVpc.DisplayName, *createdVpc.Path, snatIP, path, cidr, nc.PrivateIPv4CIDRs, nc.Name)
	} else {
		if controllerutil.ContainsFinalizer(obj, commonservice.NetworkInfoFinalizerName) {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, common.MetricResTypeNetworkInfo)
			isShared, err := r.Service.IsSharedVPCNamespaceByNS(obj.GetNamespace())
			if err != nil {
				log.Error(err, "failed to check if namespace is shared", "Namespace", obj.GetNamespace())
				return common.ResultRequeue, err
			}
			vpcs := r.Service.GetVPCsByNamespace(obj.GetNamespace())
			// if nsx resource do not exist, continue to remove finalizer, or the crd can not be removed
			if len(vpcs) == 0 {
				// when nsx vpc not found in vpc store, skip deleting NSX VPC
				log.Info("can not find VPC in store, skip deleting NSX VPC, remove finalizer from NetworkInfo CR")
			} else if !isShared {
				vpc := vpcs[0]
				// first delete vpc and then ipblock or else it will fail arguing it is being referenced by other objects
				if err := r.Service.DeleteVPC(*vpc.Path); err != nil {
					log.Error(err, "failed to delete nsx VPC, would retry exponentially", "NetworkInfo", req.NamespacedName)
					deleteFail(r, &ctx, obj, &err, r.Client)
					return common.ResultRequeueAfter10sec, err
				}
				if err := r.Service.DeleteIPBlockInVPC(*vpc); err != nil {
					log.Error(err, "failed to delete private ip blocks for VPC", "VPC", req.NamespacedName)
					return common.ResultRequeueAfter10sec, err
				}
			}

			controllerutil.RemoveFinalizer(obj, commonservice.NetworkInfoFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				deleteFail(r, &ctx, obj, &err, r.Client)
				return common.ResultRequeue, err
			}
			log.V(1).Info("removed finalizer", "NetworkInfo", req.NamespacedName)
			deleteSuccess(r, &ctx, obj)
		} else {
			// only print a message because it's not a normal case
			log.Info("finalizers cannot be recognized", "NetworkInfo", req.NamespacedName)
		}
	}
	return common.ResultNormal, nil
}

func (r *NetworkInfoReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.NetworkInfo{}).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Watches(
			// For created/removed network config, add/remove from vpc network config cache.
			// For modified network config, currently only support appending ips to public ip blocks,
			// update network config in cache and update nsx vpc object.
			&v1alpha1.VPCNetworkConfiguration{},
			&VPCNetworkConfigurationHandler{
				Client:     mgr.GetClient(),
				vpcService: r.Service,
			},
			builder.WithPredicates(VPCNetworkConfigurationPredicate)).
		Complete(r)
}

// Start setup manager and launch GC
func (r *NetworkInfoReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}

	go r.GarbageCollector(make(chan bool), commonservice.GCInterval)
	return nil
}

// GarbageCollector collect vpc which has been removed from networkInfo crd.
// cancel is used to break the loop during UT
func (r *NetworkInfoReconciler) GarbageCollector(cancel chan bool, timeout time.Duration) {
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

		networkInfoList := &v1alpha1.NetworkInfoList{}
		err := r.Client.List(ctx, networkInfoList)
		if err != nil {
			log.Error(err, "failed to list NetworkInfo CR")
			continue
		}

		crdNetworkInfoVPCStateSet := sets.NewString()
		for _, networkInfo := range networkInfoList.Items {
			for _, vpcState := range networkInfo.VPCs {
				crdNetworkInfoVPCStateSet.Insert(vpcState.VPCPath)
			}
		}

		for _, elem := range nsxVPCList {
			if crdNetworkInfoVPCStateSet.Has(*elem.Path) {
				continue
			}

			log.V(1).Info("GC collected nsx VPC object", "ID", elem.Id)
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, common.MetricResTypeNetworkInfo)
			err = r.Service.DeleteVPC(*elem.Path)
			if err != nil {
				metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, common.MetricResTypeNetworkInfo)
			} else {
				metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, common.MetricResTypeNetworkInfo)
				if err := r.Service.DeleteIPBlockInVPC(elem); err != nil {
					log.Error(err, "failed to delete private ip blocks for VPC", "VPC", *elem.DisplayName)
				}
				log.Info("deleted private ip blocks for VPC", "VPC", *elem.DisplayName)
			}
		}
	}
}
