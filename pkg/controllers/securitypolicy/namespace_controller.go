/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package securitypolicy

import (
	"context"
	"reflect"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// We should consider the below scenarios:
// When a namespace's label is changed and if there are pods in this namespace,
// we should reconcile the corresponding security policy.

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
	if isInSysNs, err := util.IsSystemNamespace(nil, "", obj); err != nil {
		log.Error(err, "failed to fetch namespace", "namespace", obj.Name)
		return
	} else if isInSysNs {
		log.V(2).Info("namespace is in system namespace, ignore it", "namespace", obj.Name)
		return
	}

	podList := &v1.PodList{}
	err := e.Client.List(context.Background(), podList, client.InNamespace(obj.Name))
	if err != nil {
		log.Error(err, "failed to list pod in namespace", "namespace", obj.Name)
		return
	}

	shouldReconcile := false
	for _, pod := range podList.Items {
		if util.CheckPodHasNamedPort(pod, "update") {
			shouldReconcile = true
			break
		}
	}
	if !shouldReconcile {
		log.Info("no pod in namespace is relevant", "namespace", obj.Name)
		return
	}

	err = reconcileSecurityPolicy(e.Client, podList.Items, l)
	if err != nil {
		log.Error(err, "failed to reconcile security policy")
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
