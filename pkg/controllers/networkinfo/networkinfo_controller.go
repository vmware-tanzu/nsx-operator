/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkinfo

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	commonservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/ipblocksinfo"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

var (
	log           = &logger.Log
	MetricResType = common.MetricResTypeNetworkInfo
)

// NetworkInfoReconciler NetworkInfoReconcile reconciles a NetworkInfo object
// Actually it is more like a shell, which is used to manage nsx VPC
type NetworkInfoReconciler struct {
	Client              client.Client
	Scheme              *apimachineryruntime.Scheme
	Service             *vpc.VPCService
	IPBlocksInfoService *ipblocksinfo.IPBlocksInfoService
	Recorder            record.EventRecorder
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
				updateFail(r, ctx, obj, &err, r.Client, nil)
				return common.ResultRequeue, err
			}
			log.V(1).Info("added finalizer on NetworkInfo CR", "NetworkInfo", req.NamespacedName)
		}
		// TODO:
		// 1. check whether the logic to get VPC network config can be replaced by GetVPCNetworkConfigByNamespace
		// 2. sometimes the variable nc points to a VPCNetworkInfo, sometimes it's a VPCNetworkConfiguration, we need to distinguish between them.
		ncName, err := r.Service.GetNetworkconfigNameFromNS(obj.Namespace)
		if err != nil {
			log.Error(err, "failed to get network config name for VPC when creating NSX VPC", "VPC", obj.Name)
			updateFail(r, ctx, obj, &err, r.Client, nil)
			return common.ResultRequeueAfter10sec, err
		}
		nc, _exist := r.Service.GetVPCNetworkConfig(ncName)
		if !_exist {
			message := fmt.Sprintf("failed to read network config %s when creating NSX VPC", ncName)
			log.Info(message)
			updateFail(r, ctx, obj, &err, r.Client, nil)
			return common.ResultRequeueAfter10sec, errors.New(message)
		}
		log.Info("got network config from store", "NetworkConfig", ncName)
		vpcNetworkConfiguration := &v1alpha1.VPCNetworkConfiguration{}
		err = r.Client.Get(ctx, types.NamespacedName{Name: commonservice.SystemVPCNetworkConfigurationName}, vpcNetworkConfiguration)
		if err != nil {
			log.Error(err, "failed to get system VPCNetworkConfiguration")
			updateFail(r, ctx, obj, &err, r.Client, nil)
			return common.ResultRequeueAfter10sec, err
		}
		gatewayConnectionReady, _, err := getGatewayConnectionStatus(ctx, vpcNetworkConfiguration)
		if err != nil {
			log.Error(err, "failed to get the gateway connection status", "req", req.NamespacedName)
			return common.ResultRequeueAfter10sec, err
		}
		gatewayConnectionReason := ""
		if ncName == commonservice.SystemVPCNetworkConfigurationName {
			gatewayConnectionReady, gatewayConnectionReason, err = r.Service.ValidateGatewayConnectionStatus(&nc)
			log.Info("got the gateway connection status", "gatewayConnectionReady", gatewayConnectionReady, "gatewayConnectionReason", gatewayConnectionReason)
			if err != nil {
				log.Error(err, "failed to validate the edge and gateway connection", "org", nc.Org, "project", nc.NSXProject)
				updateFail(r, ctx, obj, &err, r.Client, nil)
				return common.ResultRequeueAfter10sec, err
			}
			setVPCNetworkConfigurationStatusWithGatewayConnection(ctx, r.Client, vpcNetworkConfiguration, gatewayConnectionReady, gatewayConnectionReason)
		}
		if !gatewayConnectionReady && ncName != commonservice.SystemVPCNetworkConfigurationName {
			log.Info("skipping reconciling the network info because the system gateway connection is not ready", "NetworkInfo", req.NamespacedName)
			return common.ResultRequeueAfter60sec, nil
		}
		lbProvider := r.Service.GetLBProvider()
		createdVpc, err := r.Service.CreateOrUpdateVPC(obj, &nc, lbProvider)
		if err != nil {
			log.Error(err, "create vpc failed, would retry exponentially", "VPC", req.NamespacedName)
			updateFail(r, ctx, obj, &err, r.Client, nil)
			return common.ResultRequeueAfter10sec, err
		}

		var privateIPs []string
		var vpcConnectivityProfilePath string
		var nsxLBSPath string
		isPreCreatedVPC := vpc.IsPreCreatedVPC(nc)
		if isPreCreatedVPC {
			privateIPs = createdVpc.PrivateIps
			vpcConnectivityProfilePath = *createdVpc.VpcConnectivityProfile
			// Retrieve NSX lbs path if Avi is not used with the pre-created VPC.
			if createdVpc.LoadBalancerVpcEndpoint == nil || createdVpc.LoadBalancerVpcEndpoint.Enabled == nil ||
				!*createdVpc.LoadBalancerVpcEndpoint.Enabled {
				nsxLBSPath, err = r.Service.GetLBSsFromNSXByVPC(*createdVpc.Path)
				if err != nil {
					log.Error(err, "failed to get NSX LBS path with pre-created VPC", "VPC", createdVpc.Path)
					updateFail(r, ctx, obj, &err, r.Client, nil)
					return common.ResultRequeueAfter10sec, err
				}
			}
		} else {
			privateIPs = nc.PrivateIPs
			vpcConnectivityProfilePath = nc.VPCConnectivityProfile
		}

		snatIP, path, cidr := "", "", ""

		vpcConnectivityProfile, err := r.Service.GetVpcConnectivityProfile(&nc, vpcConnectivityProfilePath)
		if err != nil {
			log.Error(err, "get VpcConnectivityProfile failed, would retry exponentially", "VPC", req.NamespacedName)
			updateFail(r, ctx, obj, &err, r.Client, nil)
			return common.ResultRequeueAfter10sec, err
		}
		hasExternalIPs := true
		if ncName == commonservice.SystemVPCNetworkConfigurationName {
			if len(vpcConnectivityProfile.ExternalIpBlocks) == 0 {
				hasExternalIPs = false
				log.Error(err, "there is no ExternalIPBlock in VPC ConnectivityProfile", "VPC", req.NamespacedName)
			}
			setVPCNetworkConfigurationStatusWithNoExternalIPBlock(ctx, r.Client, vpcNetworkConfiguration, hasExternalIPs)
		}
		// currently, auto snat is not exposed, and use default value True
		// checking autosnat to support future extension in vpc configuration
		autoSnatEnabled := r.Service.IsEnableAutoSNAT(vpcConnectivityProfile)
		if autoSnatEnabled {
			snatIP, err = r.Service.GetDefaultSNATIP(*createdVpc)
			if err != nil {
				log.Error(err, "failed to read default SNAT ip from VPC", "VPC", createdVpc.Id)
				state := &v1alpha1.VPCState{
					Name:                    *createdVpc.DisplayName,
					DefaultSNATIP:           "",
					LoadBalancerIPAddresses: "",
					PrivateIPs:              privateIPs,
				}
				updateFail(r, ctx, obj, &err, r.Client, state)
				return common.ResultRequeueAfter10sec, err
			}
		}
		if ncName == commonservice.SystemVPCNetworkConfigurationName {
			vpcNetworkConfiguration := &v1alpha1.VPCNetworkConfiguration{}
			err := r.Client.Get(ctx, types.NamespacedName{Name: ncName}, vpcNetworkConfiguration)
			if err != nil {
				log.Error(err, "failed to get VPCNetworkConfiguration", "Name", ncName)
				updateFail(r, ctx, obj, &err, r.Client, nil)
				return common.ResultRequeueAfter10sec, err
			}
			log.Info("got the AutoSnat status", "autoSnatEnabled", autoSnatEnabled, "req", req.NamespacedName)
			setVPCNetworkConfigurationStatusWithSnatEnabled(ctx, r.Client, vpcNetworkConfiguration, autoSnatEnabled)
		}

		// if lb vpc enabled, read avi subnet path and cidr
		// nsx bug, if set LoadBalancerVpcEndpoint.Enabled to false, when read this vpc back,
		// LoadBalancerVpcEndpoint.Enabled will become a nil pointer.
		if lbProvider == vpc.AVILB && createdVpc.LoadBalancerVpcEndpoint != nil && createdVpc.LoadBalancerVpcEndpoint.Enabled != nil && *createdVpc.LoadBalancerVpcEndpoint.Enabled {
			path, cidr, err = r.Service.GetAVISubnetInfo(*createdVpc)
			if err != nil {
				log.Error(err, "failed to read lb subnet path and cidr", "VPC", createdVpc.Id)
				state := &v1alpha1.VPCState{
					Name:                    *createdVpc.DisplayName,
					DefaultSNATIP:           snatIP,
					LoadBalancerIPAddresses: "",
					PrivateIPs:              privateIPs,
				}
				updateFail(r, ctx, obj, &err, r.Client, state)
				return common.ResultRequeueAfter10sec, err
			}
		}

		state := &v1alpha1.VPCState{
			Name:                    *createdVpc.DisplayName,
			DefaultSNATIP:           snatIP,
			LoadBalancerIPAddresses: cidr,
			PrivateIPs:              privateIPs,
			VPCPath:                 *createdVpc.Path,
		}

		if !isPreCreatedVPC {
			nsxLBSPath = r.Service.GetDefaultNSXLBSPathByVPC(*createdVpc.Id)
		}
		// AKO needs to know the AVI subnet path created by NSX
		setVPCNetworkConfigurationStatusWithLBS(ctx, r.Client, ncName, state.Name, path, nsxLBSPath, *createdVpc.Path)
		updateSuccess(r, ctx, obj, r.Client, state, nc.Name, path)
		if ncName == commonservice.SystemVPCNetworkConfigurationName && (!gatewayConnectionReady || !autoSnatEnabled || !hasExternalIPs) {
			log.Info("requeuing the NetworkInfo CR because VPCNetworkConfiguration system is not ready", "gatewayConnectionReason", gatewayConnectionReason, "autoSnatEnabled", autoSnatEnabled, "hasExternalIPs", hasExternalIPs, "req", req)
			return common.ResultRequeueAfter60sec, nil
		}
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
				for _, vpc := range vpcs {
					// first delete vpc and then ipblock or else it will fail arguing it is being referenced by other objects
					if err := r.Service.DeleteVPC(*vpc.Path); err != nil {
						log.Error(err, "failed to delete nsx VPC, would retry exponentially", "NetworkInfo", req.NamespacedName)
						deleteFail(r, ctx, obj, &err, r.Client)
						return common.ResultRequeueAfter10sec, err
					}
					if err := r.Service.DeleteIPBlockInVPC(*vpc); err != nil {
						log.Error(err, "failed to delete private ip blocks for VPC", "VPC", req.NamespacedName)
						return common.ResultRequeueAfter10sec, err
					}
				}
			}

			controllerutil.RemoveFinalizer(obj, commonservice.NetworkInfoFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				deleteFail(r, ctx, obj, &err, r.Client)
				return common.ResultRequeue, err
			}
			ncName, err := r.Service.GetNetworkconfigNameFromNS(obj.Namespace)
			if err != nil {
				log.Error(err, "failed to get network config name for VPC when deleting NetworkInfo CR", "NetworkInfo", obj.Name)
				return common.ResultRequeueAfter10sec, err
			}
			log.V(1).Info("removed finalizer", "NetworkInfo", req.NamespacedName)
			deleteVPCNetworkConfigurationStatus(ctx, r.Client, ncName, vpcs, r.Service.ListVPC())
			deleteSuccess(r, ctx, obj)
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
			// For created/removed network config, add/remove from vpc network config cache,
			// and update IPBlocksInfo.
			// For modified network config, currently only support appending ips to public ip blocks,
			// update network config in cache and update nsx vpc object.
			&v1alpha1.VPCNetworkConfiguration{},
			&VPCNetworkConfigurationHandler{
				Client:              mgr.GetClient(),
				vpcService:          r.Service,
				ipBlocksInfoService: r.IPBlocksInfoService,
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
	return nil
}

