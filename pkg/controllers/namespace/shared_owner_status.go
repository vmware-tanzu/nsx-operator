/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package namespace

import (
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const sharedOwnerUnavailableReason = "SharedVPCOwnerUnavailable"

// Preserve references and existing allocations while the owner is unavailable.
// On recovery, wake the resource controllers to revalidate; only they can report
// Ready=True after checking the backend and member status.
func (r *NamespaceReconciler) updateSharedOwnerResourceStatus(ctx context.Context, ns string, unavailable bool) error {
	if !r.NSXConfig.NetworkMigrationEnabled() || !config.IsPerNamespaceProvidersSupported() {
		return nil
	}
	subnets, subnetSets := &v1alpha1.SubnetList{}, &v1alpha1.SubnetSetList{}
	if err := r.APIReader.List(ctx, subnets, client.InNamespace(ns)); err != nil {
		return err
	}
	if err := r.APIReader.List(ctx, subnetSets, client.InNamespace(ns)); err != nil {
		return err
	}
	var objects []client.Object
	for i := range subnets.Items {
		if servicecommon.IsSharedSubnet(&subnets.Items[i]) {
			objects = append(objects, &subnets.Items[i])
		}
	}
	for i := range subnetSets.Items {
		if subnetSets.Items[i].Spec.SubnetNames != nil {
			objects = append(objects, &subnetSets.Items[i])
		}
	}
	for _, obj := range objects {
		if !obj.GetDeletionTimestamp().IsZero() {
			continue
		}
		var conditions *[]v1alpha1.Condition
		switch resource := obj.(type) {
		case *v1alpha1.Subnet:
			conditions = &resource.Status.Conditions
		case *v1alpha1.SubnetSet:
			conditions = &resource.Status.Conditions
		}
		index := -1
		for i := range *conditions {
			if (*conditions)[i].Type == v1alpha1.Ready {
				index = i
				break
			}
		}
		if !unavailable && (index < 0 || (*conditions)[index].Reason != sharedOwnerUnavailableReason) {
			continue
		}
		// Recheck with the API reader before each status write. An old queued
		// event or a stale cached NotFound must not override a restored owner or
		// an explicitly selected child configuration.
		namespace := &corev1.Namespace{}
		if err := r.APIReader.Get(ctx, client.ObjectKey{Name: ns}, namespace); err != nil {
			return err
		}
		if !namespace.DeletionTimestamp.IsZero() {
			return nil
		}
		if _, explicit := namespace.Annotations[config.VPCNetworkConfigAnnotation]; unavailable && explicit {
			return nil
		}
		allowed, err := common.IsVPCNamespaceOrShared(ctx, r.APIReader, namespace)
		var ownerErr *common.SharedVPCOwnerUnavailableError
		if err != nil && !errors.As(err, &ownerErr) {
			return err
		}
		if unavailable && ownerErr == nil || !unavailable && !allowed {
			return nil
		}
		condition := v1alpha1.Condition{Type: v1alpha1.Ready, LastTransitionTime: metav1.Now()}
		if unavailable {
			condition.Status, condition.Reason, condition.Message = corev1.ConditionFalse, sharedOwnerUnavailableReason, ownerErr.Error()
		} else {
			condition.Status, condition.Reason, condition.Message = corev1.ConditionUnknown, "SharedVPCOwnerRevalidation", "Shared VPC owner is available; awaiting resource revalidation"
		}
		if index >= 0 {
			if common.IsConditionSemanticEqual(&(*conditions)[index], &condition) {
				continue
			}
			(*conditions)[index] = condition
		} else {
			*conditions = append(*conditions, condition)
		}
		if err := r.Client.Status().Update(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
			// Conflicts and failed writes retry through the Namespace queue.
			return err
		}
	}
	return nil
}
