/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package fabric

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/utils/ptr"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

func TestFabricServiceClientsFromNSXClient(t *testing.T) {
	_, err := FabricServiceClientsFromNSXClient(nil)
	require.ErrorContains(t, err, "shared NSX client is required")

	_, err = FabricServiceClientsFromNSXClient(&nsx.Client{})
	require.ErrorContains(t, err, "NSX cluster identity is required")

	client := &nsx.Client{
		NsxConfig:   &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "cluster"}},
		QueryClient: &searchClient{}, TransitGatewayClient: &gatewayClient{}, IPBlockClient: &ipBlocksClient{},
		VPCConnectivityProfilesClient: &profilesClient{}, VPCClient: &vpcsClient{},
		VpcAttachmentClient: &attachmentsClient{}, RealizedEntitiesClient: realizedClient{},
	}
	clients, err := FabricServiceClientsFromNSXClient(client)
	require.NoError(t, err)
	require.Equal(t, "cluster", clients.Cluster)
	require.Same(t, client.QueryClient, clients.QueryClient)
	require.Same(t, client.TransitGatewayClient, clients.TransitGatewaysClient)
	require.Same(t, client.IPBlockClient, clients.IPBlocksClient)
	require.Same(t, client.VPCConnectivityProfilesClient, clients.ProfilesClient)
	require.Same(t, client.VPCClient, clients.VPCsClient)
	require.Same(t, client.VpcAttachmentClient, clients.AttachmentsClient)
	require.Equal(t, client.RealizedEntitiesClient, clients.RealizedEntitiesClient)
}

func TestSNATPreparationServiceFromNSXClient(t *testing.T) {
	_, err := SNATPreparationServiceFromNSXClient(nil)
	require.ErrorContains(t, err, "shared NSX client is required")

	_, err = SNATPreparationServiceFromNSXClient(&nsx.Client{})
	require.ErrorContains(t, err, "NSX cluster identity is required")

	client := &nsx.Client{
		NsxConfig:     &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "cluster"}},
		IPBlockClient: &ipBlocksClient{}, VPCConnectivityProfilesClient: &profilesClient{},
		IPAddressAllocationClient: &snatAllocations{}, VPCLBSClient: &snatLBs{},
		RealizedEntitiesClient: realizedClient{},
	}
	service, err := SNATPreparationServiceFromNSXClient(client)
	require.NoError(t, err)
	require.Equal(t, "cluster", service.cluster)
	require.Same(t, client.IPBlockClient, service.ipBlocks)
	require.Same(t, client.VPCConnectivityProfilesClient, service.profiles)
	require.Same(t, client.IPAddressAllocationClient, service.allocations)
	require.Same(t, client.VPCLBSClient, service.lbServices)
}

func TestFabricServiceProviderRejectsUnavailableAndCanceledRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var provider *FabricServiceProvider
	_, err := provider.GetFabricService(ctx)
	require.ErrorIs(t, err, context.Canceled)
	_, err = provider.GetSNATPreparation(ctx)
	require.ErrorIs(t, err, context.Canceled)

	_, err = provider.GetFabricService(context.Background())
	require.ErrorContains(t, err, "migration NSX service is required")
	_, err = provider.GetSNATPreparation(context.Background())
	require.ErrorContains(t, err, "migration NSX service is required")

	provider = &FabricServiceProvider{}
	_, err = provider.GetFabricService(context.Background())
	require.ErrorContains(t, err, "migration NSX service is required")
	_, err = provider.GetSNATPreparation(context.Background())
	require.ErrorContains(t, err, "migration NSX service is required")
}

