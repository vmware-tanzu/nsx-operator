/* Copyright © 2024 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Code generated by lister-gen. DO NOT EDIT.

package v1alpha1

import (
	v1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

// IPBlocksInfoLister helps list IPBlocksInfos.
// All objects returned here must be treated as read-only.
type IPBlocksInfoLister interface {
	// List lists all IPBlocksInfos in the indexer.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1alpha1.IPBlocksInfo, err error)
	// IPBlocksInfos returns an object that can list and get IPBlocksInfos.
	IPBlocksInfos(namespace string) IPBlocksInfoNamespaceLister
	IPBlocksInfoListerExpansion
}

// iPBlocksInfoLister implements the IPBlocksInfoLister interface.
type iPBlocksInfoLister struct {
	indexer cache.Indexer
}

// NewIPBlocksInfoLister returns a new IPBlocksInfoLister.
func NewIPBlocksInfoLister(indexer cache.Indexer) IPBlocksInfoLister {
	return &iPBlocksInfoLister{indexer: indexer}
}

// List lists all IPBlocksInfos in the indexer.
func (s *iPBlocksInfoLister) List(selector labels.Selector) (ret []*v1alpha1.IPBlocksInfo, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha1.IPBlocksInfo))
	})
	return ret, err
}

// IPBlocksInfos returns an object that can list and get IPBlocksInfos.
func (s *iPBlocksInfoLister) IPBlocksInfos(namespace string) IPBlocksInfoNamespaceLister {
	return iPBlocksInfoNamespaceLister{indexer: s.indexer, namespace: namespace}
}

// IPBlocksInfoNamespaceLister helps list and get IPBlocksInfos.
// All objects returned here must be treated as read-only.
type IPBlocksInfoNamespaceLister interface {
	// List lists all IPBlocksInfos in the indexer for a given namespace.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1alpha1.IPBlocksInfo, err error)
	// Get retrieves the IPBlocksInfo from the indexer for a given namespace and name.
	// Objects returned here must be treated as read-only.
	Get(name string) (*v1alpha1.IPBlocksInfo, error)
	IPBlocksInfoNamespaceListerExpansion
}

// iPBlocksInfoNamespaceLister implements the IPBlocksInfoNamespaceLister
// interface.
type iPBlocksInfoNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
}

// List lists all IPBlocksInfos in the indexer for a given namespace.
func (s iPBlocksInfoNamespaceLister) List(selector labels.Selector) (ret []*v1alpha1.IPBlocksInfo, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha1.IPBlocksInfo))
	})
	return ret, err
}

// Get retrieves the IPBlocksInfo from the indexer for a given namespace and name.
func (s iPBlocksInfoNamespaceLister) Get(name string) (*v1alpha1.IPBlocksInfo, error) {
	obj, exists, err := s.indexer.GetByKey(s.namespace + "/" + name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1alpha1.Resource("ipblocksinfo"), name)
	}
	return obj.(*v1alpha1.IPBlocksInfo), nil
}