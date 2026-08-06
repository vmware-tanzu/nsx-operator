/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"errors"

	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
)

// HandleEASError translates NSX VAPI errors and K8s API errors into proper K8s REST errors (403/404).
// If the error doesn't match a known 403/404 condition, it returns the fallback error.
func HandleEASError(err error, resource string, name string, fallback error) error {
	if err == nil {
		return nil
	}
	gr := schema.GroupResource{Group: easv1alpha1.GroupVersion.Group, Resource: resource}

	// Check for K8s errors first
	if k8serrors.IsNotFound(err) {
		return k8serrors.NewNotFound(gr, name)
	}
	if k8serrors.IsForbidden(err) {
		return k8serrors.NewForbidden(gr, name, err)
	}

	// Check for NSX VAPI errors
	var notFound vapierrors.NotFound
	if errors.As(err, &notFound) {
		return k8serrors.NewNotFound(gr, name)
	}
	var unauthorized vapierrors.Unauthorized
	if errors.As(err, &unauthorized) {
		return k8serrors.NewForbidden(gr, name, err)
	}

	return fallback
}
