/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package infrastructure

import (
	"context"
	"errors"
	"fmt"

	netop "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var ErrNoManagedSourceNetwork = errors.New("no managed source Network")

// KubernetesSourceNetworks resolves a VDN name or vCenter DVPG ID to one
// managed Network in the Net Operator Namespace. Ambiguous matches are rejected.
type KubernetesSourceNetworks struct {
	Reader    client.Reader
	Namespace string
}

func (source KubernetesSourceNetworks) ForBacking(ctx context.Context, backingID string) (client.ObjectKey, error) {
	if source.Reader == nil || source.Namespace == "" || backingID == "" {
		return client.ObjectKey{}, fmt.Errorf("source Network reader, Namespace and backing ID are required")
	}
	var networks netop.NetworkList
	if err := source.Reader.List(ctx, &networks, client.InNamespace(source.Namespace)); err != nil {
		return client.ObjectKey{}, err
	}
	var found client.ObjectKey
	for _, network := range networks.Items {
		if network.Spec.Type != netop.NetworkTypeVDS || network.Spec.ProviderRef.APIGroup != netop.GroupName || network.Spec.ProviderRef.Kind != "VSphereDistributedNetwork" || network.Spec.ProviderRef.Name == "" || network.Spec.ProviderRef.Namespace != "" {
			continue
		}
		var vdn netop.VSphereDistributedNetwork
		if err := source.Reader.Get(ctx, client.ObjectKey{Name: network.Spec.ProviderRef.Name}, &vdn); err != nil {
			return client.ObjectKey{}, err
		}
		if vdn.Name != backingID && vdn.Spec.PortGroupID != backingID {
			continue
		}
		if found.Name != "" {
			return client.ObjectKey{}, fmt.Errorf("multiple source Networks match DVPG %s", backingID)
		}
		found = client.ObjectKey{Namespace: network.Namespace, Name: network.Name}
	}
	if found.Name == "" {
		return client.ObjectKey{}, fmt.Errorf("%w matches DVPG %s", ErrNoManagedSourceNetwork, backingID)
	}
	return found, nil
}
