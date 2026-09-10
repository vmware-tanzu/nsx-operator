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

type fakeVPCServiceEndpointClient struct {
	results model.VpcServiceEndpointListResult
	err     error
}

func (f *fakeVPCServiceEndpointClient) List(orgId string, projectId string, vpcId string, cursor *string, includeMarkForDeleteObjects *bool, includedFields *string, pageSize *int64, sortAscending *bool, sortBy *string) (model.VpcServiceEndpointListResult, error) {
	return f.results, f.err
}

func (f *fakeVPCServiceEndpointClient) Delete(orgId string, projectId string, vpcId string, vpcServiceEndpointId string) error {
	return nil
}

func (f *fakeVPCServiceEndpointClient) Get(orgId string, projectId string, vpcId string, vpcServiceEndpointId string) (model.VpcServiceEndpoint, error) {
	return model.VpcServiceEndpoint{}, nil
}

func (f *fakeVPCServiceEndpointClient) Patch(orgId string, projectId string, vpcId string, vpcServiceEndpointId string, vpcServiceEndpoint model.VpcServiceEndpoint) error {
	return nil
}

func (f *fakeVPCServiceEndpointClient) Update(orgId string, projectId string, vpcId string, vpcServiceEndpointId string, vpcServiceEndpoint model.VpcServiceEndpoint) (model.VpcServiceEndpoint, error) {
	return model.VpcServiceEndpoint{}, nil
}

type fakeVPCServiceEndpointStatisticsClient struct {
	result model.VpcServiceEndpointStatistics
	err    error
}

func (f *fakeVPCServiceEndpointStatisticsClient) Get(orgId string, projectId string, vpcId string, vpcServiceEndpointId string) (model.VpcServiceEndpointStatistics, error) {
	return f.result, f.err
}

func TestServiceEndpointStatisticsStorage_Get_NoVPC(t *testing.T) {
	s := NewServiceEndpointStatisticsStorage(&nsx.Client{}, emptyVPCProvider{})
	_, err := s.Get(context.Background(), "ns1", "ep1")
	require.Error(t, err)
	assert.True(t, k8serrors.IsNotFound(err))
}

func TestServiceEndpointStatisticsStorage_Get_ListError(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	c := &nsx.Client{}
	c.VPCServiceEndpointClient = &fakeVPCServiceEndpointClient{err: fmt.Errorf("list error")}
	s := NewServiceEndpointStatisticsStorage(c, p)
	_, err := s.Get(context.Background(), "ns1", "ep1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list error")
}

func TestServiceEndpointStatisticsStorage_Get_EndpointNotFound(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	c := &nsx.Client{}
	c.VPCServiceEndpointClient = &fakeVPCServiceEndpointClient{
		results: model.VpcServiceEndpointListResult{
			Results: []model.VpcServiceEndpoint{},
		},
	}
	s := NewServiceEndpointStatisticsStorage(c, p)
	_, err := s.Get(context.Background(), "ns1", "ep1")
	require.Error(t, err)
	assert.True(t, k8serrors.IsNotFound(err))
}

func TestServiceEndpointStatisticsStorage_Get_StatsError(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	c := &nsx.Client{}
	epID := "ep-id-1"
	tagScope := common.TagScopeVPCServiceEndpointCRName
	tagValue := "ep1"
	c.VPCServiceEndpointClient = &fakeVPCServiceEndpointClient{
		results: model.VpcServiceEndpointListResult{
			Results: []model.VpcServiceEndpoint{
				{
					Id: &epID,
					Tags: []model.Tag{
						{Scope: &tagScope, Tag: &tagValue},
					},
				},
			},
		},
	}
	c.VPCServiceEndpointStatisticsClient = &fakeVPCServiceEndpointStatisticsClient{err: fmt.Errorf("stats error")}
	s := NewServiceEndpointStatisticsStorage(c, p)
	_, err := s.Get(context.Background(), "ns1", "ep1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stats error")
}

func TestServiceEndpointStatisticsStorage_Get_OK(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	c := &nsx.Client{}
	epID := "ep-id-1"
	tagScope := common.TagScopeVPCServiceEndpointCRName
	tagValue := "ep1"
	c.VPCServiceEndpointClient = &fakeVPCServiceEndpointClient{
		results: model.VpcServiceEndpointListResult{
			Results: []model.VpcServiceEndpoint{
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
	c.VPCServiceEndpointStatisticsClient = &fakeVPCServiceEndpointStatisticsClient{
		result: model.VpcServiceEndpointStatistics{
			Rx: &model.ServiceGatewayCounterCommon{
				DroppedPackets: &dropped,
			},
		},
	}
	s := NewServiceEndpointStatisticsStorage(c, p)
	result, err := s.Get(context.Background(), "ns1", "ep1")
	require.NoError(t, err)
	assert.Equal(t, "ep1", result.Name)
	assert.Equal(t, int64(10), result.RX.DroppedPackets)
}