// CollectGarbage logic for nsx-vpc is that:
// 1. list all current existing namespace in kubernetes
// 2. list all the nsx-vpc in vpcStore
// 3. loop all the nsx-vpc to get its namespace, check if the namespace still exist
// 4. if ns do not exist anymore, delete the nsx-vpc resource
// it implements the interface GarbageCollector method.
func (r *NetworkInfoReconciler) CollectGarbage(ctx context.Context) {
	log.Info("VPC garbage collector started")
	// read all nsx-vpc from vpc store
	nsxVPCList := r.Service.ListVPC()
	if len(nsxVPCList) == 0 {
		return
	}

	// read all namespaces from k8s
	namespaces := &corev1.NamespaceList{}
	err := r.Client.List(ctx, namespaces)
	if err != nil {
		log.Error(err, "failed to list k8s namespaces")
		return
	}
	nsSet := sets.NewString()
	for _, ns := range namespaces.Items {
		nsSet.Insert(ns.Name)
	}

	for i := len(nsxVPCList) - 1; i >= 0; i-- {
		nsxVPCNamespace := getNamespaceFromNSXVPC(&nsxVPCList[i])
		if nsSet.Has(nsxVPCNamespace) {
			continue
		}
		elem := nsxVPCList[i]
		log.Info("GC collected nsx VPC object", "ID", elem.Id, "Namespace", nsxVPCNamespace)
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
