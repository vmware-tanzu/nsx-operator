/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"cmp"
	"hash/fnv"
	"slices"
	"sync"

	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
	extdnssrc "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/source"

	"k8s.io/apimachinery/pkg/types"
)

// gateway_ip_cache: per-Gateway listener targets for Route DNS and annotation-based Gateway DNS.

func sortAdmissionRowsForCompare(rows []extdnssrc.AdmissionHostCacheRow) []extdnssrc.AdmissionHostCacheRow {
	out := slices.Clone(rows)
	slices.SortFunc(out, func(a, b extdnssrc.AdmissionHostCacheRow) int {
		if a.FromListenerSet != b.FromListenerSet {
			if a.FromListenerSet {
				return 1
			}
			return -1
		}
		if c := cmp.Compare(a.ListenerSet.String(), b.ListenerSet.String()); c != 0 {
			return c
		}
		if c := cmp.Compare(string(a.Section), string(b.Section)); c != 0 {
			return c
		}
		return cmp.Compare(a.Filter, b.Filter)
	})
	return out
}

// gatewayDNSCacheEntryEqual compares entries for Route DNS (AdmissionRows order-insensitive).
func gatewayDNSCacheEntryEqual(a, b gatewayDNSCacheEntry) bool {
	if a.GatewayResource != b.GatewayResource {
		return false
	}
	ai := slices.Clone(a.IPs)
	bi := slices.Clone(b.IPs)
	slices.Sort(ai)
	slices.Sort(bi)
	if !slices.Equal(ai, bi) {
		return false
	}
	return slices.Equal(sortAdmissionRowsForCompare(a.AdmissionRows), sortAdmissionRowsForCompare(b.AdmissionRows))
}

// gatewayDNSCacheEntry holds resolved data for Route DNS reconcilers (IPs + structured listener admission).
type gatewayDNSCacheEntry struct {
	IPs             extdns.Targets
	AdmissionRows   []extdnssrc.AdmissionHostCacheRow
	GatewayResource types.NamespacedName
}

