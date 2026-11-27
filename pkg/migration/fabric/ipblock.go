/*
Copyright © 2026 Broadcom, Inc. All Rights Reserved.

	SPDX-License-Identifier: Apache-2.0
*/
package fabric

import (
	"context"
	"fmt"
	"net/netip"
	"sort"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func (s IPBlockService) initializeStore(ctx context.Context, query search.QueryClient, cluster string) error {
	return initializeResourceStore(ctx, query, cluster, IPBlock, common.ResourceTypeIPBlock, s.StoreQueryResult)
}

func (s IPBlockService) StoreQueryResult(path string, e *data.StructValue) error {
	x, err := decode[model.IpAddressBlock](e, model.IpAddressBlockBindingType())
	if err != nil {
		return err
	}
	return s.Store.Put(path, x)
}
func (s IPBlockService) Get(ctx context.Context, r Resource) (Resource, bool, error) {
	if err := ctx.Err(); err != nil {
		return Resource{}, false, err
	}
	o, p, e := scope(r, IPBlock)
	if e != nil {
		return Resource{}, false, e
	}
	x, e := s.Client.Get(o, p, r.ID, nil)
	if e != nil {
		if absent(e) {
			s.Store.Delete(r.Path)
			return Resource{}, false, nil
		}
		return Resource{}, false, e
	}
	if err := s.Store.Put(value(x.Path), &x); err != nil {
		return Resource{}, false, err
	}
	return ipBlockResource(x)
}
func ipBlockResource(x model.IpAddressBlock) (Resource, bool, error) {
	if x.MarkedForDelete != nil && *x.MarkedForDelete {
		return Resource{}, false, &ConflictError{Kind: IPBlock, Path: value(x.Path)}
	}
	out := resourceFrom(IPBlock, value(x.Id), value(x.Path), value(x.ParentPath))
	out.Tags = x.Tags
	cidr, err := ipBlockCIDR(x)
	if err != nil {
		return out, true, err
	}
	out.CIDR = cidr
	out.SubnetExclusive = x.SubnetExclusive != nil && *x.SubnetExclusive
	for _, r := range x.ExcludedIps {
		out.ExcludedIPs = append(out.ExcludedIPs, IPRange{Start: value(r.Start), End: value(r.End)})
	}
	out.ExcludedIPs, err = normalizeExclusions(cidr, out.ExcludedIPs)
	if err != nil {
		return out, true, fmt.Errorf("IP block %s: %w", value(x.Path), err)
	}
	if value(x.Visibility) != model.IpAddressBlock_VISIBILITY_EXTERNAL {
		return out, true, fmt.Errorf("IP block %s has visibility %q, expected %q", value(x.Path), value(x.Visibility), model.IpAddressBlock_VISIBILITY_EXTERNAL)
	}
	return out, true, nil
}

// ipBlockCIDR accepts the legacy single CIDR field as well as the current
// plural field. A fabric block must contain exactly one address range.
func ipBlockCIDR(block model.IpAddressBlock) (string, error) {
	if len(block.Cidrs) > 1 {
		return "", fmt.Errorf("IP block %s has multiple CIDRs", value(block.Path))
	}
	if len(block.Cidrs) == 1 {
		return block.Cidrs[0], nil
	}
	return value(block.Cidr), nil //nolint:staticcheck // Support legacy NSX IP blocks.
}
func (s IPBlockService) Create(ctx context.Context, r Resource, tags []model.Tag) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o, p, e := scope(r, IPBlock)
	if e != nil {
		return e
	}
	visibility := model.IpAddressBlock_VISIBILITY_EXTERNAL
	ranges, err := normalizeExclusions(r.CIDR, r.ExcludedIPs)
	if err != nil {
		return err
	}
	excluded := make([]model.IpPoolRange, 0, len(ranges))
	for _, item := range ranges {
		start, end := item.Start, item.End
		excluded = append(excluded, model.IpPoolRange{Start: &start, End: &end})
	}
	return s.Client.Patch(o, p, r.ID, model.IpAddressBlock{Id: &r.ID, DisplayName: &r.ID, Cidrs: []string{r.CIDR}, ExcludedIps: excluded, SubnetExclusive: &r.SubnetExclusive, Visibility: &visibility, Tags: tags})
}

