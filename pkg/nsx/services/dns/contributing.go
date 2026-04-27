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

func parseContributingOwnersFromRecord(rec *Record) []string {
	if rec == nil {
		return nil
	}
	return parseContributingOwnersTag(firstTagValue(rec.Tags, common.TagScopeDNSRecordContributingOwners))
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
	out := seen.UnsortedList()
	slices.Sort(out)
	return out
}

func formatContributingOwnersTag(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	cp := append([]string(nil), keys...)
	slices.Sort(cp)
	return strings.Join(cp, ",")
}

// mergeContributingOwnerKeys returns sorted unique contributing keys (excludes primaryNNKey).
func mergeContributingOwnerKeys(existing []string, add string, primaryNNKey string) []string {
	seen := sets.New[string]()
	for _, k := range existing {
		k = strings.TrimSpace(k)
		if k == "" || k == primaryNNKey {
			continue
		}
		seen.Insert(k)
	}
	add = strings.TrimSpace(add)
	if add != "" && add != primaryNNKey {
		seen.Insert(add)
	}
	out := seen.UnsortedList()
	slices.Sort(out)
	return out
}

func contributingTagNormalized(tag string) string {
	return formatContributingOwnersTag(parseContributingOwnersTag(tag))
}

func resourceRefFromDNSRecord(rec *Record) (*ResourceRef, bool) {
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

func primaryOwnerNNIndexKeyFromRecord(rec *Record) string {
	return getDNSRecordOwnerNamespacedName(rec)
}

func replaceContributingOwnersInTags(tags []model.Tag, keys []string) []model.Tag {
	out := filterTagsWithoutScope(tags, common.TagScopeDNSRecordContributingOwners)
	if len(keys) == 0 {
		return out
	}
	return append(out, modelTag(common.TagScopeDNSRecordContributingOwners, formatContributingOwnersTag(keys)))
}

func filterTagsWithoutScope(tags []model.Tag, dropScope string) []model.Tag {
	if len(tags) == 0 {
		return nil
	}
	var out []model.Tag
	for _, t := range tags {
		if t.Scope != nil && *t.Scope == dropScope {
			continue
		}
		out = append(out, t)
	}
	return out
}
