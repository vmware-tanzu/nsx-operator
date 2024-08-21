/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkinfo

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

var (
	log           = &logger.Log
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
				updateFail(r, &ctx, obj, &err, r.Client, nil)
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
			updateFail(r, &ctx, obj, &err, r.Client, nil)
			return common.ResultRequeueAfter10sec, err
		}
		nc, _exist := r.Service.GetVPCNetworkConfig(ncName)
		if !_exist {
			message := fmt.Sprintf("failed to read network config %s when creating NSX VPC", ncName)
			log.Info(message)
			updateFail(r, &ctx, obj, &err, r.Client, nil)
			return common.ResultRequeueAfter10sec, errors.New(message)
		}
		log.Info("got network config from store", "NetworkConfig", ncName)
		vpcNetworkConfiguration := &v1alpha1.VPCNetworkConfiguration{}
		err = r.Client.Get(ctx, types.NamespacedName{Name: commonservice.SystemVPCNetworkConfigurationName}, vpcNetworkConfiguration)
		if err != nil {
			log.Error(err, "failed to get system VPCNetworkConfiguration")
			updateFail(r, &ctx, obj, &err, r.Client, nil)
			return common.ResultRequeueAfter10sec, err
		}
		gatewayConnectionReady, _, err := getGatewayConnectionStatus(&ctx, vpcNetworkConfiguration)
		if err != nil {
			log.Error(err, "failed to get the gateway connection status", "req", req.NamespacedName)
			return common.ResultRequeueAfter10sec, err
		}

		reason := ""
		if !gatewayConnectionReady {
			if ncName == commonservice.SystemVPCNetworkConfigurationName {
				gatewayConnectionReady, reason, err = r.Service.ValidateGatewayConnectionStatus(&nc)
				log.Info("got the gateway connection status", "gatewayConnectionReady", gatewayConnectionReady, "reason", reason)
				if err != nil {
					log.Error(err, "failed to validate the edge and gateway connection", "org", nc.Org, "project", nc.NSXProject)
					updateFail(r, &ctx, obj, &err, r.Client, nil)
					return common.ResultRequeueAfter10sec, err
				}
				vpcNetworkConfiguration := &v1alpha1.VPCNetworkConfiguration{}
				err := r.Client.Get(ctx, types.NamespacedName{Name: ncName}, vpcNetworkConfiguration)
				if err != nil {
					log.Error(err, "failed to get VPCNetworkConfiguration", "Name", ncName)
					updateFail(r, &ctx, obj, &err, r.Client, nil)
					return common.ResultRequeueAfter10sec, err
				}
				setVPCNetworkConfigurationStatusWithGatewayConnection(&ctx, r.Client, vpcNetworkConfiguration, gatewayConnectionReady, reason)
			} else {
				log.Info("skipping reconciling the network info because the system gateway connection is not ready", "NetworkInfo", req.NamespacedName)
				return common.ResultRequeueAfter60sec, nil
			}
		}

		createdVpc, err := r.Service.CreateOrUpdateVPC(obj, &nc)
		if err != nil {
			log.Error(err, "create vpc failed, would retry exponentially", "VPC", req.NamespacedName)
			updateFail(r, &ctx, obj, &err, r.Client, nil)
			return common.ResultRequeueAfter10sec, err
		}

		var privateIPs []string
		var vpcConnectivityProfilePath string
		if vpc.IsPreCreatedVPC(nc) {
			privateIPs = createdVpc.PrivateIps
			vpcConnectivityProfilePath = *createdVpc.VpcConnectivityProfile
		} else {
			privateIPs = nc.PrivateIPs
			vpcConnectivityProfilePath = nc.VPCConnectivityProfile
		}

		snatIP, path, cidr := "", "", ""
		parts := strings.Split(vpcConnectivityProfilePath, "/")
		if len(parts) < 1 {
			log.Error(err, "failed to check VPCConnectivityProfile length", "VPCConnectivityProfile", nc.VPCConnectivityProfile)
			return common.ResultRequeue, err
		}
		vpcConnectivityProfileName := parts[len(parts)-1]
		vpcConnectivityProfile, err := r.Service.NSXClient.VPCConnectivityProfilesClient.Get(nc.Org, nc.NSXProject, vpcConnectivityProfileName)
		if err != nil {
			log.Error(err, "failed to get NSX VPC ConnectivityProfile object", "vpcConnectivityProfileName", vpcConnectivityProfileName)
			return common.ResultRequeue, err
		}
		isEnableAutoSNAT := func() bool {
			if vpcConnectivityProfile.ServiceGateway == nil || vpcConnectivityProfile.ServiceGateway.Enable == nil {
				return false
			}
			if *vpcConnectivityProfile.ServiceGateway.Enable {
				if vpcConnectivityProfile.ServiceGateway.NatConfig == nil || vpcConnectivityProfile.ServiceGateway.NatConfig.EnableDefaultSnat == nil {
					return false
				}
				return *vpcConnectivityProfile.ServiceGateway.NatConfig.EnableDefaultSnat
			}
			return false
		}
		// currently, auto snat is not exposed, and use default value True
		// checking autosnat to support future extension in vpc configuration
		autoSnatEnabled := isEnableAutoSNAT()
		if autoSnatEnabled {
			snatIP, err = r.Service.GetDefaultSNATIP(*createdVpc)
			if err != nil {
				log.Error(err, "failed to read default SNAT ip from VPC", "VPC", createdVpc.Id)
				state := &v1alpha1.VPCState{
					Name:                    *createdVpc.DisplayName,
					VPCPath:                 *createdVpc.Path,
					DefaultSNATIP:           "",
					LoadBalancerIPAddresses: "",
					PrivateIPs:              privateIPs,
				}
				updateFail(r, &ctx, obj, &err, r.Client, state)
				return common.ResultRequeueAfter10sec, err
			}
		}
		if ncName == commonservice.SystemVPCNetworkConfigurationName {
			vpcNetworkConfiguration := &v1alpha1.VPCNetworkConfiguration{}
			err := r.Client.Get(ctx, types.NamespacedName{Name: ncName}, vpcNetworkConfiguration)
			if err != nil {
				log.Error(err, "failed to get VPCNetworkConfiguration", "Name", ncName)
				updateFail(r, &ctx, obj, &err, r.Client, nil)
				return common.ResultRequeueAfter10sec, err
			}
			if autoSnatEnabled {
				log.Info("detected that the AutoSnat is enabled", "req", req.NamespacedName)
				setVPCNetworkConfigurationStatusWithSnatEnabled(&ctx, r.Client,
					vpcNetworkConfiguration, true)
			} else {
				log.Info("detected that the AutoSnat is disabled", "req", req.NamespacedName)
				setVPCNetworkConfigurationStatusWithSnatEnabled(&ctx, r.Client, vpcNetworkConfiguration, false)
			}
		}

		// if lb vpc enabled, read avi subnet path and cidr
		// nsx bug, if set LoadBalancerVpcEndpoint.Enabled to false, when read this vpc back,
		// LoadBalancerVpcEndpoint.Enabled will become a nil pointer.
		if !r.Service.NSXLBEnabled() && createdVpc.LoadBalancerVpcEndpoint.Enabled != nil && *createdVpc.LoadBalancerVpcEndpoint.Enabled {
			path, cidr, err = r.Service.GetAVISubnetInfo(*createdVpc)
			if err != nil {
				log.Error(err, "failed to read lb subnet path and cidr", "VPC", createdVpc.Id)
				state := &v1alpha1.VPCState{
					Name:                    *createdVpc.DisplayName,
					VPCPath:                 *createdVpc.Path,
					DefaultSNATIP:           snatIP,
					LoadBalancerIPAddresses: "",
					PrivateIPs:              privateIPs,
				}
				updateFail(r, &ctx, obj, &err, r.Client, state)
				return common.ResultRequeueAfter10sec, err
			}
		}

		state := &v1alpha1.VPCState{
			Name:                    *createdVpc.DisplayName,
			VPCPath:                 *createdVpc.Path,
			DefaultSNATIP:           snatIP,
			LoadBalancerIPAddresses: cidr,
			PrivateIPs:              privateIPs,
		}
		// AKO needs to know the AVI subnet path created by NSX
		setVPCNetworkConfigurationStatusWithLBS(&ctx, r.Client, ncName, state.Name, path, r.Service.GetNSXLBSPath(*createdVpc.Id))
		updateSuccess(r, &ctx, obj, r.Client, state, nc.Name, path)
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
