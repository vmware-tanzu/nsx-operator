/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package fabric

import (
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// ResourceStore retains complete NSX model objects keyed by policy path.
// Each fabric resource service uses a store instantiated for its model type.
type ResourceStore[T any] struct {
	common.ResourceStore
}

func NewResourceStore[T any]() *ResourceStore[T] {
	return &ResourceStore[T]{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(fabricResourceKey, cache.Indexers{}),
	}}
}
func (s *ResourceStore[T]) Get(path string) (*T, bool) {
	value := s.ResourceStore.GetByKey(path)
	if value == nil {
		return nil, false
	}
	resource, ok := value.(*T)
	return resource, ok
}
func (s *ResourceStore[T]) Put(path string, v *T) error {
	if v == nil {
		return fmt.Errorf("cannot store a nil fabric resource")
	}
	resourcePath, err := fabricResourcePath(v)
	if err != nil {
		return err
	}
	if resourcePath != path {
		return fmt.Errorf("fabric resource path %q does not match store key %q", resourcePath, path)
	}
	return s.Add(v)
}
func (s *ResourceStore[T]) Delete(path string) {
	value := s.ResourceStore.GetByKey(path)
	if value == nil {
		return
	}
	_ = s.ResourceStore.Delete(value)
}
func (s *ResourceStore[T]) List() map[string]*T {
	items := s.ResourceStore.List()
	out := make(map[string]*T, len(items))
	for _, item := range items {
		path, err := fabricResourcePath(item)
		if err != nil {
			continue
		}
		if resource, ok := item.(*T); ok {
			out[path] = resource
		}
	}
	return out
}

func fabricResourceKey(obj interface{}) (string, error) {
	return fabricResourcePath(obj)
}

func fabricResourcePath(obj interface{}) (string, error) {
	var path *string
	switch resource := obj.(type) {
	case *model.TransitGateway:
		path = resource.Path
	case *model.IpAddressBlock:
		path = resource.Path
	case *model.VpcConnectivityProfile:
		path = resource.Path
	case *model.Vpc:
		path = resource.Path
	case *model.VpcAttachment:
		path = resource.Path
	default:
		return "", fmt.Errorf("unsupported fabric store resource %T", obj)
	}
	if path == nil || *path == "" {
		return "", fmt.Errorf("fabric resource %T has no policy path", obj)
	}
	return *path, nil
}

// Stores is shared by the fabric and SNAT services for one provider.
type Stores struct {
	TGWs        *ResourceStore[model.TransitGateway]
	Blocks      *ResourceStore[model.IpAddressBlock]
	Profiles    *ResourceStore[model.VpcConnectivityProfile]
	VPCs        *ResourceStore[model.Vpc]
	Attachments *ResourceStore[model.VpcAttachment]
}

func NewStores() *Stores {
	return &Stores{NewResourceStore[model.TransitGateway](), NewResourceStore[model.IpAddressBlock](), NewResourceStore[model.VpcConnectivityProfile](), NewResourceStore[model.Vpc](), NewResourceStore[model.VpcAttachment]()}
}
func replaceStore[T any](dst, src *ResourceStore[T]) error {
	for path := range dst.List() {
		dst.Delete(path)
	}
	for path, obj := range src.List() {
		if err := dst.Put(path, obj); err != nil {
			return err
		}
	}
	return nil
}
