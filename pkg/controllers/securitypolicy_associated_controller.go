/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	"reflect"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
)

var ignoreNs = []string{"kube-", "vmware-"}

// We should consider the below sceneries:
// When a new added pod whose port name exists in security policy.
// When a deleted pod whose port name exists in security policy.
// When a pod's label is changed.
// When a namespace's label is changed.
// In summary, we could roughly think if the port name of security policy exists in the
// new pod or old pod, we should reconcile the security policy.

type EnqueueRequestForNamespace struct {
	Client client.Client
}

func (e *EnqueueRequestForNamespace) Create(
	_ event.CreateEvent,
	_ workqueue.RateLimitingInterface,
) {
	log.V(1).Info("namespace create event, do nothing")
}

func (e *EnqueueRequestForNamespace) Delete(
	_ event.DeleteEvent,
	_ workqueue.RateLimitingInterface,
) {
	log.V(1).Info("namespace delete event, do nothing")
}

func (e *EnqueueRequestForNamespace) Generic(
	_ event.GenericEvent,
	_ workqueue.RateLimitingInterface,
) {
	log.V(1).Info("namespace generic event, do nothing")
}

func (e *EnqueueRequestForNamespace) Update(
	updateEvent event.UpdateEvent,
	l workqueue.RateLimitingInterface,
) {
	obj := updateEvent.ObjectNew.(*v1.Namespace)

	podList := &v1.PodList{}
	err := e.Client.List(context.Background(), podList, client.InNamespace(obj.Name))
	if err != nil {
		log.Error(err, "failed to list pod in namespace", "namespace", obj.Name)
		return
	}

	shouldReconcile := false
	for _, pod := range podList.Items {
		if checkPodHasNamedPort(pod, "update") {
			shouldReconcile = true
			break
		}
	}
	if !shouldReconcile {
		log.Info("no pod in namespace is relevant", "namespace", obj.Namespace, "name", obj.Name)
		return
	}

	err = reconcileSecurityPolicy(e.Client, podList.Items, l)
	if err != nil {
		log.Error(err, "Failed to reconcile security policy")
	}
}

type EnqueueRequestForPod struct {
	Client client.Client
}

func (e *EnqueueRequestForPod) Create(
	createEvent event.CreateEvent,
	l workqueue.RateLimitingInterface,
) {
	e.Raw(createEvent, l)
}

func (e *EnqueueRequestForPod) Update(
	updateEvent event.UpdateEvent,
	l workqueue.RateLimitingInterface,
) {
	e.Raw(updateEvent, l)
}

func (e *EnqueueRequestForPod) Delete(
	deleteEvent event.DeleteEvent,
	l workqueue.RateLimitingInterface,
) {
	e.Raw(deleteEvent, l)
}

func (e *EnqueueRequestForPod) Generic(
	genericEvent event.GenericEvent,
	l workqueue.RateLimitingInterface,
) {
	e.Raw(genericEvent, l)
}

func (e *EnqueueRequestForPod) Raw(evt interface{}, q workqueue.RateLimitingInterface) {
	var pods []v1.Pod
	var obj client.Object
	var oldObj client.Object

	switch et := evt.(type) {
	case event.CreateEvent:
		obj = et.Object.(*v1.Pod)
	case event.UpdateEvent:
		obj = et.ObjectNew.(*v1.Pod)
		oldObj = et.ObjectOld.(*v1.Pod)
		pods = append(pods, *oldObj.(*v1.Pod))
	case event.DeleteEvent:
		obj = et.Object.(*v1.Pod)
	case event.GenericEvent:
		obj = et.Object.(*v1.Pod)
	default:
		log.Error(nil, "Unknown event type", "event", evt)
	}
	pod := obj.(*v1.Pod)
	pods = append(pods, *pod)
	err := reconcileSecurityPolicy(e.Client, pods, q)
	if err != nil {
		log.Error(err, "Failed to reconcile security policy")
	}
}

func getAllPodPortNames(pods []v1.Pod) sets.String {
	podPortNames := sets.NewString()
	for _, pod := range pods {
		for _, container := range pod.Spec.Containers {
			for _, port := range container.Ports {
				if port.Name != "" {
					podPortNames.Insert(port.Name)
				}
			}
		}
	}
	return podPortNames
}

func reconcileSecurityPolicy(
	client client.Client,
	pods []v1.Pod,
	q workqueue.RateLimitingInterface,
) error {
	podPortNames := getAllPodPortNames(pods)
	log.V(1).Info("pod named port", "podPortNames", podPortNames)
	spList := &v1alpha1.SecurityPolicyList{}
	err := client.List(context.Background(), spList)
	if err != nil {
		log.Error(err, "failed to list security policy")
		return err
	}

	for _, securityPolicy := range spList.Items {
		shouldReconcile := false
		for _, rule := range securityPolicy.Spec.Rules {
			for _, port := range rule.Ports {
				if port.Port.Type == intstr.String {
					if podPortNames.Has(port.Port.StrVal) {
						shouldReconcile = true
					}
				}
			}
		}
		if shouldReconcile {
			log.Info(
				"reconcile it",
				"namespace",
				securityPolicy.Namespace,
				"name",
				securityPolicy.Name,
			)
			q.Add(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      securityPolicy.Name,
					Namespace: securityPolicy.Namespace,
				},
			})
		}
	}
	return nil
}

var PredicateFuncsPod = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		if p, ok := e.Object.(*v1.Pod); ok {
			return checkPodHasNamedPort(*p, "create")
		}
		return false
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		if p, ok := e.ObjectOld.(*v1.Pod); ok {
			if checkPodHasNamedPort(*p, "update") {
				return true
			}
		}
		if p, ok := e.ObjectNew.(*v1.Pod); ok {
			if checkPodHasNamedPort(*p, "update") {
				return true
			}
		}
		return false
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		if p, ok := e.Object.(*v1.Pod); ok {
			return checkPodHasNamedPort(*p, "delete")
		}
		return false
	},
}

func checkPodHasNamedPort(pod v1.Pod, reason string) bool {
	for _, ns := range ignoreNs {
		if strings.HasPrefix(pod.Namespace, ns) {
			return false
		}
	}
	for _, container := range pod.Spec.Containers {
		for _, port := range container.Ports {
			if port.Name != "" {
				log.Info(
					"receive pod event",
					"reason",
					reason,
					"namespace",
					pod.Namespace,
					"name",
					pod.Name,
					"port",
					port.Name,
					"portNumber",
					port.ContainerPort,
					"protocol",
					port.Protocol,
				)
				return true
			}
		}
	}
	return false
}

var PredicateFuncsNs = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return false
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldObj := e.ObjectOld.(*v1.Namespace)
		newObj := e.ObjectNew.(*v1.Namespace)
		for _, ns := range ignoreNs {
			if strings.HasPrefix(newObj.Name, ns) {
				return false
			}
		}
		log.Info("receive namespace event", "name", oldObj.Name)
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
