/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// +kubebuilder:object:generate=true
// +groupName=migration.vmware.com

// Package v1alpha1 defines the Supervisor network transition API.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion identifies the network transition API version.
	GroupVersion = schema.GroupVersion{Group: "migration.vmware.com", Version: "v1alpha1"}
	// SchemeBuilder registers network transition resources.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	// AddToScheme registers network transition resources in a scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion, &NetworkResourceTransition{}, &NetworkResourceTransitionList{})
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
