/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package serviceendpoint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestKeyFunc(t *testing.T) {
	id := "se-1"
	key, err := keyFunc(&model.VpcServiceEndpoint{Id: &id})
	assert.NoError(t, err)
	assert.Equal(t, "se-1", key)

	_, err = keyFunc(&model.VpcServiceEndpoint{})
	assert.Error(t, err)

	_, err = keyFunc("not-a-service-endpoint")
	assert.Error(t, err)
}

func TestIndexByServiceEndpoint(t *testing.T) {
	scope := common.TagScopeServiceEndpointCRUID
	tag := "uid-1"
	obj := &model.VpcServiceEndpoint{Tags: []model.Tag{{Scope: &scope, Tag: &tag}}}

	values, err := indexByServiceEndpoint(obj)
	assert.NoError(t, err)
	assert.Equal(t, []string{"uid-1"}, values)

	_, err = indexByServiceEndpoint("not-a-service-endpoint")
	assert.Error(t, err)
}

func TestServiceEndpointStoreApply(t *testing.T) {
	store := buildServiceEndpointStore()
	id := "se-1"
	uid := "uid-1"
	scope := common.TagScopeServiceEndpointCRUID
	obj := &model.VpcServiceEndpoint{Id: &id, Tags: []model.Tag{{Scope: &scope, Tag: &uid}}}

	assert.NoError(t, store.Apply(obj))
	assert.NotNil(t, store.GetByKey("se-1"))

	markedForDelete := true
	obj.MarkedForDelete = &markedForDelete
	assert.NoError(t, store.Apply(obj))
	assert.Nil(t, store.GetByKey("se-1"))
}
