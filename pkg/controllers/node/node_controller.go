/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package node

import (
	"context"
	"os"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/node"
)

var (
	log               = logger.Log
	MetricResTypeNode = common.MetricResTypeNode
)

// NodeReconciler reconciles a Node object
type NodeReconciler struct {
	client.Client
	Scheme  *apimachineryruntime.Scheme
	Service *node.NodeService
}

func (r *NodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	node := &v1.Node{}
	deleted := false
	log.Info("reconciling node", "node", req.NamespacedName)

	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, MetricResTypeNode)

	if err := r.Client.Get(ctx, req.NamespacedName, node); err != nil {
		if errors.IsNotFound(err) {
			log.Info("node not found", "req", req.NamespacedName)
			deleted = true
		} else {
			log.Error(err, "unable to fetch node", "req", req.NamespacedName)
		}
		return common.ResultNormal, client.IgnoreNotFound(err)
	}
	if common.NodeIsMaster(node) {
		// For WCP supervisor cluster, the master node isn't a transport node.
		log.Info("skipping handling master node", "node", req.NamespacedName)
		return ctrl.Result{}, nil
	}
	if !node.ObjectMeta.DeletionTimestamp.IsZero() {
		log.Info("node is being deleted", "node", req.NamespacedName)
		deleted = true
	}

	if err := r.Service.SyncNodeStore(node.Name, deleted); err != nil {
		log.Error(err, "failed to sync node store", "req", req.NamespacedName)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Node{}).
		Complete(r)
}

func StartNodeController(mgr ctrl.Manager, nodeService *node.NodeService) {
	nodePortReconciler := NodeReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}

	nodePortReconciler.Service = nodeService

	if err := nodePortReconciler.Start(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "Node")
		os.Exit(1)
	}
}

func (r *NodeReconciler) Start(mgr ctrl.Manager) error {
	err := r.SetupWithManager(mgr)
	if err != nil {
		return err
	}
	return nil
}
