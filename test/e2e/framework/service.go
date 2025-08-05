// Copyright © 2019-2021 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package framework

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
)

// CreateService creates a service with the given parameters
func (data *TestData) CreateService(namespace, serviceName string, port, targetPort int32, protocol corev1.Protocol, selector map[string]string,
	serviceType corev1.ServiceType, mutators ...func(service *corev1.Service),
) (*corev1.Service, error) {
	ipFamilies := []corev1.IPFamily{corev1.IPv4Protocol}

	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
			Labels: map[string]string{
				"nsx-op-e2e": serviceName,
				"app":        serviceName,
			},
		},
		Spec: corev1.ServiceSpec{
			SessionAffinity: corev1.ServiceAffinityNone,
			Ports: []corev1.ServicePort{{
				Port:       port,
				TargetPort: intstr.FromInt32(targetPort),
				Protocol:   protocol,
			}},
			Type:       serviceType,
			Selector:   selector,
			IPFamilies: ipFamilies,
		},
	}
	for _, mutator := range mutators {
		mutator(&service)
	}
	return data.ClientSet.CoreV1().Services(namespace).Create(context.TODO(), &service, metav1.CreateOptions{})
}

// ServiceWaitFor polls the K8s apiServer until the specified Service meets the condition
func (data *TestData) ServiceWaitFor(readyTime time.Duration, namespace string, name string, conditionFunc func(svc *corev1.Service) (bool, error)) (*corev1.Service, error) {
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, readyTime, false, func(ctx context.Context) (bool, error) {
		if svc, err := data.ClientSet.CoreV1().Services(namespace).Get(context.TODO(), name, metav1.GetOptions{}); err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("error when getting Service '%s/%s': %v", namespace, name, err)
		} else {
			return conditionFunc(svc)
		}
	})
	if err != nil {
		return nil, err
	}
	return data.ClientSet.CoreV1().Services(namespace).Get(context.TODO(), name, metav1.GetOptions{})
}

// DeleteService deletes a service
func (data *TestData) DeleteService(nsName string, svcName string) error {
	ctx := context.TODO()
	err := data.ClientSet.CoreV1().Services(nsName).Delete(ctx, svcName, metav1.DeleteOptions{})
	if err != nil {
		Log.Error(err, "Failed to delete Service", "namespace", nsName, "name", svcName)
	}
	return err
}
