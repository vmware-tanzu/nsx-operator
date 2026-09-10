/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type fakeVPCEndpointClient struct {
	results model.VpcEndpointListResult
	err     error
}

func (f *fakeVPCEndpointClient) List(orgId string, projectId string, vpcId string, cursor *string, includeMarkForDeleteObjects *bool, includedFields *string, pageSize *int64, sortAscending *bool, sortBy *string) (model.VpcEndpointListResult, error) {
	return f.results, f.err
}

func (f *fakeVPCEndpointClient) Delete(orgId string, projectId string, vpcId string, vpcEndpointId string) error {
	return nil
}

func (f *fakeVPCEndpointClient) Get(orgId string, projectId string, vpcId string, vpcEndpointId string) (model.VpcEndpoint, error) {
	return model.VpcEndpoint{}, nil
}

func (f *fakeVPCEndpointClient) Patch(orgId string, projectId string, vpcId string, vpcEndpointId string, vpcEndpoint model.VpcEndpoint) error {
	return nil
}

func (f *fakeVPCEndpointClient) Update(orgId string, projectId string, vpcId string, vpcEndpointId string, vpcEndpoint model.VpcEndpoint) (model.VpcEndpoint, error) {
	return model.VpcEndpoint{}, nil
}

type fakeVPCEndpointStatisticsClient struct {
	result model.VpcEndpointStatistics
	err    error
}

func (f *fakeVPCEndpointStatisticsClient) Get(orgId string, projectId string, vpcId string, vpcEndpointId string) (model.VpcEndpointStatistics, error) {
	return f.result, f.err
}

func TestVPCEndpointStatisticsStorage_Get_NoVPC(t *testing.T) {
	s := NewVPCEndpointStatisticsStorage(&nsx.Client{}, emptyVPCProvider{})
	_, err := s.Get(context.Background(), "ns1", "ep1")
	require.Error(t, err)
	assert.True(t, k8serrors.IsNotFound(err))
}

func TestVPCEndpointStatisticsStorage_Get_ListError(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	c := &nsx.Client{}
	c.VPCEndpointClient = &fakeVPCEndpointClient{err: fmt.Errorf("list error")}
	s := NewVPCEndpointStatisticsStorage(c, p)
	_, err := s.Get(context.Background(), "ns1", "ep1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list error")
}

func TestVPCEndpointStatisticsStorage_Get_EndpointNotFound(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	c := &nsx.Client{}
	c.VPCEndpointClient = &fakeVPCEndpointClient{
		results: model.VpcEndpointListResult{
			Results: []model.VpcEndpoint{},
		},
	}
	s := NewVPCEndpointStatisticsStorage(c, p)
	_, err := s.Get(context.Background(), "ns1", "ep1")
	require.Error(t, err)
	assert.True(t, k8serrors.IsNotFound(err))
}

func TestVPCEndpointStatisticsStorage_Get_StatsError(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	c := &nsx.Client{}
	epID := "ep-id-1"
	tagScope := common.TagScopeVPCEndpointCRName
	tagValue := "ep1"
	c.VPCEndpointClient = &fakeVPCEndpointClient{
		results: model.VpcEndpointListResult{
			Results: []model.VpcEndpoint{
				{
					Id: &epID,
					Tags: []model.Tag{
						{Scope: &tagScope, Tag: &tagValue},
					},
				},
			},
		},
	}
	c.VPCEndpointStatisticsClient = &fakeVPCEndpointStatisticsClient{err: fmt.Errorf("stats error")}
	s := NewVPCEndpointStatisticsStorage(c, p)
	_, err := s.Get(context.Background(), "ns1", "ep1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stats error")
}

func TestVPCEndpointStatisticsStorage_Get_OK(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	c := &nsx.Client{}
	epID := "ep-id-1"
	tagScope := common.TagScopeVPCEndpointCRName
	tagValue := "ep1"
	c.VPCEndpointClient = &fakeVPCEndpointClient{
		results: model.VpcEndpointListResult{
			Results: []model.VpcEndpoint{
				{
					Id: &epID,
					Tags: []model.Tag{
						{Scope: &tagScope, Tag: &tagValue},
					},
				},
			},
		},
	}
	dropped := int64(10)
	c.VPCEndpointStatisticsClient = &fakeVPCEndpointStatisticsClient{
		result: model.VpcEndpointStatistics{
			Rx: &model.ServiceGatewayCounterCommon{
				DroppedPackets: &dropped,
			},
		},
	}
	s := NewVPCEndpointStatisticsStorage(c, p)
	result, err := s.Get(context.Background(), "ns1", "ep1")
	require.NoError(t, err)
	assert.Equal(t, "ep1", result.Name)
	assert.Equal(t, int64(10), result.RX.DroppedPackets)
}
