/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsxserviceaccount

import (
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	tagScopeCluster                 = common.TagScopeCluster
	tagScopeNamespace               = common.TagScopeNamespace
	tagScopeNSXServiceAccountCRName = common.TagScopeNSXServiceAccountCRName
	tagScopeNSXServiceAccountCRUID  = common.TagScopeNSXServiceAccountCRUID
)

func (s *NSXServiceAccountService) buildBasicTags(obj *v1alpha1.NSXServiceAccount) []model.Tag {
	uid := string(obj.UID)
	return []model.Tag{{
		Scope: &tagScopeCluster,
		Tag:   &s.NSXConfig.CoeConfig.Cluster,
	}, {
		Scope: &tagScopeNamespace,
		Tag:   &obj.Namespace,
	}, {
		Scope: &tagScopeNSXServiceAccountCRName,
		Tag:   &obj.Name,
	}, {
		Scope: &tagScopeNSXServiceAccountCRUID,
		Tag:   &uid,
	}}
}
