/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	nsxpolicy "github.com/vmware/vsphere-automation-sdk-go/services/nsxt"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs"
	"go.uber.org/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	dnsrecmocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnsrecordsclient"
	dnszonemocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnszonesclient"
	searchmocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/searchclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type testOrgsClient struct{ nsxpolicy.OrgsClient }

func (testOrgsClient) List(*string, *bool, *bool, *string, *int64, *bool, *string) (model.OrgListResult, error) {
	return model.OrgListResult{Results: []model.Org{{Id: common.String("org1")}}}, nil
}

type testProjectsClient struct{ orgs.ProjectsClient }

func (testProjectsClient) List(orgID string, _ *string, _ *bool, _ *bool, _ *string, _ *string, _ *int64, _ *bool, _ *string) (model.ProjectListResult, error) {
	if orgID != "org1" {
		return model.ProjectListResult{}, nil
	}
	return model.ProjectListResult{Results: []model.Project{{Id: common.String("proj1")}}}, nil
}

func TestRestoreDNSRecordsWithoutCurrentConfiguration(t *testing.T) {
	ctrl := gomock.NewController(t)
	records := dnsrecmocks.NewMockDnsRecordsClient(ctrl)
	records.EXPECT().List("org1", "proj1", nil, nil, nil, nil, nil, nil, nil, nil).Return(model.DnsRecordListResult{
		Results: []model.DnsRecord{{
			Id: common.String("legacy"), Path: common.String("/orgs/org1/projects/proj1/dns-records/legacy"),
			ZonePath: common.String(testDNSZonePathT),
			Tags:     []model.Tag{{Scope: common.String(common.TagScopeCluster), Tag: common.String("unit-test")}},
		}},
	}, nil)
	svc := &DNSRecordService{
		Service: common.Service{NSXClient: &nsx.Client{
			OrgsClient: testOrgsClient{}, ProjectClient: testProjectsClient{}, DnsRecordsClient: records,
		}},
		DNSRecordStore: BuildDNSRecordStore(),
	}
	require.NoError(t, svc.restoreDNSRecordsFromProjects("unit-test", nil))
	require.Len(t, svc.DNSRecordStore.ListDNSRecords(), 1)
}

func TestInitializeDNSRecordServiceRestoresProjectRecords(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	nc := &v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "nc"},
		Spec: v1alpha1.VPCNetworkConfigurationSpec{
			NSXProject: "/orgs/org1/projects/proj1",
			DNSZones:   []string{testDNSZonePathT},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nc).Build()
	ctrl := gomock.NewController(t)
	query := searchmocks.NewMockQueryClient(ctrl)
	query.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(model.SearchResponse{}, nil)
	records := dnsrecmocks.NewMockDnsRecordsClient(ctrl)
	zone := dnszonemocks.NewMockZonesClient(ctrl)
	clusterTag := model.Tag{Scope: common.String(common.TagScopeCluster), Tag: common.String("unit-test")}
	ownerTags := []model.Tag{
		clusterTag,
		{Scope: common.String(common.TagScopeDNSRecordFor), Tag: common.String(common.TagValueDNSRecordForDNSRecord)},
		{Scope: common.String(common.TagScopeDNSRecordOwnerNamespace), Tag: common.String("ns1")},
		{Scope: common.String(common.TagScopeDNSRecordOwnerName), Tag: common.String("rec1")},
	}
	rec := model.DnsRecord{
		Id:         common.String("api_zone-t_a"),
		Path:       common.String("/orgs/org1/projects/proj1/dns-records/api_zone-t_a"),
		RecordName: common.String("api"), RecordType: common.String(model.DnsRecord_RECORD_TYPE_A),
		ZonePath: common.String(testDNSZonePathT), Tags: ownerTags,
	}
	otherCluster := rec
	otherCluster.Id = common.String("other_zone-t_a")
	otherCluster.Path = common.String("/orgs/org1/projects/proj1/dns-records/other_zone-t_a")
	otherCluster.Tags = []model.Tag{{Scope: common.String(common.TagScopeCluster), Tag: common.String("other-cluster")}}
	next := "next"
	records.EXPECT().List("org1", "proj1", nil, nil, nil, nil, nil, nil, nil, nil).
		Return(model.DnsRecordListResult{Results: []model.DnsRecord{rec, otherCluster}, Cursor: &next}, nil)
	records.EXPECT().List("org1", "proj1", &next, nil, nil, nil, nil, nil, nil, nil).
		Return(model.DnsRecordListResult{}, nil)
	domain := "example.com"
	zone.EXPECT().Get("org1", "proj1", "ds1", "zone-t").Return(model.DnsZone{DnsDomainName: &domain}, nil)
	cfg := &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "unit-test"}}
	svc, err := InitializeDNSRecordService(common.Service{
		Client: c, NSXConfig: cfg,
		NSXClient: &nsx.Client{QueryClient: query, DnsRecordsClient: records, DnsZoneClient: zone, NsxConfig: cfg},
	}, nil, c)
	require.NoError(t, err)
	require.Len(t, svc.DNSRecordStore.ListDNSRecords(), 1)
	require.Len(t, svc.DNSRecordStore.GetByOwnerResourceNamespacedName(ResourceKindDNSRecord, "ns1", "rec1"), 1)
}

