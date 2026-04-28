/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// emptyVPCProvider never resolves a VPC.
type emptyVPCProvider struct{}

func (emptyVPCProvider) ListVPCInfo(string) []common.VPCResourceInfo { return nil }
func (emptyVPCProvider) ListAllVPCNamespaces() []string              { return nil }

func TestVPCIPAddressUsageStorage_Get_NoVPC(t *testing.T) {
	s := NewVPCIPAddressUsageStorage(&nsx.Client{}, emptyVPCProvider{})
	_, err := s.Get(context.Background(), "ns1", "ignored")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no VPC found")
}

func TestIPBlockUsageStorage_List_NoVPC(t *testing.T) {
	s := NewIPBlockUsageStorage(&nsx.Client{}, emptyVPCProvider{})
	list, err := s.List(context.Background(), "ns1")
	require.NoError(t, err)
	require.NotNil(t, list)
	assert.Empty(t, list.Items)
}

func TestSubnetIPPoolsStorage_Get_NoVPC(t *testing.T) {
	s := NewSubnetIPPoolsStorage(&nsx.Client{}, emptyVPCProvider{})
	_, err := s.Get(context.Background(), "ns1", "sub1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no VPC found")
}

func TestSubnetDHCPStatsStorage_Get_NoVPC(t *testing.T) {
	s := NewSubnetDHCPStatsStorage(&nsx.Client{}, emptyVPCProvider{})
	_, err := s.Get(context.Background(), "ns1", "sub1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no VPC found")
}
