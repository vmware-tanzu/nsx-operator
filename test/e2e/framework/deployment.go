// Copyright © 2019-2021 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package framework

import (
	"context"
	"fmt"
	"time"

	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
)

// DeploymentWaitForNames polls the K8s apiServer once the specific pods are created, no matter they are running or not.
func (data *TestData) DeploymentWaitForNames(timeout time.Duration, namespace, deployment string) ([]string, error) {
	var podNames []string
	opt := metav1.ListOptions{
		LabelSelector: "deployment=" + deployment,
	}
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		if pods, err := data.ClientSet.CoreV1().Pods(namespace).List(context.TODO(), opt); err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("error when getting Pod  %v", err)
		} else {
			for _, p := range pods.Items {
				podNames = append(podNames, p.Name)
			}
			return true, nil
		}
	})
	if err != nil {
		return nil, err
	}
	return podNames, nil
}

// DeploymentWaitForIPsOrNames polls the K8s apiServer until the specified Pod in deployment has an IP address
func (data *TestData) DeploymentWaitForIPsOrNames(timeout time.Duration, namespace, deployment string, replica int) ([]string, []string, error) {
	var podIPStrings []string
	var podNames []string
	opt := metav1.ListOptions{
		LabelSelector: "deployment=" + deployment,
	}

	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		podIPStrings = podIPStrings[:0]
		podNames = podNames[:0]
		if pods, err := data.ClientSet.CoreV1().Pods(namespace).List(context.TODO(), opt); err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("error when getting Pod %v", err)
		} else {
			for _, p := range pods.Items {
				if p.Status.Phase != corev1.PodRunning || p.Status.PodIP == "" {
					continue
				}
				podIPStrings = append(podIPStrings, p.Status.PodIP)
				podNames = append(podNames, p.Name)
			}
			if len(podNames) < replica {
				return false, nil
			}
			return true, nil
		}
	})
	if err != nil {
		return nil, nil, err
	}
	return podIPStrings, podNames, nil
}

// CreateDeployment creates a deployment with the given parameters
func (data *TestData) CreateDeployment(namespace, deploymentName, containerName, image string,
	protocol corev1.Protocol, containerPort int32, replicas int32,
	mutators ...func(deployment *v1.Deployment),
) (*v1.Deployment, error) {
	deployment := v1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        deploymentName,
			Namespace:   namespace,
			Annotations: map[string]string{},
		},
		Spec: v1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"nsx-op-e2e": deploymentName,
					"deployment": deploymentName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"nsx-op-e2e": deploymentName,
						"deployment": deploymentName,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            containerName,
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Ports: []corev1.ContainerPort{
								{
									Protocol:      protocol,
									ContainerPort: containerPort,
								},
							},
						},
					},
					HostNetwork: false,
					// Set it to 1s for immediate shutdown to reduce test run time and to avoid affecting subsequent tests.
					TerminationGracePeriodSeconds: ptr.To[int64](1),
				},
			},
		},
	}

	for _, mutator := range mutators {
		mutator(&deployment)
	}
	return data.ClientSet.AppsV1().Deployments(namespace).Create(context.TODO(), &deployment, metav1.CreateOptions{})
}
