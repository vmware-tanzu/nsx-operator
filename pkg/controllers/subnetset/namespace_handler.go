/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetset

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

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
)

// SubnetSet controller should watch event of namespace, when there are some updates of namespace labels,
// controller should build tags and update VpcSubnetSetSet according to new labels.

type EnqueueRequestForNamespace struct {
	Client client.Client
}

func (e *EnqueueRequestForNamespace) Create(_ event.CreateEvent, _ workqueue.RateLimitingInterface) {
	log.V(1).Info("namespace create event, do nothing")
}

func (e *EnqueueRequestForNamespace) Delete(_ event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	log.V(1).Info("namespace delete event, do nothing")
}

func (e *EnqueueRequestForNamespace) Generic(_ event.GenericEvent, _ workqueue.RateLimitingInterface) {
	log.V(1).Info("namespace generic event, do nothing")
}

func (e *EnqueueRequestForNamespace) Update(updateEvent event.UpdateEvent, l workqueue.RateLimitingInterface) {
	obj := updateEvent.ObjectNew.(*v1.Namespace)
	err := reconcileSubnetSet(e.Client, obj.Name, l)
	if err != nil {
		log.Error(err, "failed to reconcile subnet")
	}
}

var PredicateFuncsNs = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return false
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldObj := e.ObjectOld.(*v1.Namespace)
		newObj := e.ObjectNew.(*v1.Namespace)
		log.V(1).Info("receive namespace update event", "name", oldObj.Name)
		if reflect.DeepEqual(oldObj.ObjectMeta.Labels, newObj.ObjectMeta.Labels) {
			log.Info("label of namespace is not changed, ignore it", "name", oldObj.Name)
			return false
		}
		return true
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return false
	},
}

func reconcileSubnetSet(c client.Client, namespace string, q workqueue.RateLimitingInterface) error {
	subnetSetList := &v1alpha1.SubnetSetList{}
	err := c.List(context.Background(), subnetSetList, client.InNamespace(namespace))
	if err != nil {
		log.Error(err, "failed to list all the subnets")
		return err
	}

	for _, subnet_set_item := range subnetSetList.Items {
		log.Info("reconcile subnet set because namespace update",
			"namespace", subnet_set_item.Namespace, "name", subnet_set_item.Name)
		q.Add(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      subnet_set_item.Name,
				Namespace: subnet_set_item.Namespace,
			},
		})
	}
	return nil
}