func listenerSetKeysFromAdmissionRows(rows []extdnssrc.AdmissionHostCacheRow) []types.NamespacedName {
	seen := make(map[string]struct{})
	var out []types.NamespacedName
	for i := range rows {
		row := rows[i]
		if !row.FromListenerSet {
			continue
		}
		k := row.ListenerSet.String()
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, row.ListenerSet)
	}
	slices.SortFunc(out, func(a, b types.NamespacedName) int {
		if c := cmp.Compare(a.Namespace, b.Namespace); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return out
}

func reconcileListenerSetRootIndexLocked(lsToRoot map[string]types.NamespacedName, gwNN types.NamespacedName, prev, next []types.NamespacedName, hadPrev bool) {
	nextKeep := make(map[string]struct{}, len(next))
	for _, ls := range next {
		nextKeep[ls.String()] = struct{}{}
	}
	if hadPrev {
		for _, ls := range prev {
			if _, still := nextKeep[ls.String()]; still {
				continue
			}
			if lsToRoot[ls.String()] == gwNN {
				delete(lsToRoot, ls.String())
			}
		}
	}
	for _, ls := range next {
		lsToRoot[ls.String()] = gwNN
	}
}

const gatewayIPCacheShards = 16

type gatewayIPCacheShard struct {
	mu sync.RWMutex
	m  map[string]gatewayDNSCacheEntry // key: types.NamespacedName.String()
}

// gatewayIPCache: sharded Gateway → IPs + admission rows; updated by Gateway reconcile for Route DNS.
type gatewayIPCache struct {
	shards     [gatewayIPCacheShards]gatewayIPCacheShard
	lsRootMu   sync.RWMutex
	lsToRootGw map[string]types.NamespacedName // ListenerSet NN string -> root Gateway NN
}

// NewGatewayIPCache returns an initialized cache (zero value is invalid).
func NewGatewayIPCache() *gatewayIPCache {
	return &gatewayIPCache{
		lsToRootGw: make(map[string]types.NamespacedName),
	}
}

func gatewayIPCacheShardIdx(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return h.Sum32() % gatewayIPCacheShards
}

func (c *gatewayIPCache) shardForKey(key string) *gatewayIPCacheShard {
	return &c.shards[gatewayIPCacheShardIdx(key)]
}

// put stores e for nn; returns true if entry changed. Updates lsToRootGw under lsRootMu + shard lock.
func (c *gatewayIPCache) put(nn types.NamespacedName, e gatewayDNSCacheEntry) bool {
	if c == nil {
		return false
	}
	c.lsRootMu.Lock()
	defer c.lsRootMu.Unlock()

	key := nn.String()
	s := c.shardForKey(key)
	s.mu.Lock()
	if s.m == nil {
		s.m = make(map[string]gatewayDNSCacheEntry)
	}
	prev, had := s.m[key]
	changed := !had || !gatewayDNSCacheEntryEqual(prev, e)
	prevKeys := listenerSetKeysFromAdmissionRows(prev.AdmissionRows)
	if !had {
		prevKeys = nil
	}
	nextKeys := listenerSetKeysFromAdmissionRows(e.AdmissionRows)
	s.m[key] = e
	reconcileListenerSetRootIndexLocked(c.lsToRootGw, nn, prevKeys, nextKeys, had)
	s.mu.Unlock()
	return changed
}

// delete removes nn; returns true if an entry existed.
func (c *gatewayIPCache) delete(nn types.NamespacedName) bool {
	if c == nil {
		return false
	}
	c.lsRootMu.Lock()
	defer c.lsRootMu.Unlock()

	key := nn.String()
	s := c.shardForKey(key)
	s.mu.Lock()
	prev, had := s.m[key]
	if !had {
		s.mu.Unlock()
		return false
	}
	prevKeys := listenerSetKeysFromAdmissionRows(prev.AdmissionRows)
	delete(s.m, key)
	reconcileListenerSetRootIndexLocked(c.lsToRootGw, nn, prevKeys, nil, true)
	s.mu.Unlock()
	return true
}

func (c *gatewayIPCache) get(nn types.NamespacedName) (gatewayDNSCacheEntry, bool) {
	if c == nil {
		return gatewayDNSCacheEntry{}, false
	}
	key := nn.String()
	s := c.shardForKey(key)
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.m[key]
	return e, ok
}

// lookupLSToRootGWLocked returns the root Gateway for lsNN from lsToRootGw. Caller must hold c.lsRootMu (RLock or Lock).
func (c *gatewayIPCache) lookupLSToRootGWLocked(lsNN types.NamespacedName) (types.NamespacedName, bool) {
	gw, ok := c.lsToRootGw[lsNN.String()]
	return gw, ok
}

// rootGatewayForCachedListenerSet returns (rootGw, true) when lsNN is indexed in lsToRootGw as attached to a cached Gateway.
func (c *gatewayIPCache) rootGatewayForCachedListenerSet(lsNN types.NamespacedName) (types.NamespacedName, bool) {
	if c == nil {
		return types.NamespacedName{}, false
	}
	c.lsRootMu.RLock()
	defer c.lsRootMu.RUnlock()
	return c.lookupLSToRootGWLocked(lsNN)
}

// listenerSetInLSToRootIndex reports whether lsNN is a key in lsToRootGw (ListenerSet attached to a Gateway
// that has a cache entry).
func (c *gatewayIPCache) listenerSetInLSToRootIndex(lsNN types.NamespacedName) bool {
	if c == nil {
		return false
	}
	c.lsRootMu.RLock()
	defer c.lsRootMu.RUnlock()
	_, ok := c.lookupLSToRootGWLocked(lsNN)
	return ok
}

// listGatewayNamespacedNames returns cached Gateway NNs (unordered snapshot).
func (c *gatewayIPCache) listGatewayNamespacedNames() []types.NamespacedName {
	if c == nil {
		return nil
	}
	var out []types.NamespacedName
	for i := range c.shards {
		s := &c.shards[i]
		s.mu.RLock()
		for _, e := range s.m {
			out = append(out, e.GatewayResource)
		}
		s.mu.RUnlock()
	}
	return out
}
