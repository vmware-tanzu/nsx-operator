/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package vpcendpoint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestVPCEndpointKeyFunc(t *testing.T) {
	id := "vpcep-1"
	key, err := keyFunc(&model.VpcEndpoint{Id: &id})
	assert.NoError(t, err)
	assert.Equal(t, "vpcep-1", key)

	_, err = keyFunc(&model.VpcEndpoint{})
	assert.Error(t, err)

	_, err = keyFunc("not-a-vpc-endpoint")
	assert.Error(t, err)
}

func TestIndexByVPCEndpoint(t *testing.T) {
	scope := common.TagScopeVPCEndpointCRUID
	tag := "uid-1"
	obj := &model.VpcEndpoint{Tags: []model.Tag{{Scope: &scope, Tag: &tag}}}

	values, err := indexByVPCEndpoint(obj)
	assert.NoError(t, err)
	assert.Equal(t, []string{"uid-1"}, values)

	_, err = indexByVPCEndpoint("not-a-vpc-endpoint")
	assert.Error(t, err)
}

func TestVPCEndpointStoreApply(t *testing.T) {
	store := buildVPCEndpointStore()
	id := "vpcep-1"
	uid := "uid-1"
	scope := common.TagScopeVPCEndpointCRUID
	obj := &model.VpcEndpoint{Id: &id, Tags: []model.Tag{{Scope: &scope, Tag: &uid}}}

	assert.NoError(t, store.Apply(obj))
	assert.NotNil(t, store.GetByKey("vpcep-1"))

	markedForDelete := true
	obj.MarkedForDelete = &markedForDelete
	assert.NoError(t, store.Apply(obj))
	assert.Nil(t, store.GetByKey("vpcep-1"))
}
