/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package common

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

var (
	// Currently NSX only has default org
	orgId = "default"
)

func QueryTagCondition(resourceType, cluster string) string {
	return fmt.Sprintf("%s:%s AND tags.scope:%s AND tags.tag:%s",
		ResourceType, resourceType,
		strings.Replace(TagScopeCluster, "/", "\\/", -1),
		strings.Replace(cluster, ":", "\\:", -1))
}

func ConvertTagsToMPTags(tags []model.Tag) []mpmodel.Tag {
	mpTags := make([]mpmodel.Tag, len(tags))
	for i := 0; i < len(tags); i++ {
		mpTags[i].Scope = tags[i].Scope
		mpTags[i].Tag = tags[i].Tag
	}
	return mpTags
}

func ConvertMPTagsToTags(mpTags []mpmodel.Tag) []model.Tag {
	tags := make([]model.Tag, len(mpTags))
	for i := 0; i < len(mpTags); i++ {
		tags[i].Scope = mpTags[i].Scope
		tags[i].Tag = mpTags[i].Tag
	}
	return tags
}

func ParseVPCResourcePath(nsxResourcePath string) (VPCResourceInfo, error) {
	info := VPCResourceInfo{}
	reExp := regexp.MustCompile(`/orgs/([^/]+)/projects/([^/]+)/vpcs/([^/]+)([/\S+]*)`)
	matches := reExp.FindStringSubmatch(nsxResourcePath)
	if len(matches) != 5 {
		err := fmt.Errorf("invalid path '%s'", nsxResourcePath)
		return info, err
	}
	info.OrgID = matches[1]
	info.ProjectID = matches[2]
	info.VPCID = matches[3]
	layers := strings.Split(nsxResourcePath, "/")
	size := len(layers)
	info.ID = layers[size-1]
	info.ParentID = layers[size-3]
	return info, nil
}

// parse org id and project id from nsxProject path
// example /orgs/default/projects/nsx_operator_e2e_test
func NSXProjectPathToId(path string) (string, string, error) {
	parts := strings.Split(path, "/")
	if len(parts) < 5 {
		return "", "", errors.New("invalid NSX project path")
	}
	return parts[2], parts[4], nil
}

func IsDefaultNetworkConfigCR(vpcConfigCR *v1alpha1.VPCNetworkConfiguration) bool {
	annos := vpcConfigCR.GetAnnotations()
	val, exist := annos[AnnotationDefaultNetworkConfig]
	if exist {
		boolVar, err := strconv.ParseBool(val)
		if err != nil {
			log.Error(err, "Failed to parse annotation to check default NetworkConfig", "Annotation", annos[AnnotationDefaultNetworkConfig])
			return false
		}
		return boolVar
	}
	return false
}

// IsSharedSubnet checks if a Subnet is shared based on the associated-resource annotation
func IsSharedSubnet(subnet *v1alpha1.Subnet) bool {
	if subnet.Annotations == nil {
		return false
	}
	_, exists := subnet.Annotations[AnnotationAssociatedResource]
	return exists
}

// GetVPCFullID returns the formatted VPC full naIDme based on project and VPC IDs
// If the project is a default NSX project, the format is ":vpcId", otherwise it's "projectId:vpcId"
func GetVPCFullID(orgID, projectID, vpcID string, vpcService VPCServiceProvider) (string, error) {
	// Format VPC full ID
	vpcFullID := fmt.Sprintf("%s:%s", projectID, vpcID)
	isDefault, err := vpcService.IsDefaultNSXProject(orgID, projectID)
	if err != nil {
		return "", fmt.Errorf("failed to check if project is default: %w", err)
	}
	if isDefault {
		vpcFullID = fmt.Sprintf(":%s", vpcID)
	}

	return vpcFullID, nil
}

func GetSubnetPathFromAssociatedResource(associatedResource string) (string, error) {
	// associatedResource has the format projectID:vpcID:subnetID
	parts := strings.Split(associatedResource, ":")
	if len(parts) != 3 {
		return "", fmt.Errorf("failed to parse associated resource annotation %s", associatedResource)
	}
	return fmt.Sprintf("/orgs/%s/projects/%s/vpcs/%s/subnets/%s", orgId, parts[0], parts[1], parts[2]), nil
}

// ExtractSubnetPath extracts the org id, project id, VPC id, and subnet id from a subnet path
func ExtractSubnetPath(sharedSubnetPath string) (orgID, projectID, vpcID, subnetID string, err error) {
	// Format: /orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1
	vpcResourceInfo, err := ParseVPCResourcePath(sharedSubnetPath)
	if err != nil {
		return "", "", "", "", fmt.Errorf("invalid subnet path format: %s", sharedSubnetPath)
	}

	orgID = vpcResourceInfo.OrgID
	projectID = vpcResourceInfo.ProjectID
	vpcID = vpcResourceInfo.VPCID
	subnetID = vpcResourceInfo.ID
	return orgID, projectID, vpcID, subnetID, nil
}

// ConvertSubnetPathToAssociatedResource converts a subnet path to the associated resource format
// e.g., /orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1 -> proj-1:vpc-1:subnet-1
func ConvertSubnetPathToAssociatedResource(sharedSubnetPath string) (string, error) {
	// Extract the org id, project id, VPC id, and subnet id using ExtractSubnetPath
	_, projectID, vpcID, subnetID, err := ExtractSubnetPath(sharedSubnetPath)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s:%s:%s", projectID, vpcID, subnetID), nil
}

// BuildUniqueIDWithRandomUUID returns a string with format "obj.name_hash(uid)[UUIDHashLength]". If the returned
// string already exists, a random UUID is used to generate a hash suffix to replace "hash(uid)[UUIDHashLength]".
// nsx services should call this function to generate the NSX resource id by avoiding id collisions.
func BuildUniqueIDWithRandomUUID(initialObject metav1.Object, idGeneratorFn func(obj metav1.Object) string, idExistsFn func(id string) bool) string {
	resId := idGeneratorFn(initialObject)
	for idExistsFn(resId) {
		newObj := &metav1.ObjectMeta{
			Name: initialObject.GetName(),
			UID:  types.UID(uuid.New().String()),
		}
		resId = idGeneratorFn(newObj)
	}
	return resId
}

// BuildUniqueIDWithSuffix returns a string with format "obj.name-suffixStr_hash(uid)[UUIDHashLength]". If the returned
// string already exists, a random UUID is used to generate a hash suffix to replace "hash(uid)[UUIDHashLength]".
// nsx services should use this function to generate the NSX resource id if a "suffixStr" is expected.
func BuildUniqueIDWithSuffix(obj metav1.Object, suffixStr string, maxLength int, idGeneratorFn func(obj metav1.Object) string, idExistsFn func(id string) bool) string {
	maxNameLength := maxLength - UUIDHashLength - 1
	prefix := obj.GetName()
	suffix := ""
	if len(suffixStr) > 0 {
		maxNameLength = maxNameLength - (len(suffixStr) + 1)
		suffix = fmt.Sprintf("%s%s", ConnectorHyphen, suffixStr)
	}

	if len(prefix) > maxNameLength {
		prefix = prefix[:maxNameLength]
	}

	objectMeta := &metav1.ObjectMeta{
		Name: fmt.Sprintf("%s%s", prefix, suffix),
		UID:  obj.GetUID(),
	}
	return BuildUniqueIDWithRandomUUID(objectMeta, idGeneratorFn, idExistsFn)
}
