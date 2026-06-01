/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/runtime"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func strPtr(s string) *string { return &s }
func int64Ptr(i int64) *int64 { return &i }

// singleVPCProvider returns a fixed VPCEntry (DisplayName = VPCID) for any namespace.
type singleVPCProvider struct{ info common.VPCResourceInfo }

func (p singleVPCProvider) ListVPCInfo(string) []eas.VPCEntry {
	return []eas.VPCEntry{{DisplayName: p.info.VPCID, Info: p.info}}
}
func (p singleVPCProvider) ListAllVPCNamespaces() []string { return nil }

// ---- minimal NSX client mocks ----

type fakeSubnetsClient struct {
	results model.VpcSubnetListResult
	err     error
}

func (f *fakeSubnetsClient) Delete(string, string, string, string) error { return nil }
func (f *fakeSubnetsClient) Get(string, string, string, string) (model.VpcSubnet, error) {
	return model.VpcSubnet{}, nil
}
func (f *fakeSubnetsClient) List(_, _, _ string, _ *string, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.VpcSubnetListResult, error) {
	return f.results, f.err
}
func (f *fakeSubnetsClient) Patch(string, string, string, string, model.VpcSubnet) error { return nil }
func (f *fakeSubnetsClient) Update(string, string, string, string, model.VpcSubnet) (model.VpcSubnet, error) {
	return model.VpcSubnet{}, nil
}

type fakeDHCPStatsClient struct {
	result model.DhcpServerStatistics
	err    error
}

func (f *fakeDHCPStatsClient) Get(_, _, _, _ string, _ *string, _ *string, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.DhcpServerStatistics, error) {
	return f.result, f.err
}

type fakeIPPoolClient struct {
	result model.IpAddressPoolListResult
	err    error
}

func (f *fakeIPPoolClient) Get(string, string, string, string, string) (model.IpAddressPool, error) {
	return model.IpAddressPool{}, nil
}
func (f *fakeIPPoolClient) List(_, _, _, _ string, _ *string, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.IpAddressPoolListResult, error) {
	return f.result, f.err
}

type fakeIPAddressUsageClient struct {
	result model.VpcIpAddressBlocks
	err    error
}

func (f *fakeIPAddressUsageClient) Get(string, string, string) (model.VpcIpAddressBlocks, error) {
	return f.result, f.err
}

type fakeInfraIPBlockUsageClient struct {
	result model.IpAddressBlockUsage
	err    error
}

func (f *fakeInfraIPBlockUsageClient) Get(string) (model.IpAddressBlockUsage, error) {
	return f.result, f.err
}
func (f *fakeInfraIPBlockUsageClient) List(_ *string, _ *bool, _ *string, _ *string, _ *int64, _ *bool, _ *string) (model.IpAddressBlockUsageList, error) {
	return model.IpAddressBlockUsageList{}, nil
}

type fakeProjectIPBlockUsageClient struct {
	getResult  model.IpAddressBlockUsage
	listResult model.IpAddressBlockUsageList
	err        error
}

func (f *fakeProjectIPBlockUsageClient) Get(string, string, string) (model.IpAddressBlockUsage, error) {
	return f.getResult, f.err
}
func (f *fakeProjectIPBlockUsageClient) List(_, _ string, _ *string, _ *bool, _ *string, _ *string, _ *int64, _ *bool, _ *string) (model.IpAddressBlockUsageList, error) {
	return f.listResult, f.err
}

type fakeVpcsClient struct {
	result model.Vpc
	err    error
}

func (f *fakeVpcsClient) Get(_, _, _ string) (model.Vpc, error) {
	return f.result, f.err
}
func (f *fakeVpcsClient) Delete(_, _, _ string, _ *bool) error {
	return nil
}
func (f *fakeVpcsClient) Patch(_, _, _ string, _ model.Vpc) error {
	return nil
}
func (f *fakeVpcsClient) Update(_, _, _ string, _ model.Vpc) (model.Vpc, error) {
	return model.Vpc{}, nil
}
func (f *fakeVpcsClient) List(_, _ string, _ *string, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.VpcListResult, error) {
	return model.VpcListResult{}, nil
}

