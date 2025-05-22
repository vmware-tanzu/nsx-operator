/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package common

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

func BuildUniqueID(idGeneratorFn func(reGenerate bool) string, idExistsFn func(id string) bool) string {
	resId := idGeneratorFn(false)
	for idExistsFn(resId) {
		resId = idGeneratorFn(true)
	}
	return resId
}

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
