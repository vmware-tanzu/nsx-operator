/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package namespaces checks the live members of a namespace transition.
package namespaces

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Validate rejects absent or terminating members before admitting a batch and
// before dispatching a mutating handler. The lifecycle webhook prevents deleting
// or recreating either object while the persisted batch can still operate on it.
// These reads are not an atomic transaction with concurrent admission requests.
func Validate(ctx context.Context, reader client.Reader, names []string) error {
	for _, name := range names {
		namespace := &corev1.Namespace{}
		if err := reader.Get(ctx, types.NamespacedName{Name: name}, namespace); err != nil {
			return fmt.Errorf("reading Kubernetes Namespace %q: %w", name, err)
		}
		if !namespace.DeletionTimestamp.IsZero() {
			return fmt.Errorf("Kubernetes Namespace %q is terminating", name)
		}
		if err := validateSupervisorNamespace(ctx, reader, name); err != nil {
			return err
		}
	}
	return nil
}

func validateSupervisorNamespace(ctx context.Context, reader client.Reader, name string) error {
	var lastErr error
	for _, version := range []string{"v1alpha2", "v1alpha1"} {
		object := &unstructured.Unstructured{}
		object.SetGroupVersionKind(schema.GroupVersionKind{Group: "iaas.vmware.com", Version: version, Kind: "SupervisorNamespace"})
		err := reader.Get(ctx, types.NamespacedName{Name: name}, object)
		if err == nil {
			if !object.GetDeletionTimestamp().IsZero() {
				return fmt.Errorf("SupervisorNamespace %q is terminating", name)
			}
			return nil
		}
		lastErr = err
		if !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err) {
			break
		}
	}
	return fmt.Errorf("reading SupervisorNamespace %q: %w", name, lastErr)
}