type fakeVpcAttachmentClient struct {
	result model.VpcAttachmentListResult
	err    error
}

func (f *fakeVpcAttachmentClient) Get(_, _, _, _ string) (model.VpcAttachment, error) {
	return model.VpcAttachment{}, nil
}
func (f *fakeVpcAttachmentClient) Delete(_, _, _, _ string) error {
	return nil
}
func (f *fakeVpcAttachmentClient) Patch(_, _, _, _ string, _ model.VpcAttachment) error {
	return nil
}
func (f *fakeVpcAttachmentClient) Update(_, _, _, _ string, _ model.VpcAttachment) (model.VpcAttachment, error) {
	return model.VpcAttachment{}, nil
}
func (f *fakeVpcAttachmentClient) List(_, _, _ string, _ *string, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.VpcAttachmentListResult, error) {
	return f.result, f.err
}

type fakeVpcConnectivityProfilesClient struct {
	result model.VpcConnectivityProfile
	err    error
}

func (f *fakeVpcConnectivityProfilesClient) Get(_, _, _ string) (model.VpcConnectivityProfile, error) {
	return f.result, f.err
}
func (f *fakeVpcConnectivityProfilesClient) Delete(_, _, _ string) error {
	return nil
}
func (f *fakeVpcConnectivityProfilesClient) Patch(_, _, _ string, _ model.VpcConnectivityProfile) error {
	return nil
}
func (f *fakeVpcConnectivityProfilesClient) Update(_, _, _ string, _ model.VpcConnectivityProfile) (model.VpcConnectivityProfile, error) {
	return model.VpcConnectivityProfile{}, nil
}
func (f *fakeVpcConnectivityProfilesClient) List(_, _ string, _ *string, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.VpcConnectivityProfileListResult, error) {
	return model.VpcConnectivityProfileListResult{}, nil
}

// emptyVPCProvider never resolves a VPC.
type emptyVPCProvider struct{}

func (emptyVPCProvider) ListVPCInfo(string) []eas.VPCEntry { return nil }
func (emptyVPCProvider) ListAllVPCNamespaces() []string    { return nil }

// multiVPCProvider returns a fixed set of VPCEntries for any namespace.
type multiVPCProvider struct{ entries []eas.VPCEntry }

func (p multiVPCProvider) ListVPCInfo(string) []eas.VPCEntry { return p.entries }
func (p multiVPCProvider) ListAllVPCNamespaces() []string    { return nil }

// newFakeK8sClient builds a fake controller-runtime client pre-populated with the given objects.
func newFakeK8sClient(objs ...k8sclient.Object) k8sclient.Client {
	scheme := runtime.NewScheme()
	_ = vpcv1alpha1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}
func TestParseSubnetVPCName(t *testing.T) {
	// Non-default project: "projectID:vpcID"
	org, proj, vpc := parseSubnetVPCName("proj1:vpc1")
	assert.Equal(t, "default", org)
	assert.Equal(t, "proj1", proj)
	assert.Equal(t, "vpc1", vpc)

	// Default NSX project: ":vpcID" (empty prefix produced by GetVPCFullID)
	org, proj, vpc = parseSubnetVPCName(":sean-ns_2oq3d")
	assert.Equal(t, "default", org)
	assert.Equal(t, "default", proj) // empty prefix → NSX default project
	assert.Equal(t, "sean-ns_2oq3d", vpc)

	// Infra-scoped (no colon): "vpcID"
	org, proj, vpc = parseSubnetVPCName("vpc-only")
	assert.Equal(t, "default", org)
	assert.Equal(t, "", proj)
	assert.Equal(t, "vpc-only", vpc)
}
func TestNsxTagValue_Found(t *testing.T) {
	s1, t1 := "scope1", "tag1"
	tags := []model.Tag{{Scope: &s1, Tag: &t1}}
	assert.Equal(t, "tag1", nsxTagValue(tags, "scope1"))
}

func TestNsxTagValue_Missing(t *testing.T) {
	s1, t1 := "scope1", "tag1"
	tags := []model.Tag{{Scope: &s1, Tag: &t1}}
	assert.Equal(t, "", nsxTagValue(tags, "other"))
	assert.Equal(t, "", nsxTagValue(nil, "scope1"))
}
