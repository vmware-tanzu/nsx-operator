/* Copyright © 2024 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package vpcnetwork

import (
	"context"
	"fmt"

	"github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

type EnqueueRequestForNetwork struct {
	Client client.Client
	Lister func(namespace string) ([]types.NamespacedName, error)
}

func (e *EnqueueRequestForNetwork) Create(_ context.Context, _ event.CreateEvent, _ workqueue.RateLimitingInterface) {
	log.V(1).Info("namespace create event, do nothing")
}

func (e *EnqueueRequestForNetwork) Delete(_ context.Context, _ event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	log.V(1).Info("namespace delete event, do nothing")
}

func (e *EnqueueRequestForNetwork) Generic(_ context.Context, _ event.GenericEvent, _ workqueue.RateLimitingInterface) {
	log.V(1).Info("namespace generic event, do nothing")
}

func (e *EnqueueRequestForNetwork) Update(ctx context.Context, updateEvent event.UpdateEvent, q workqueue.RateLimitingInterface) {
	network := updateEvent.ObjectNew.(*v1alpha1.Network)
	netNS := network.Namespace
	objs := make([]types.NamespacedName, 0)
	listCRsInNS := func(ns string) error {
		crs, err := e.Lister(ns)
		if err != nil {
			log.Error(err, "Failed to list CRs in the Namespace", "namespace", network.Namespace)
			return err
		}
		objs = append(objs, crs...)
		return nil
	}

	// List existing CRs in a non-system and add into the queue
	if !systemNetworkNamespaces.Has(netNS) {
		if err := listCRsInNS(network.Namespace); err != nil {
			return
		}
	} else {
		// List CRs in all system Namespaces and all into the queue.
		namespaceList := &v1.NamespaceList{}
		err := e.Client.List(ctx, namespaceList)
		if err != nil {
			log.Error(err, "failed to list Namespaces after system network is updated to VPC", "namespace", network.Namespace)
			return
		}
		for i := range namespaceList.Items {
			ns := namespaceList.Items[i]
			if isSystem, _ := util.IsVPCSystemNamespace(e.Client, ns.Name, &ns); isSystem {
				if err = listCRsInNS(ns.Name); err != nil {
					return
				}
			}
		}
	}

	for _, namespacedname := range objs {
		q.Add(reconcile.Request{NamespacedName: namespacedname})
	}
}

var PredicateFuncsByNetwork = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return false
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		return predictNetworkUpdateEvent(e, log.V(1))
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return false
	},
}

func (r *NetworkReconciler) ReconcileWithVPCFilters(resource string, ctx context.Context, req ctrl.Request, innerFunc ReconcileFunc) (ctrl.Result, error) {
	vpcEnabled, err := r.IsVPCEnabledOnNamespace(req.Namespace)
	if err != nil {
		log.Error(err, fmt.Sprintf("failed to check VPC enablement when processing %s", resource), "namespace", req.Namespace, "name", req.Name)
		return common.ResultRequeue, err
	}
	if !vpcEnabled {
		log.V(2).Info(fmt.Sprintf("VPC is not enabled, ignore %s in the Namespace", resource), "namespace", req.Namespace)
		return common.ResultNormal, nil
	}
	return innerFunc(ctx, req)
}
