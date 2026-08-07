/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"slices"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func parseContributingOwnersFromRecord(rec *model.DnsRecord) []string {
	tagKeys := parseContributingOwnersTag(decompressContributingTags(rec))

	var allKeys []string
	allKeys = append(allKeys, tagKeys...)

	additionalTag := firstTagValue(rec.Tags, common.TagScopeDNSRecordAdditionalContributingOwners)
	if additionalTag != "" {
		additionalKeys := parseContributingOwnersTag(additionalTag)
		allKeys = append(allKeys, additionalKeys...)
	}

	return sortedCopyStrings(allKeys)
}

func decompressContributingTags(rec *model.DnsRecord) string {
	encodedContributingKeys := firstTagValue(rec.Tags, common.TagScopeDNSRecordContributingOwners)
	if encodedContributingKeys == "" {
		return ""
	}
	return encodedContributingKeys
}

func parseContributingOwnersTag(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	seen := sets.New[string]()
	for _, p := range strings.Split(raw, ",") {
		k := strings.TrimSpace(p)
		if k != "" {
			seen.Insert(k)
		}
	}
	out := sets.List(seen)
	return out
}

func formatContributingOwnersTag(keys []string) (string, string) {
	if len(keys) == 0 {
		return "", ""
	}
	contributionTag, extraTag := joinAndPackStrings(keys)
	if len(extraTag) > 0 {
		log.Info("Tag length exceeds dns_contributing_owners limit (255), putting remaining in dns_contributing_owners_ext")
	}
	return contributionTag, extraTag
}

// mergeContributingOwnerKeys returns sorted unique contributing keys (excludes primaryNNKey).
func mergeContributingOwnerKeys(existing []string, add string, primaryNNKey string) []string {
	seen := sets.New[string]()
	for _, k := range existing {
		k = strings.TrimSpace(k)
		if k != "" && k != primaryNNKey {
			seen.Insert(k)
		}
	}
	k := strings.TrimSpace(add)
	if k != "" && k != primaryNNKey {
		seen.Insert(k)
	}
	return sortedCopyStrings(seen.UnsortedList())
}

func resourceRefFromDNSRecord(rec *model.DnsRecord) (*ResourceRef, bool) {
	if rec == nil {
		return nil, false
	}
	createdFor, ns, name, ok := ownerCreatedForAndNNFromDNSRecord(rec)
	if !ok || ns == "" || name == "" {
		return nil, false
	}
	kind := resourceKindFromCreatedForTag(createdFor)
	if kind == "" {
		return nil, false
	}
	meta := metav1.ObjectMeta{Namespace: ns, Name: name}
	return &ResourceRef{Kind: kind, Object: &meta}, true
}

// parseOwnerNNIndexKey parses "createdFor/ns/name" owner index keys.
func parseOwnerNNIndexKey(key string) (createdFor, ns, name string, ok bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", "", false
	}
	parts := strings.SplitN(key, "/", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

func ownerNNIndexKeyForResourceRef(owner *ResourceRef) string {
	if owner == nil {
		return ""
	}
	createdFor := resourceKindToCreatedFor(owner.Kind)
	if createdFor == "" {
		return ""
	}
	return dnsRecordOwnerKey(createdFor, dnsRecordOwnerNamespacedNameKey(owner.GetNamespace(), owner.GetName()))
}

// appendGatewayAndContributionTags appends the optional GatewayIndexList and ContributingOwners tags
// onto tags when their values are non-empty, returning the extended slice. The caller is
// responsible for passing a slice it owns so this function may append to it directly.
func appendGatewayAndContributionTags(tags []model.Tag, gwKey string, contribKeys []string) []model.Tag {
	if gwKey = strings.TrimSpace(gwKey); gwKey != "" {
		tags = append(tags, modelTag(common.TagScopeDNSRecordGatewayIndexList, gwKey))
	}
	if len(contribKeys) > 0 {
		plain, overflow := formatContributingOwnersTag(contribKeys)
		if plain != "" {
			tags = append(tags, modelTag(common.TagScopeDNSRecordContributingOwners, plain))
		}
		if overflow != "" {
			tags = append(tags, modelTag(common.TagScopeDNSRecordAdditionalContributingOwners, overflow))
		}
	}
	return tags
}

func replaceContributingOwnersInTags(tags []model.Tag, newContribKeys []string) []model.Tag {
	out := make([]model.Tag, 0)
	for _, t := range tags {
		if t.Scope == nil {
			continue
		}
		if *t.Scope == common.TagScopeDNSRecordContributingOwners || *t.Scope == common.TagScopeDNSRecordAdditionalContributingOwners {
			continue
		}
		out = append(out, t)
	}
	if len(newContribKeys) > 0 {
		plain, overflow := formatContributingOwnersTag(newContribKeys)
		if plain != "" {
			out = append(out, modelTag(common.TagScopeDNSRecordContributingOwners, plain))
		}
		if overflow != "" {
			out = append(out, modelTag(common.TagScopeDNSRecordAdditionalContributingOwners, overflow))
		}
	}
	return out
}

// joinAndPackStrings sorts and joins src strings into two comma-separated
// chunks (primary and overflow), each strictly bounded to 255 bytes.
func joinAndPackStrings(src []string) (string, string) {
	if len(src) == 0 {
		return "", ""
	}

	sorted := slices.Clone(src)
	slices.Sort(sorted)

	var sb, overflowSB strings.Builder
	sb.Grow(255)
	overflowSB.Grow(255)

	// push attempts to append an item to the specified builder (truncating single oversized items).
	// It returns true if the item was successfully appended within the 255-byte limit.
	push := func(b *strings.Builder, item string) bool {
		if len(item) > 255 {
			item = item[:255]
		}
		needComma := b.Len() > 0
		addedLen := len(item)
		if needComma {
			addedLen++
		}

		if b.Len()+addedLen > 255 {
			return false
		}
		if needComma {
			b.WriteString(",")
		}
		b.WriteString(item)
		return true
	}

	truncated := false
	for _, item := range sorted {
		if push(&sb, item) {
			continue
		}
		if !push(&overflowSB, item) {
			truncated = true
		}
	}

	if truncated {
		log.Info("Tag length exceeds the extra tag's limit (255), truncating additional inputs")
	}

	return sb.String(), overflowSB.String()
}