func TestFabricQueryStoreContract(t *testing.T) {
	called := false
	store := fabricQueryStore{ctx: context.Background(), kind: TGW, store: func(path string, _ *data.StructValue) error {
		called = true
		require.Equal(t, "/path", path)
		return nil
	}}
	require.ErrorContains(t, store.TransResourceToStore(nil), "empty resource")
	require.ErrorContains(t, store.TransResourceToStore(data.NewStructValue("Resource", map[string]data.DataValue{})), "without path")
	require.ErrorContains(t, store.TransResourceToStore(data.NewStructValue("Resource", map[string]data.DataValue{"path": data.NewStringValue("")})), "empty path")
	require.NoError(t, store.TransResourceToStore(data.NewStructValue("Resource", map[string]data.DataValue{"path": data.NewStringValue("/path")})))
	require.True(t, called)

	cause := errors.New("store rejected object")
	store.store = func(string, *data.StructValue) error { return cause }
	require.ErrorIs(t, store.TransResourceToStore(data.NewStructValue("Resource", map[string]data.DataValue{"path": data.NewStringValue("/path")})), cause)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store.ctx = ctx
	require.ErrorIs(t, store.TransResourceToStore(nil), context.Canceled)
	require.Nil(t, store.ListIndexFuncValues("anything"))
	require.ErrorContains(t, store.Apply(nil), "does not support Apply")
	require.True(t, store.IsPolicyAPI())
}

func TestResourceStoreQueryResultDecodeErrors(t *testing.T) {
	bad := data.NewStructValue("not-the-expected-type", map[string]data.DataValue{})
	tests := []struct {
		name string
		call func() error
	}{
		{"transit gateway", func() error {
			return (TransitGatewayService{Store: NewResourceStore[model.TransitGateway]()}).StoreQueryResult("/tgw", bad)
		}},
		{"ip block", func() error {
			return (IPBlockService{Store: NewResourceStore[model.IpAddressBlock]()}).StoreQueryResult("/block", bad)
		}},
		{"profile", func() error {
			return (ConnectivityProfileService{Store: NewResourceStore[model.VpcConnectivityProfile]()}).StoreQueryResult("/profile", bad)
		}},
		{"vpc", func() error { return (VPCService{Store: NewResourceStore[model.Vpc]()}).StoreQueryResult("/vpc", bad) }},
		{"attachment", func() error {
			return (AttachmentService{Store: NewResourceStore[model.VpcAttachment]()}).StoreQueryResult("/attachment", bad)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { require.Error(t, tt.call()) })
	}
}

func TestInitializeResourceStoreRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := initializeResourceStore(ctx, &searchClient{}, "cluster", TGW, "TransitGateway", func(string, *data.StructValue) error { return nil })
	require.ErrorIs(t, err, context.Canceled)
}

type realizationErrorStub struct{ err error }

func (r realizationErrorStub) List(string, *string) (model.GenericPolicyRealizedResourceListResult, error) {
	return model.GenericPolicyRealizedResourceListResult{}, r.err
}

func TestNSXRealizerReadinessErrorAndPendingStates(t *testing.T) {
	ready, err := (NSXRealizer{Client: realizationErrorStub{err: errors.New("API unavailable")}}).Ready(context.Background(), "/resource")
	require.ErrorContains(t, err, "API unavailable")
	require.False(t, ready)

	ready, err = (NSXRealizer{Client: pagedRealizationStub{result: model.GenericPolicyRealizedResourceListResult{}}}).Ready(context.Background(), "/resource")
	require.NoError(t, err)
	require.False(t, ready)

	ready, err = (NSXRealizer{Client: pagedRealizationStub{result: model.GenericPolicyRealizedResourceListResult{Results: []model.GenericPolicyRealizedResource{{}}}}}).Ready(context.Background(), "/resource")
	require.NoError(t, err)
	require.False(t, ready)

	ready, err = (NSXRealizer{Client: pagedRealizationStub{result: model.GenericPolicyRealizedResourceListResult{Results: []model.GenericPolicyRealizedResource{{State: ptr.To(model.GenericPolicyRealizedResource_STATE_ERROR)}}}}}).Ready(context.Background(), "/resource")
	require.ErrorContains(t, err, "realization error")
	require.False(t, ready)
}

func TestConflictErrorFormatsResourceIdentity(t *testing.T) {
	err := (&ConflictError{Kind: VPC, Path: "/orgs/o/projects/p/vpcs/v"}).Error()
	require.Equal(t, "conflicting vpc at /orgs/o/projects/p/vpcs/v", err)
}
