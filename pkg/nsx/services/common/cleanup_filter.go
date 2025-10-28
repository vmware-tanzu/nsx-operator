package common

import (
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

// ShouldCleanResource checks if a resource should be cleaned based on namespace/VPC filtering
// This checks the nsx-op/namespace tag first (most reliable), then falls back to path matching
func ShouldCleanResource(targetNamespace, targetVPC string, path *string, tags []model.Tag) bool {
	// If no filtering, clean everything
	if targetNamespace == "" && targetVPC == "" {
		return true
	}

	// Check namespace filtering by tag (most reliable - all resources have nsx-op/namespace tag)
	if targetNamespace != "" {
		for _, tag := range tags {
			if tag.Scope != nil && *tag.Scope == TagScopeNamespace {
				if tag.Tag != nil && *tag.Tag == targetNamespace {
					return true
				}
			}
		}
	}

	// Check VPC filtering - match VPC ID or VPC name in path like /vpcs/vpc-name/
	if targetVPC != "" && path != nil {
		pathStr := *path
		if strings.Contains(pathStr, "/vpcs/"+targetVPC+"/") || strings.Contains(pathStr, "/vpcs/"+targetVPC+"_") {
			return true
		}
	}

	return false
}
