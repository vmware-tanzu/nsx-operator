/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnet

import (
	"context"
	"reflect"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

// Subnet controller should watch event of Namespace, when there are some updates of Namespace labels,
// controller should build tags and update VpcSubnet according to new labels.

type EnqueueRequestForNamespace struct {
	Client client.Client
}

func (e *EnqueueRequestForNamespace) Create(_ context.Context, _ event.CreateEvent, _ workqueue.RateLimitingInterface) {
	log.V(1).Info("Namespace create event, do nothing")
}

func (e *EnqueueRequestForNamespace) Delete(_ context.Context, _ event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	log.V(1).Info("Namespace delete event, do nothing")
}

func (e *EnqueueRequestForNamespace) Generic(_ context.Context, _ event.GenericEvent, _ workqueue.RateLimitingInterface) {
	log.V(1).Info("Namespace generic event, do nothing")
}

func (e *EnqueueRequestForNamespace) Update(_ context.Context, updateEvent event.UpdateEvent, l workqueue.RateLimitingInterface) {
	obj := updateEvent.ObjectNew.(*v1.Namespace)
	err := requeueSubnet(e.Client, obj.Name, l)
	if err != nil {
		log.Error(err, "Failed to requeue Subnet")
	}
}

var PredicateFuncsNs = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return false
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldObj := e.ObjectOld.(*v1.Namespace)
		newObj := e.ObjectNew.(*v1.Namespace)
		log.V(1).Info("Receive Namespace update event", "Name", oldObj.Name)
		if reflect.DeepEqual(oldObj.ObjectMeta.Labels, newObj.ObjectMeta.Labels) {
			log.Info("Labels of Namespace are not changed", "Name", oldObj.Name)
			return false
		}
		return true
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return false
	},
}

func listSubnet(c client.Client, ctx context.Context, options ...client.ListOption) (*v1alpha1.SubnetList, error) {
	subnetList := &v1alpha1.SubnetList{}
	err := c.List(ctx, subnetList, options...)
	if err != nil {
		return nil, err
	}
	return subnetList, nil
}

func requeueSubnet(c client.Client, ns string, q workqueue.RateLimitingInterface) error {
	subnetList, err := listSubnet(c, context.Background(), client.InNamespace(ns))
	if err != nil {
		log.Error(err, "Failed to list all the Subnets")
		return err
	}

	for _, subnet_item := range subnetList.Items {
		log.Info("Requeue Subnet because Namespace update",
			"Namespace", subnet_item.Namespace, "Name", subnet_item.Name)
		q.Add(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      subnet_item.Name,
				Namespace: subnet_item.Namespace,
			},
		})
	}
	return nil
}
