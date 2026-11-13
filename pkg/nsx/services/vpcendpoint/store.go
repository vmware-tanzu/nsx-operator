/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package vpcendpoint

import (
	"errors"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const indexNamespacedName = "indexNamespacedName"

func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *model.VpcEndpoint:
		if v == nil || v.Id == nil {
			return "", errors.New("VpcEndpoint is nil or has nil Id")
		}
		return *v.Id, nil
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

func indexByVPCEndpoint(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch v := obj.(type) {
	case *model.VpcEndpoint:
		return filterTag(v.Tags, common.TagScopeVPCEndpointCRUID), nil
	default:
		return res, errors.New("indexByVPCEndpoint doesn't support unknown type")
	}
}

func indexByNamespacedName(obj interface{}) ([]string, error) {
	switch v := obj.(type) {
	case *model.VpcEndpoint:
		var namespace, name string
		for _, tag := range v.Tags {
			switch *tag.Scope {
			case common.TagScopeNamespace:
				namespace = *tag.Tag
			case common.TagScopeVPCEndpointCRName:
				name = *tag.Tag
			}
		}
		if namespace == "" || name == "" {
			return []string{}, nil
		}
		return []string{types.NamespacedName{Namespace: namespace, Name: name}.String()}, nil
	default:
		return nil, errors.New("indexByNamespacedName doesn't support unknown type")
	}
}

var filterTag = func(v []model.Tag, scope string) []string {
	res := make([]string, 0, 5)
	for _, tag := range v {
		if *tag.Scope == scope {
			res = append(res, *tag.Tag)
		}
	}
	return res
}

type VPCEndpointStore struct {
	common.ResourceStore
}

func (vpcEndpointStore *VPCEndpointStore) Apply(i interface{}) error {
	vpcEndpoint := i.(*model.VpcEndpoint)
	if vpcEndpoint.MarkedForDelete != nil && *vpcEndpoint.MarkedForDelete {
		if err := vpcEndpointStore.Delete(vpcEndpoint); err != nil {
			return err
		}
		log.Debug("delete vpcEndpoint from store", "vpcEndpoint", vpcEndpoint)
	} else {
		if err := vpcEndpointStore.Add(vpcEndpoint); err != nil {
			return err
		}
		log.Debug("add vpcEndpoint to store", "vpcEndpoint", vpcEndpoint)
	}
	return nil
}

func (service *VPCEndpointService) indexedVPCEndpoint(uid types.UID) (*model.VpcEndpoint, error) {
	objs, err := service.VPCEndpointStore.ResourceStore.ByIndex(common.TagScopeVPCEndpointCRUID, string(uid))
	if err != nil {
		return nil, err
	}
	if len(objs) == 0 {
		return nil, nil
	}
	return objs[0].(*model.VpcEndpoint), nil
}

func buildVPCEndpointStore() *VPCEndpointStore {
	return &VPCEndpointStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			common.TagScopeVPCEndpointCRUID: indexByVPCEndpoint,
			indexNamespacedName:             indexByNamespacedName,
		}),
		BindingType: model.VpcEndpointBindingType(),
	}}
}
