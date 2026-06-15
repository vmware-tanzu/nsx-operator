/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"io"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func parseContributingOwnersFromRecord(rec *model.DnsRecord) []string {
	return parseContributingOwnersTag(decompressContributingTags(rec))
}

func decompressContributingTags(rec *model.DnsRecord) string {
	encodedContributingKeys := firstTagValue(rec.Tags, common.TagScopeDNSRecordContributingOwners)
	if encodedContributingKeys == "" {
		return ""
	}
	return decompressString(encodedContributingKeys)
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

func formatContributingOwnersTag(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	cp := sortedCopyStrings(keys)
	plainText := strings.Join(cp, ",")
	// Compress the consolidated plain text into a tight string under 255 characters.
	return compressString(plainText)
}

// mergeContributingOwnerKeys returns sorted unique contributing keys (excludes primaryNNKey).
func mergeContributingOwnerKeys(existing string, add string, primaryNNKey string) string {
	raw := strings.Join([]string{existing, add}, ",")
	seen := sets.New[string]()
	for _, p := range strings.Split(raw, ",") {
		k := strings.TrimSpace(p)
		if k == "" || k == primaryNNKey {
			continue
		}
		if k != "" {
			seen.Insert(k)
		}
	}
	return formatContributingOwnersTag(seen.UnsortedList())
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

func primaryOwnerNNIndexKeyFromRecord(rec *model.DnsRecord) string {
	return getDNSRecordOwnerNamespacedName(rec)
}

// appendGatewayAndContributionTags appends the optional GatewayIndexList and ContributingOwners tags
// onto tags when their values are non-empty, returning the extended slice. The caller is
// responsible for passing a slice it owns so this function may append to it directly.
func appendGatewayAndContributionTags(tags []model.Tag, gwKey string, ctag string) []model.Tag {
	if gwKey = strings.TrimSpace(gwKey); gwKey != "" {
		tags = append(tags, modelTag(common.TagScopeDNSRecordGatewayIndexList, gwKey))
	}
	if ctag != "" {
		tags = append(tags, modelTag(common.TagScopeDNSRecordContributingOwners, ctag))
	}
	return tags
}

func replaceContributingOwnersInTags(tags []model.Tag, newContribKeys []string) []model.Tag {
	out := make([]model.Tag, 0)
	for _, t := range tags {
		if t.Scope == nil {
			continue
		}
		if *t.Scope != common.TagScopeDNSRecordContributingOwners {
			out = append(out, t)
			continue
		}
		if len(newContribKeys) > 0 {
			out = append(out, modelTag(common.TagScopeDNSRecordContributingOwners, formatContributingOwnersTag(newContribKeys)))
		}
	}
	return out
}

// compressString compresses plain-text keys using zlib and encodes the result into RawURLEncoding Base64.
func compressString(src string) string {
	if src == "" {
		return ""
	}
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	_, _ = w.Write([]byte(src))
	_ = w.Close()

	// RawURLEncoding is used to strip trailing padding characters ('='), saving extra bytes.
	return base64.RawURLEncoding.EncodeToString(b.Bytes())
}

// decompressString decodes the Base64 payload and inflates the zlib stream back to plain text.
func decompressString(compressed string) string {
	if compressed == "" || !strings.HasPrefix(compressed, "eJ") {
		return compressed
	}

	data, err := base64.RawURLEncoding.DecodeString(compressed)
	if err != nil {
		return compressed // Fallback to raw string if Base64 decoding fails.
	}

	b := bytes.NewReader(data)
	r, err := zlib.NewReader(b)
	if err != nil {
		return compressed // Fallback to raw string if zlib reader initialization fails.
	}
	defer r.Close()

	var out bytes.Buffer
	maxDecompressedSize := int64(1024 * 1024)
	limitedReader := io.LimitReader(r, maxDecompressedSize+1)
	_, err = io.Copy(&out, limitedReader)
	if err != nil {
		log.Error(err, "Failed to decompress the string, returning the compressed value", "value", compressed)
		return compressed
	}
	return out.String()
}
