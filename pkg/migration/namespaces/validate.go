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

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

// UnavailableError indicates that a batch member cannot currently be read or
// used. Callers can report this separately from a detected identity change.
type UnavailableError struct {
	err error
}

func (e *UnavailableError) Error() string { return e.err.Error() }

func (e *UnavailableError) Unwrap() error { return e.err }

func unavailable(err error) error { return &UnavailableError{err: err} }

// Validate rejects absent or terminating members before admitting a batch and
// before dispatching a mutating handler. The lifecycle webhook prevents deleting
// or recreating either object while the persisted batch can still operate on it.
// These reads are not an atomic transaction with concurrent admission requests.
func Validate(ctx context.Context, reader client.Reader, names []string) error {
	if err := validateNames(names); err != nil {
		return err
	}
	for _, name := range names {
		namespace := &corev1.Namespace{}
		if err := reader.Get(ctx, types.NamespacedName{Name: name}, namespace); err != nil {
			return unavailable(fmt.Errorf("reading Kubernetes Namespace %q: %w", name, err))
		}
		if !namespace.DeletionTimestamp.IsZero() {
			return unavailable(fmt.Errorf("Kubernetes Namespace %q is terminating", name))
		}
		if _, err := getSupervisorNamespace(ctx, reader, name); err != nil {
			return unavailable(err)
		}
	}
	return nil
}

// Snapshot reads the current Kubernetes Namespace and SupervisorNamespace UIDs
// for each batch member. Callers persist the first successful result and compare
// later reads against it before authorizing migration actions.
func Snapshot(ctx context.Context, reader client.Reader, names []string) ([]api.NamespaceIdentityStatus, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("namespace batch must contain at least one namespace")
	}
	if err := validateNames(names); err != nil {
		return nil, err
	}
	identities := make([]api.NamespaceIdentityStatus, 0, len(names))
	for _, name := range names {
		namespace := &corev1.Namespace{}
		if err := reader.Get(ctx, types.NamespacedName{Name: name}, namespace); err != nil {
			return nil, unavailable(fmt.Errorf("reading Kubernetes Namespace %q: %w", name, err))
		}
		if !namespace.DeletionTimestamp.IsZero() {
			return nil, unavailable(fmt.Errorf("Kubernetes Namespace %q is terminating", name))
		}
		supervisorNamespace, err := getSupervisorNamespace(ctx, reader, name)
		if err != nil {
			return nil, unavailable(err)
		}
		if namespace.UID == "" || supervisorNamespace.GetUID() == "" {
			return nil, unavailable(fmt.Errorf("namespace %q has an empty Kubernetes Namespace or SupervisorNamespace UID", name))
		}
		identities = append(identities, api.NamespaceIdentityStatus{
			Namespace: name, NamespaceUID: string(namespace.UID),
			SupervisorNamespaceUID: string(supervisorNamespace.GetUID()),
		})
	}
	return identities, nil
}

// ValidateSnapshot checks that every current member still has the UIDs captured
// during Evaluate. It validates the snapshot structure before reading the API.
func ValidateSnapshot(ctx context.Context, reader client.Reader, names []string, expected []api.NamespaceIdentityStatus) error {
	if len(names) == 0 {
		return fmt.Errorf("namespace batch must contain at least one namespace")
	}
	if err := validateNames(names); err != nil {
		return err
	}
	if len(names) != len(expected) {
		return fmt.Errorf("namespace identity snapshot has %d entries for a batch of %d namespaces", len(expected), len(names))
	}
	byName := make(map[string]api.NamespaceIdentityStatus, len(expected))
	for _, identity := range expected {
		if identity.Namespace == "" || identity.NamespaceUID == "" || identity.SupervisorNamespaceUID == "" {
			return fmt.Errorf("namespace identity snapshot is incomplete")
		}
		if _, found := byName[identity.Namespace]; found {
			return fmt.Errorf("namespace identity snapshot contains duplicate namespace %q", identity.Namespace)
		}
		byName[identity.Namespace] = identity
	}
	for _, name := range names {
		if _, found := byName[name]; !found {
			return fmt.Errorf("namespace %q is missing from the persisted identity snapshot", name)
		}
	}
	current, err := Snapshot(ctx, reader, names)
	if err != nil {
		return err
	}
	for _, identity := range current {
		want := byName[identity.Namespace]
		if identity.NamespaceUID != want.NamespaceUID || identity.SupervisorNamespaceUID != want.SupervisorNamespaceUID {
			return fmt.Errorf("namespace %q object UID changed since Evaluate", identity.Namespace)
		}
	}
	return nil
}

func validateNames(names []string) error {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" {
			return fmt.Errorf("namespace batch contains an empty namespace name")
		}
		if _, found := seen[name]; found {
			return fmt.Errorf("namespace %q appears more than once in the batch", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func getSupervisorNamespace(ctx context.Context, reader client.Reader, name string) (*unstructured.Unstructured, error) {
	var lastErr error
	for _, version := range []string{"v1alpha2", "v1alpha1"} {
		object := &unstructured.Unstructured{}
		object.SetGroupVersionKind(schema.GroupVersionKind{Group: "iaas.vmware.com", Version: version, Kind: "SupervisorNamespace"})
		err := reader.Get(ctx, types.NamespacedName{Name: name}, object)
		if err == nil {
			if !object.GetDeletionTimestamp().IsZero() {
				return nil, fmt.Errorf("SupervisorNamespace %q is terminating", name)
			}
			return object, nil
		}
		lastErr = err
		if !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err) {
			break
		}
	}
	return nil, fmt.Errorf("reading SupervisorNamespace %q: %w", name, lastErr)
}
