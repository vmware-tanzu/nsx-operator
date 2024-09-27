/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package node

import (
	"context"
	"os"
	"reflect"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/node"
)

var (
	log               = &logger.Log
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
		WithEventFilter(PredicateFuncsNode).
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

var PredicateFuncsNode = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldNode, okOld := e.ObjectOld.(*v1.Node)
		newNode, okNew := e.ObjectNew.(*v1.Node)
		if !okOld || !okNew {
			return true
		}

		// If only the condition, resource version, allocatable, capacity have changed, and other properties unchanged, ignore the update
		if len(newNode.Status.Conditions) > 0 && len(oldNode.Status.Conditions) > 0 {
			if newNode.ResourceVersion != oldNode.ResourceVersion {
				oldNode.Status.Allocatable = newNode.Status.Allocatable
				oldNode.Status.Capacity = newNode.Status.Capacity
				oldNode.Status.Conditions = newNode.Status.Conditions
				return !reflect.DeepEqual(oldNode.Status, newNode.Status)
			}
		}
		return true

	},
	CreateFunc: func(e event.CreateEvent) bool {
		return true
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return true
	},
	GenericFunc: func(e event.GenericEvent) bool {
		return true
	},
}
