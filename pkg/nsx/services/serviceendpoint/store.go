/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package serviceendpoint

import (
	"errors"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *model.VpcServiceEndpoint:
		if v == nil || v.Id == nil {
			return "", errors.New("VpcServiceEndpoint is nil or has nil Id")
		}
		return *v.Id, nil
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

func indexByServiceEndpoint(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch v := obj.(type) {
	case *model.VpcServiceEndpoint:
		return filterTag(v.Tags, common.TagScopeServiceEndpointCRUID), nil
	default:
		return res, errors.New("indexByServiceEndpoint doesn't support unknown type")
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

type ServiceEndpointStore struct {
	common.ResourceStore
}

func (serviceEndpointStore *ServiceEndpointStore) Apply(i interface{}) error {
	serviceEndpoint := i.(*model.VpcServiceEndpoint)
	if serviceEndpoint.MarkedForDelete != nil && *serviceEndpoint.MarkedForDelete {
		if err := serviceEndpointStore.Delete(serviceEndpoint); err != nil {
			return err
		}
		log.Debug("delete serviceEndpoint from store", "serviceEndpoint", serviceEndpoint)
	} else {
		if err := serviceEndpointStore.Add(serviceEndpoint); err != nil {
			return err
		}
		log.Debug("add serviceEndpoint to store", "serviceEndpoint", serviceEndpoint)
	}
	return nil
}

func (service *ServiceEndpointService) indexedServiceEndpoint(uid types.UID) (*model.VpcServiceEndpoint, error) {
	objs, err := service.ServiceEndpointStore.ResourceStore.ByIndex(common.TagScopeServiceEndpointCRUID, string(uid))
	if err != nil {
		return nil, err
	}
	if len(objs) == 0 {
		return nil, nil
	}
	return objs[0].(*model.VpcServiceEndpoint), nil
}

func buildServiceEndpointStore() *ServiceEndpointStore {
	return &ServiceEndpointStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			common.TagScopeServiceEndpointCRUID: indexByServiceEndpoint,
		}),
		BindingType: model.VpcServiceEndpointBindingType(),
	}}
}
