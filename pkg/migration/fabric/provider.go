/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package fabric

import (
	"context"
	"fmt"
	"sync"

	nsxmigration "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/migration"
)

// FabricServiceProvider lazily constructs fabric services from the shared
// migration NSX client. Store initialization is performed separately for
// selected kinds.
type FabricServiceProvider struct {
	NSX     *nsxmigration.Service
	mu      sync.Mutex
	service *FabricService
	snat    *SNATPreparationService
	stores  *Stores
}

// GetSNATPreparation builds the dedicated SNAT service from the same shared
// NSX client used by the generic fabric service. It does not create a second
// transport or initialize unrelated fabric resource stores.
func (p *FabricServiceProvider) GetSNATPreparation(ctx context.Context) (*SNATPreparationService, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p == nil || p.NSX == nil {
		return nil, fmt.Errorf("migration NSX service is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.snat != nil {
		return p.snat, nil
	}
	client, err := p.NSX.Client()
	if err != nil {
		return nil, err
	}
	service, err := SNATPreparationServiceFromNSXClient(client)
	if err != nil {
		return nil, err
	}
	if p.stores == nil {
		p.stores = NewStores()
	}
	service.stores = p.stores
	p.snat = service
	return service, nil
}

// GetFabricService lazily builds and caches the shared fabric preparation service.
func (p *FabricServiceProvider) GetFabricService(ctx context.Context) (*FabricService, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p == nil || p.NSX == nil {
		return nil, fmt.Errorf("migration NSX service is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.service != nil {
		return p.service, nil
	}
	client, err := p.NSX.Client()
	if err != nil {
		return nil, err
	}
	deps, err := FabricServiceClientsFromNSXClient(client)
	if err != nil {
		return nil, err
	}
	service, err := NewFabricService(deps)
	if err != nil {
		return nil, err
	}
	if p.stores == nil {
		p.stores = NewStores()
	}
	service.TransitGateways.Store = p.stores.TGWs
	service.IPBlocks.Store = p.stores.Blocks
	service.Profiles.Store = p.stores.Profiles
	service.VPCs.Store = p.stores.VPCs
	service.Attachments.Store = p.stores.Attachments
	p.service = service
	return service, nil
}
