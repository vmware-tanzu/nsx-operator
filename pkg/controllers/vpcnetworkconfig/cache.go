/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package vpcnetworkconfig

import (
	"sync"

	"k8s.io/client-go/tools/cache"

	v1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
)

const (
	// cached indexer
	networkConfigIndexerByNamespace = "vpcnetworkconfig.namespace"
)

type VPCNetworkConfigInfoCache struct {
	// Mutex to protect CNIConfigInfo.PodCNIDeleted which is written by CNIServer, and read by
	// the secondary network Pod controller.
	sync.RWMutex
	cache cache.Indexer
}

// Add CNIPodInfo to local cache store.
func (c *VPCNetworkConfigInfoCache) AddVPCNetworkConfigInfo(networkConfig *VPCNetworkConfigInfo) {
	c.RLock()
	defer c.RUnlock()
	c.cache.Add(networkConfig)
}

// Delete CNIPodInfo from local cache store.
func (c *VPCNetworkConfigInfoCache) DeleteVPCNetworkConfigInfo(networkConfig *VPCNetworkConfigInfo) {
	c.RLock()
	defer c.RUnlock()
	c.cache.Delete(networkConfig)
}

// Retrieve a VPCNetworkConfigInfo cache entry for the given namespace.
func (c *VPCNetworkConfigInfoCache) GetVPCNetworkConfigInfoPerNamespace(namespace string) *VPCNetworkConfigInfo {
	networkConfigObjs, _ := c.cache.ByIndex(networkConfigIndexerByNamespace, namespace)
	c.RLock()
	defer c.RUnlock()
	for i := range networkConfigObjs {
		var networkConfig *VPCNetworkConfigInfo
		networkConfig = networkConfigObjs[i].(*VPCNetworkConfigInfo)
		return networkConfig
	}
	return nil
}

// Retrieve a VPCNetworkConfigInfo cache entry for the given namespace.
func (c *VPCNetworkConfigInfoCache) GetByKey(name string) (*VPCNetworkConfigInfo, bool) {
	networkConfigObj, found, _ := c.cache.GetByKey(name)
	c.RLock()
	defer c.RUnlock()
	if !found {
		return nil, false
	}
	return networkConfigObj.(*VPCNetworkConfigInfo), found
}

func networkConfigIndexerByNamespaceFunc(obj interface{}) ([]string, error) {
	networkConfig := obj.(v1alpha1.VPCNetworkConfiguration)
	return []string{networkConfig.Spec.AppliedToNamespaces[0]}, nil
}

func vpcNetworkConfigIndexerKeyFunc(obj interface{}) (string, error) {
	networkConfig := obj.(v1alpha1.VPCNetworkConfiguration)
	return networkConfig.Name, nil
}

func NewVPCNetworkConfigInfoStore() *VPCNetworkConfigInfoCache {
	return &VPCNetworkConfigInfoCache{
		cache: cache.NewIndexer(vpcNetworkConfigIndexerKeyFunc, cache.Indexers{
			networkConfigIndexerByNamespace: networkConfigIndexerByNamespaceFunc,
		}),
	}
}
