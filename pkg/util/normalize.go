/* Copyright © 2025 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func NormalizeId(name string) string {
	newName := strings.ReplaceAll(name, ":", "_")
	if len(newName) <= MaxIdLength {
		return newName
	}
	hashString := Sha1(name)
	nameLength := MaxIdLength - HashLength - 1
	for strings.ContainsAny(string(newName[nameLength-1]), "-._") {
		nameLength--
	}
	newName = fmt.Sprintf("%s-%s", newName[:nameLength], hashString[:HashLength])
	return newName
}

// GenerateIDByObject generate string id for NSX resource using the provided Object's name and the hash of CR uid.
// Note, this function is used on the resources within the VPC scenario, and the provided obj is the K8s CR
// used to generate the NSX resource.
// Note: This function may use hash(obj.UID)[:5] as the return string's suffix. Since the hash suffix is short,
// it may have collision with the existing NSX resources;
// nsx services layer provides the corresponding handle.
func GenerateIDByObject(obj metav1.Object) string {
	limit := MaxIdLength
	uidStr := string(obj.GetUID())
	suffix := TruncateUIDHash(uidStr)
	desiredName := connectStrings(ConnectorUnderline, obj.GetName(), suffix)
	if len(desiredName) > limit {
		valueLen := limit - len(suffix) - 1
		desiredName = connectStrings(ConnectorUnderline, obj.GetName()[:valueLen], suffix)
	}
	return desiredName
}

// GenerateIDByObjectWithSuffix is only used to generate the NSX Security Rule id for now.
// TODO: remove this function after Security Rule id switch to `GenerateIDByObject`.
func GenerateIDByObjectWithSuffix(obj metav1.Object, suffix string) string {
	limit := MaxIdLength
	limit -= len(suffix) + 1
	return connectStrings(ConnectorUnderline, normalizeNameByLimit(obj.GetName(), string(obj.GetUID()), limit, truncateNameOrIDHash), suffix)
}

// GenerateID generate id for NSX resource, some resources have a complex index, so set its type to string.
// Note, this function is used with T1 scenario, and the VPC resources (e.g., Security Rule) which are not migrated
// to the new desired ID format. For new introduced NSX VPC resources, please use functions like
// "BuildUniqueIDWithRandomUUID" in pkg/services/common/builder.go
func GenerateID(resID, prefix, suffix string, index string) string {
	return connectStrings(ConnectorUnderline, prefix, resID, index, suffix)
}

func generateDisplayName(connector, resName, prefix, suffix, project, cluster string) string {
	// Return a string in this format:
	// prefix<connector>cluster<connector>resName<connector>project<connector>suffix.
	return connectStrings(connector, prefix, cluster, resName, project, suffix)
}

func GenerateTruncName(limit int, resName string, prefix, suffix, project, cluster string) string {
	adjustedLimit := limit - len(prefix) - len(suffix)
	for _, i := range []string{prefix, suffix} {
		if len(i) > 0 {
			adjustedLimit -= 1
		}
	}
	oldName := generateDisplayName(ConnectorUnderline, resName, "", "", project, cluster)
	if len(oldName) > adjustedLimit {
		newName := normalizeNameByLimit(oldName, "", adjustedLimit, TruncateUIDHash)
		return generateDisplayName(ConnectorUnderline, newName, prefix, suffix, "", "")
	}
	return generateDisplayName(ConnectorUnderline, resName, prefix, suffix, project, cluster)
}

func GetRandomIndexString() string {
	uuidStr := uuid.NewString()
	return Sha1(uuidStr)[:HashLength]
}

var (
	clusterUUID uuid.UUID
)

func GetClusterUUID(clusterID string) uuid.UUID {
	if clusterUUID == uuid.Nil {
		clusterUUID = uuid.NewSHA1(uuid.NameSpaceX500, []byte(clusterID))
	}
	return clusterUUID
}

func NormalizeLabels(matchLabels *map[string]string) *map[string]string {
	newLabels := make(map[string]string)
	for k, v := range *matchLabels {
		newLabels[NormalizeLabelKey(k, truncateLabelHash)] = NormalizeLabelValue(v, truncateLabelHash)
	}
	return &newLabels
}

func NormalizeLabelKey(key string, shaFn func(data string) string) string {
	if len(key) <= MaxTagScopeLength {
		return key
	}
	splitted := strings.Split(key, "/")
	key = splitted[len(splitted)-1]
	return normalizeNameByLimit(key, "", MaxTagScopeLength, shaFn)
}

func NormalizeLabelValue(value string, shaFn func(data string) string) string {
	return normalizeNameByLimit(value, "", MaxTagValueLength, shaFn)
}

func normalizeNameByLimit(name string, suffix string, limit int, hashFn func(data string) string) string {
	newName := connectStrings(ConnectorUnderline, name, suffix)
	if len(newName) <= limit {
		return newName
	}

	hashedTarget := name
	if len(suffix) > 0 {
		hashedTarget = suffix
	}

	hashString := hashFn(hashedTarget)
	nameLength := limit - len(hashString) - 1
	if len(name) < nameLength {
		nameLength = len(name)
	}
	return strings.Join([]string{name[:nameLength], hashString}, ConnectorUnderline)
}
