/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package fabric

import (
	"fmt"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

// FabricServiceClientsFromNSXClient splits the shared migration NSX client into the
// resource-specific clients used by fabric services. All clients retain the
// same cluster transport, authentication state and endpoint failover policy.
func FabricServiceClientsFromNSXClient(client *nsx.Client) (FabricServiceClients, error) {
	if client == nil {
		return FabricServiceClients{}, fmt.Errorf("shared NSX client is required")
	}
	if client.NsxConfig == nil || client.NsxConfig.CoeConfig == nil || client.NsxConfig.Cluster == "" {
		return FabricServiceClients{}, fmt.Errorf("NSX cluster identity is required")
	}
	return FabricServiceClients{
		Cluster:                client.NsxConfig.Cluster,
		QueryClient:            client.QueryClient,
		TransitGatewaysClient:  client.TransitGatewayClient,
		IPBlocksClient:         client.IPBlockClient,
		ProfilesClient:         client.VPCConnectivityProfilesClient,
		VPCsClient:             client.VPCClient,
		AttachmentsClient:      client.VpcAttachmentClient,
		RealizedEntitiesClient: client.RealizedEntitiesClient,
	}, nil
}

// SNATPreparationServiceFromNSXClient wires only the clients required by the
// dedicated SNAT preparation flow. They use the same shared NSX client and
// transport as other migration APIs.
func SNATPreparationServiceFromNSXClient(client *nsx.Client) (*SNATPreparationService, error) {
	if client == nil {
		return nil, fmt.Errorf("shared NSX client is required")
	}
	if client.NsxConfig == nil || client.NsxConfig.CoeConfig == nil || client.NsxConfig.Cluster == "" {
		return nil, fmt.Errorf("NSX cluster identity is required")
	}
	return NewSNATPreparationService(SNATPreparationDependencies{
		Cluster: client.NsxConfig.Cluster, IPBlocks: client.IPBlockClient, Profiles: client.VPCConnectivityProfilesClient,
		Allocations: client.IPAddressAllocationClient, LBServices: client.VPCLBSClient,
		Realizer: NSXRealizer{Client: client.RealizedEntitiesClient},
	})
}
