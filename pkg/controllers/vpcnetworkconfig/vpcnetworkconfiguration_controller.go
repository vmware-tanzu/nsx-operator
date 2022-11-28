/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package vpcnetworkconfig

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	v1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
)

// VPCNetworkConfigurationReconciler reconciles a VPCNetworkConfiguration object.
type VPCNetworkConfigurationReconciler struct {
	client.Client
	Scheme                     *runtime.Scheme
	installedVPCNetworkConfigs *VPCNetworkConfigInfoCache
}

func NewVPCNetworkConfigurationReconciler(client client.Client, scheme *runtime.Scheme) *VPCNetworkConfigurationReconciler {
	return &VPCNetworkConfigurationReconciler{
		Client:                     client,
		Scheme:                     scheme,
		installedVPCNetworkConfigs: NewVPCNetworkConfigInfoStore(),
	}
}

// Reconcile will attempt to ensure that the imported Resource is installed in local cluster as per the
// VPCNetworkConfiguration object.
func (r *VPCNetworkConfigurationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	klog.V(2).InfoS("Reconciling VPCNetworkConfiguration", "vpcNetworkConfiguration", req.Name)

	var vpcNetworkConfig v1alpha1.VPCNetworkConfiguration
	err := r.Client.Get(ctx, req.NamespacedName, &vpcNetworkConfig)
	var isDeleted bool
	if err != nil {
		isDeleted = apierrors.IsNotFound(err)
		if !isDeleted {
			klog.InfoS("Unable to fetch VPCNetworkConfiguration", "vpcNetworkConfiguration", req.NamespacedName.String(), "err", err)
			return ctrl.Result{}, err
		} else {
			vpcNetworkConfigurationObj, exist := r.installedVPCNetworkConfigs.GetByKey(req.NamespacedName.String())
			if exist {
				r.installedVPCNetworkConfigs.DeleteVPCNetworkConfigInfo(vpcNetworkConfigurationObj)
				return ctrl.Result{}, nil
			}
		}
	}
	ninfo := &VPCNetworkConfigInfo{
		name:                    vpcNetworkConfig.Name,
		namespace:               vpcNetworkConfig.Spec.AppliedToNamespaces[0],
		defaultGatewayPath:      vpcNetworkConfig.Spec.DefaultGatewayPath,
		edgeClusterPath:         vpcNetworkConfig.Spec.EdgeClusterPath,
		nsxtProject:             vpcNetworkConfig.Spec.NSXTProject,
		publicIPv4Blocks:        vpcNetworkConfig.Spec.PublicIPv4Blocks,
		privateIPv4CIDRs:        vpcNetworkConfig.Spec.PrivateIPv4CIDRs,
		defaultIPv4SubnetSize:   vpcNetworkConfig.Spec.DefaultIPv4SubnetSize,
		defaultSubnetAccessMode: vpcNetworkConfig.Spec.DefaultSubnetAccessMode,
	}
	r.installedVPCNetworkConfigs.AddVPCNetworkConfigInfo(ninfo)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the ClusterManager
// which will set up controllers for resources that need to be monitored
// in the remoteCommonArea.
func (r *VPCNetworkConfigurationReconciler) setupWithManager(mgr ctrl.Manager) error {
	// Ignore status update event via GenerationChangedPredicate
	instance := predicate.GenerationChangedPredicate{}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.VPCNetworkConfiguration{}).
		WithEventFilter(instance).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 4,
		}).
		Complete(r)
}

// Start setup manager
func (r *VPCNetworkConfigurationReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}
	return nil
}
