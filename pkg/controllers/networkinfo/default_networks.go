/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkinfo

import (
	"context"
	"fmt"
	"path"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// NetworkInfo can predate the provider switch and can have a noncanonical name.
func networkInfosInNamespace(ctx context.Context, reader client.Reader, namespace string) []ctrl.Request {
	infos := &v1alpha1.NetworkInfoList{}
	if err := reader.List(ctx, infos, client.InNamespace(namespace)); err != nil {
		log.Error(err, "Could not list NetworkInfos", "namespace", namespace)
		return nil
	}
	var requests []ctrl.Request
	for i := range infos.Items {
		requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&infos.Items[i])})
	}
	return requests
}

// checkDefaultNetworks observes the resources reconciled by the Namespace and
// SubnetSet controllers. Auto-created sets allocate their first subnet on demand.
func (r *NetworkInfoReconciler) checkDefaultNetworks(ctx context.Context, ns string, nc *v1alpha1.VPCNetworkConfiguration, vpcPath string, autoFamilies map[string]v1alpha1.IPAddressType) error {
	for _, role := range []string{servicecommon.DefaultPodNetwork, servicecommon.DefaultVMNetwork} {
		ss, err := common.GetDefaultSubnetSetByNamespace(r.Client, ns, role)
		if err != nil {
			return fmt.Errorf("%s default network in %s: %w", role, ns, err)
		}
		if !ss.DeletionTimestamp.IsZero() || !common.IsObjectReady(ss.Status.Conditions) {
			return fmt.Errorf("%s default network in %s is not ready", role, ns)
		}
		expected := common.DefaultSubnetPaths(nc, role)
		if ss.Spec.SubnetNames == nil && expected.Len() == 0 {
			if autoFamilies[role] == "" || !common.IsSupersetIPAddressTypes(autoFamilies[role], existingSubnetSetFamily(ss)) {
				return fmt.Errorf("target VPC has no compatible address space for %s/%s", ns, ss.Name)
			}
			continue
		}
		if ss.Spec.SubnetNames == nil || expected.Len() == 0 || len(*ss.Spec.SubnetNames) != expected.Len() {
			return fmt.Errorf("%s/%s has not bound the configured %s subnets", ns, ss.Name, role)
		}
		for _, name := range *ss.Spec.SubnetNames {
			subnet := &v1alpha1.Subnet{}
			if err := r.Client.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, subnet); err != nil {
				return err
			}
			subnetPath, err := servicecommon.GetSubnetPathFromAssociatedResource(subnet.Annotations[servicecommon.AnnotationAssociatedResource])
			if err != nil || !expected.Has(subnetPath) || path.Dir(subnetPath) != vpcPath+"/subnets" {
				return fmt.Errorf("subnet %s/%s does not match the %s target in VPC %s", ns, name, role, vpcPath)
			}
			if !subnet.DeletionTimestamp.IsZero() || !common.IsObjectReady(subnet.Status.Conditions) || len(subnet.Status.GatewayAddresses) == 0 {
				return fmt.Errorf("subnet %s/%s is not a ready target for the %s default network", ns, name, role)
			}
			expected.Delete(subnetPath)
		}
	}
	return nil
}

// Existing SubnetSets from IPv4-only releases may omit the IP address type.
func existingSubnetSetFamily(ss *v1alpha1.SubnetSet) v1alpha1.IPAddressType {
	if ss.Spec.IPAddressType == "" {
		return v1alpha1.IPAddressTypeIPv4
	}
	return ss.Spec.IPAddressType
}
