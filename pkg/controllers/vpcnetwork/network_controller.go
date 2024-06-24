/* Copyright © 2024 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package vpcnetwork

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/go-logr/logr"
	"github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log           = logger.Log
	resultNormal  = common.ResultNormal
	resultRequeue = common.ResultRequeue

	systemNetworkNamespaces sets.Set[string]
)

const (
	defaultNetworkLabelKey   = "is-default-network"
	defaultNetworkLabelValue = "true"
	vpcNetworkValidationPath = "/validate-vpc-enablement"
)

// NetworkReconciler reconciles a WCP Network object
type NetworkReconciler struct {
	Client client.Client
	Scheme *apimachineryruntime.Scheme
}

func (r *NetworkReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Log the Network create/update event.
	log.Info("reconciling network CR", "network", req.NamespacedName)

	ns := req.Namespace
	if systemNetworkNamespaces.Has(ns) {
		log.Info("Default Network CR in system Namespaces is updated to VPC type", "req", req.NamespacedName)
		return resultNormal, nil
	}

	// Find the system Network Namespace when creating Network CRs
	nsObj := &v1.Namespace{}
	err := r.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: ns}, nsObj)
	// This shall not happen, adding the below logic for security purpose.
	if err != nil {
		log.Error(err, "failed to check Namespace CR", "namespace", ns)
		return resultRequeue, client.IgnoreNotFound(err)
	}
	isSystem, _ := util.IsVPCSystemNamespace(r.Client, ns, nsObj)
	if isSystem {
		systemNetworkNamespaces.Insert(ns)
		if systemNetworkNamespaces.Len() == 1 {
			log.Info("Default Network in system Namespaces is created", "req", req.NamespacedName)
		} else {
			// In theory, this shall not happen, adding log here for security.
			log.Error(nil, "Multiple default Networks are found in system NSs", "namespaces", systemNetworkNamespaces.Len())
		}
	}
	return resultNormal, nil
}

func (r *NetworkReconciler) IsVPCEnabledOnNamespace(ns string) (bool, error) {
	nsObj := &v1.Namespace{}
	err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: ns}, nsObj)
	if err != nil {
		log.Error(err, "failed to check Namespace CR", "namespace", ns)
		return false, err
	}
	isSystem, _ := util.IsVPCSystemNamespace(r.Client, ns, nsObj)
	if isSystem {
		if systemNetworkNamespaces.Len() == 0 {
			return false, fmt.Errorf("no shared VPC namespace found with system Namespace %s", ns)
		}
		ns = systemNetworkNamespaces.UnsortedList()[0]
	}
	netList := &v1alpha1.NetworkList{}
	matchingLabels := client.MatchingLabels{defaultNetworkLabelKey: defaultNetworkLabelValue}
	err = r.Client.List(context.Background(), netList, client.InNamespace(ns), matchingLabels)
	if err != nil {
		log.Error(err, "failed to list default Network in Namespace", "namespace", ns)
		return false, err
	}
	if len(netList.Items) == 0 {
		return false, fmt.Errorf("no default network found in Namespace %s", ns)
	}
	network := netList.Items[0]
	return network.Spec.Type == v1alpha1.NetworkTypeNSXTVPC, nil
}

func (r *NetworkReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Register validation webhook to check VPC is enabled or not when creating CRs in a Namespace.
	webhookServer := mgr.GetWebhookServer()
	webhookServer.Register(vpcNetworkValidationPath,
		&webhook.Admission{
			Handler: r,
		})
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Network{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(createEvent event.CreateEvent) bool {
				// Filter the resources only labeled with "is-default-network = true"
				return isDefaultNetwork(createEvent.Object)
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				return false
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				// Ignore the deletion event.
				return false
			},
		}).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Complete(r)
}

func newNetworkController(mgr ctrl.Manager) *NetworkReconciler {
	systemNetworkNamespaces = sets.New[string]()
	return &NetworkReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
}

func StartNetworkController(mgr ctrl.Manager) VPCNetworkProvider {
	networkReconcile := newNetworkController(mgr)
	err := networkReconcile.SetupWithManager(mgr)
	if err != nil {
		log.Error(err, "failed to create controller", "controller", "Network")
		os.Exit(1)
	}
	return networkReconcile
}

// isDefaultNetwork returns true only if the provided network is labeled with "is-default-network=true".
func isDefaultNetwork(network client.Object) bool {
	labels := network.GetLabels()
	networkLabelValue, found := labels[defaultNetworkLabelKey]
	if !found {
		return false
	}
	return strings.EqualFold(networkLabelValue, defaultNetworkLabelValue)
}

func predictNetworkUpdateEvent(e event.UpdateEvent, log logr.Logger) bool {
	oldObj := e.ObjectOld.(*v1alpha1.Network)
	newObj := e.ObjectNew.(*v1alpha1.Network)
	if !isDefaultNetwork(newObj) {
		return false
	}
	if oldObj.Spec.Type == newObj.Spec.Type {
		return false
	}
	if newObj.Spec.Type != v1alpha1.NetworkTypeNSXTVPC {
		log.Info("DefaultNetwork has updated its type to non-VPC, ignore", "namespace",
			newObj.Namespace, "name", newObj.Name, "oldType", oldObj.Spec.Type, "newType", newObj.Spec.Type)
		return false
	}
	if !systemNetworkNamespaces.Has(newObj.Namespace) {
		log.Info("Ignore the event to update network type as VPC in a non-system Namespaces", "namespace", newObj.Namespace)
		return false
	}
	log.Info("receive network update to VPC event", "name", oldObj.Name, "namespace", oldObj.Name)
	return true
}
