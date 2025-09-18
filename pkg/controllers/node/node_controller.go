/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

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
		if apierrors.IsNotFound(err) {
			log.Info("node not found", "req", req.NamespacedName)
			deleted = true
			if err := r.Service.SyncNodeStore(req.NamespacedName.Name, deleted); err != nil {
				log.Error(err, "failed to sync node store", "req", req.NamespacedName)
				return common.ResultNormal, err
			}
		} else {
			log.Error(err, "unable to fetch node", "req", req.NamespacedName)
		}
		return common.ResultNormal, client.IgnoreNotFound(err)
	}
	if common.NodeIsMaster(node) {
		// For WCP supervisor cluster, the master node isn't a transport node.
		log.Info("skipping handling master node", "node", req.NamespacedName)
		return common.ResultNormal, nil
	}
	if !node.ObjectMeta.DeletionTimestamp.IsZero() {
		log.Info("node is being deleted", "node", req.NamespacedName)
		deleted = true
	}

	if err := r.Service.SyncNodeStore(node.Name, deleted); err != nil {
		log.Error(err, "failed to sync node store", "req", req.NamespacedName)
		return common.ResultNormal, err
	}
	return common.ResultNormal, nil
}

// setupWithManager sets up the controller with the Manager.
func (r *NodeReconciler) setupWithManager(mgr ctrl.Manager) error {
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

func (r *NodeReconciler) RestoreReconcile() error {
	// Reconcile all nodes to make sure Pod restore can get the Node id
	ctx := context.Background()
	nodeList := &v1.NodeList{}
	err := r.Client.List(ctx, nodeList)
	if err != nil {
		return fmt.Errorf("failed to reconcile Nodes: %w", err)
	}
	var errorList []error
	for _, node := range nodeList.Items {
		result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: node.Namespace, Name: node.Name}})
		if result.Requeue || err != nil {
			errorList = append(errorList, fmt.Errorf("failed to reconcile Node %v, error: %w", node, err))
		}
	}
	if len(errorList) > 0 {
		return errors.Join(errorList...)
	}
	return nil
}

func (r *NodeReconciler) CollectGarbage(_ context.Context) error {
	return nil
}

func (r *NodeReconciler) StartController(mgr ctrl.Manager, _ webhook.Server) error {
	if err := r.Start(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "Node")
		return err
	}
	return nil
}

func NewNodeReconciler(mgr ctrl.Manager, nodeService *node.NodeService) *NodeReconciler {
	nodePortReconciler := &NodeReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}

	nodePortReconciler.Service = nodeService
	return nodePortReconciler
}

func (r *NodeReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
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
