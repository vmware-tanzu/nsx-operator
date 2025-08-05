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
	"k8s.io/apimachinery/pkg/util/wait"
)

// CreateNamespace creates the provided namespace
func (data *TestData) CreateNamespace(namespace string, mutators ...func(ns *corev1.Namespace)) error {
	ns := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}
	for _, mutator := range mutators {
		mutator(&ns)
	}
	if ns, err := data.ClientSet.CoreV1().Namespaces().Create(context.TODO(), &ns, metav1.CreateOptions{}); err != nil {
		// Ignore error if the namespace already exists
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("error when creating '%s' Namespace: %v", namespace, err)
		}
		// When namespace already exists, check phase
		if ns.Status.Phase == corev1.NamespaceTerminating {
			return fmt.Errorf("error when creating '%s' Namespace: namespace exists but is in 'Terminating' phase", namespace)
		}
	}
	return nil
}

// DeleteNamespace deletes the provided namespace and waits for deletion to actually complete
func (data *TestData) DeleteNamespace(namespace string, timeout time.Duration) error {
	var gracePeriodSeconds int64
	propagationPolicy := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriodSeconds,
		PropagationPolicy:  &propagationPolicy,
	}
	if err := data.ClientSet.CoreV1().Namespaces().Delete(context.TODO(), namespace, deleteOptions); err != nil {
		if errors.IsNotFound(err) {
			// namespace does not exist, we return right away
			return nil
		}
		return fmt.Errorf("error when deleting '%s' Namespace: %v", namespace, err)
	}
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		if ns, err := data.ClientSet.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{}); err != nil {
			if errors.IsNotFound(err) {
				// Success
				return true, nil
			}
			return false, fmt.Errorf("error when getting Namespace '%s' after delete: %v", namespace, err)
		} else if ns.Status.Phase != corev1.NamespaceTerminating {
			return false, fmt.Errorf("deleted Namespace '%s' should be in 'Terminating' phase", namespace)
		}
		// Keep trying
		return false, nil
	})
	return err
}

// CreateVCNamespace creates a VC namespace with the provided namespace.
// This is a wrapper around VCClient.CreateVCNamespace for backward compatibility.
func (data *TestData) CreateVCNamespace(namespace string) error {
	if data.VCClient == nil {
		return fmt.Errorf("VCClient is not initialized")
	}
	return data.VCClient.CreateVCNamespace(namespace)
}

// DeleteVCNamespace deletes the provided VC namespace and waits for deletion to actually complete.
// This is a wrapper around VCClient.DeleteNamespace for backward compatibility.
func (data *TestData) DeleteVCNamespace(namespace string) error {
	if data.VCClient == nil {
		return fmt.Errorf("VCClient is not initialized")
	}
	return data.VCClient.DeleteNamespace(namespace)
}