func (s IPBlockService) expected(req FabricPreparationRequest, project string) Resource {
	if req.IPBlockID == "" {
		return Resource{}
	}
	exclusions, _ := requestIPBlockExclusions(req) // validate has already checked the request.
	return Resource{Kind: IPBlock, ID: req.IPBlockID, Parent: project + "/infra", Path: project + "/infra/ip-blocks/" + req.IPBlockID, CIDR: req.IPBlockCIDR, ExcludedIPs: exclusions, SubnetExclusive: req.IPBlockSubnetExclusive}
}

func requestIPBlockExclusions(req FabricPreparationRequest) ([]IPRange, error) {
	if req.IPBlockCIDR == "" {
		return nil, nil
	}
	if len(req.IPBlockAllowedIPs) == 0 {
		return nil, fmt.Errorf("IP block requires complete source address ranges")
	}
	allowed, err := normalizeExclusions(req.IPBlockCIDR, req.IPBlockAllowedIPs)
	if err != nil {
		return nil, fmt.Errorf("invalid allowed ranges: %w", err)
	}
	prefix, err := netip.ParsePrefix(req.IPBlockCIDR)
	if err != nil {
		return nil, err
	}
	first := prefix.Masked().Addr()
	last := prefixLastAddress(prefix)
	var ranges []IPRange
	next := first
	for _, r := range allowed {
		start, _ := netip.ParseAddr(r.Start)
		end, _ := netip.ParseAddr(r.End)
		if next.IsValid() && next.Less(start) {
			ranges = append(ranges, IPRange{Start: next.String(), End: start.Prev().String()})
		}
		if end == last {
			next = netip.Addr{}
			break
		}
		next = end.Next()
	}
	if next.IsValid() && next.Compare(last) <= 0 {
		ranges = append(ranges, IPRange{Start: next.String(), End: last.String()})
	}
	ranges = append(ranges, req.IPBlockExcludedIPs...)
	if req.IPBlockCIDR != "" && req.SNAT != nil && vpcPurpose(req) == VPCPurposeLoadBalancer {
		ip, err := netip.ParseAddr(req.SNAT.IPAddress)
		if err != nil {
			return nil, fmt.Errorf("invalid SNAT address %q", req.SNAT.IPAddress)
		}
		if prefix.Contains(ip) {
			ranges = append(ranges, IPRange{Start: ip.String(), End: ip.String()})
		}
	}
	return normalizeExclusions(req.IPBlockCIDR, ranges)
}

func prefixLastAddress(prefix netip.Prefix) netip.Addr {
	addr := prefix.Masked().Addr()
	if addr.Is4() {
		b := addr.As4()
		for bit := prefix.Bits(); bit < 32; bit++ {
			b[bit/8] |= byte(1 << (7 - bit%8))
		}
		return netip.AddrFrom4(b)
	}
	b := addr.As16()
	for bit := prefix.Bits(); bit < 128; bit++ {
		b[bit/8] |= byte(1 << (7 - bit%8))
	}
	return netip.AddrFrom16(b)
}

func normalizeExclusions(cidr string, ranges []IPRange) ([]IPRange, error) {
	if len(ranges) == 0 {
		return nil, nil
	}
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil || prefix != prefix.Masked() {
		return nil, fmt.Errorf("invalid IP block CIDR %q", cidr)
	}
	type pair struct{ start, end netip.Addr }
	parsed := make([]pair, 0, len(ranges))
	for _, r := range ranges {
		start, e1 := netip.ParseAddr(r.Start)
		end, e2 := netip.ParseAddr(r.End)
		if e1 != nil || e2 != nil || !prefix.Contains(start) || !prefix.Contains(end) || start.Compare(end) > 0 {
			return nil, fmt.Errorf("invalid exclusion range %q-%q for IP block %s", r.Start, r.End, cidr)
		}
		parsed = append(parsed, pair{start, end})
	}
	sort.Slice(parsed, func(i, j int) bool {
		if parsed[i].start == parsed[j].start {
			return parsed[i].end.Less(parsed[j].end)
		}
		return parsed[i].start.Less(parsed[j].start)
	})
	merged := make([]pair, 0, len(parsed))
	for _, r := range parsed {
		if len(merged) != 0 && r.start.Compare(merged[len(merged)-1].end) <= 0 {
			if merged[len(merged)-1].end.Less(r.end) {
				merged[len(merged)-1].end = r.end
			}
		} else {
			merged = append(merged, r)
		}
	}
	out := make([]IPRange, 0, len(merged))
	for _, r := range merged {
		out = append(out, IPRange{Start: r.start.String(), End: r.end.String()})
	}
	return out, nil
}
