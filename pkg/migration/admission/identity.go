/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package admission

import api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"

func namespaceIdentity(status *api.TransitionStatus, namespace string) *api.NamespaceIdentityStatus {
	if status == nil {
		return nil
	}
	for i := range status.NamespaceIdentities {
		if status.NamespaceIdentities[i].Namespace == namespace {
			return &status.NamespaceIdentities[i]
		}
	}
	return nil
}

func workloadIdentity(namespace *api.NamespaceIdentityStatus, group, resource, name string) *api.WorkloadIdentityStatus {
	if namespace == nil || name == "" {
		return nil
	}
	for i := range namespace.Workloads {
		workload := &namespace.Workloads[i]
		if workload.Group == group && workload.Resource == resource && workload.Name == name {
			return workload
		}
	}
	return nil
}