func TestInitializeDNSRecordServiceFailsOnProjectRecordListError(t *testing.T) {
	for _, tt := range []struct {
		name       string
		failOnPage int
	}{
		{name: "first page", failOnPage: 1},
		{name: "later page", failOnPage: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&v1alpha1.VPCNetworkConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "nc"},
				Spec: v1alpha1.VPCNetworkConfigurationSpec{
					NSXProject: "/orgs/org1/projects/proj1",
				},
			}).Build()
			ctrl := gomock.NewController(t)
			query := searchmocks.NewMockQueryClient(ctrl)
			query.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(model.SearchResponse{}, nil)
			records := dnsrecmocks.NewMockDnsRecordsClient(ctrl)
			listErr := errors.New("NSX temporarily unavailable")
			firstPage := records.EXPECT().List("org1", "proj1", nil, nil, nil, nil, nil, nil, nil, nil)
			if tt.failOnPage == 1 {
				firstPage.Return(model.DnsRecordListResult{}, listErr)
			} else {
				next := "next"
				firstPage.Return(model.DnsRecordListResult{Cursor: &next}, nil)
				records.EXPECT().List("org1", "proj1", &next, nil, nil, nil, nil, nil, nil, nil).
					Return(model.DnsRecordListResult{}, listErr)
			}
			cfg := &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "unit-test"}}
			_, err := InitializeDNSRecordService(common.Service{
				Client: reader, NSXConfig: cfg,
				NSXClient: &nsx.Client{QueryClient: query, DnsRecordsClient: records, NsxConfig: cfg},
			}, nil, reader)
			require.ErrorIs(t, err, listErr)
			require.ErrorContains(t, err, "org1/proj1")
		})
	}
}

type errorOrgsClient struct{ nsxpolicy.OrgsClient }

func (errorOrgsClient) List(*string, *bool, *bool, *string, *int64, *bool, *string) (model.OrgListResult, error) {
	return model.OrgListResult{}, errors.New("403 Forbidden")
}

func TestRestoreDNSRecordsFailsOnOrgListError(t *testing.T) {
	svc := &DNSRecordService{
		Service: common.Service{NSXClient: &nsx.Client{
			OrgsClient: errorOrgsClient{}, ProjectClient: testProjectsClient{},
		}},
		DNSRecordStore: BuildDNSRecordStore(),
	}
	err := svc.restoreDNSRecordsFromProjects("unit-test", nil)
	require.ErrorContains(t, err, "listing NSX organizations for DNS record restore")
}

type errorProjectsClient struct{ orgs.ProjectsClient }

func (errorProjectsClient) List(string, *string, *bool, *bool, *string, *string, *int64, *bool, *string) (model.ProjectListResult, error) {
	return model.ProjectListResult{}, errors.New("503 Service Unavailable")
}

func TestRestoreDNSRecordsFailsOnProjectListError(t *testing.T) {
	svc := &DNSRecordService{
		Service: common.Service{NSXClient: &nsx.Client{
			OrgsClient: testOrgsClient{}, ProjectClient: errorProjectsClient{},
		}},
		DNSRecordStore: BuildDNSRecordStore(),
	}
	err := svc.restoreDNSRecordsFromProjects("unit-test", nil)
	require.ErrorContains(t, err, "listing NSX projects in organization org1 for DNS record restore")
}
