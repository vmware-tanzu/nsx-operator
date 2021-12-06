/*
Copyright 2014 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
/*
// Copyright (c) 2021 VMware, Inc. All rights reserved. VMware Confidential.
This file is copied from https://github.com/kubernetes/client-go/blob/master/tools/cache/thread_safe_store.go
Modifies:
- Removed original ThreadSafeStore interface
- Added NSXStore interface
- Renamed original threadSafeMap struct to threadSafeStore and implements NSXStore interface
- Added 'synced' parameter to threadSafeStore struct
- Removed Add, Replace, Index, IndexKeys, ListIndexFuncValues, GetIndexers functions
- Added Filter function for filtering objects by multiple indexes
- Added Synced function to return the current sync status of the store
- Added DoneSynced function to mark the store as synced
*/

package store

import (
	"fmt"
	"sync"

	mapset "github.com/deckarep/golang-set"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
)

// The NSXStore is a thread safe key-value cache, supports basic
// GET/UPDATE/DELETE/LIST method as well as FILTER by multiple indexes
type NSXStore interface {
	// Get returns the object associated with the primary key
	Get(key string) (item interface{}, exists bool)
	// Update updates or inserts the given object's payload in the cache associated with the key
	Update(key string, obj interface{})
	// Delete removes the object
	Delete(key string)
	// List returns a list of objects in the cache
	List() []interface{}
	// ListKeys returns a list of primary keys associated with objects in the cache
	ListKeys() []string
	// Filter returns a list of objects matching all the given indexes pair
	Filter(keysAndValues map[string]string) ([]interface{}, error)
	// Synced returns a boolean value indicates that the store has been populated
	Synced() bool
	// DoneSynced is used to set the flag when the store is populated for the first time
	DoneSynced()
}

// threadSafeStore implements NSXStore
type threadSafeStore struct {
	lock  sync.RWMutex
	items map[string]interface{}

	// indexers maps a name to an IndexFunc
	indexers cache.Indexers
	// indices maps a name to an Index
	indices cache.Indices

	// flag indicates that the store has been synced at least once
	synced bool
}

func (s *threadSafeStore) Update(key string, obj interface{}) {
	s.lock.Lock()
	defer s.lock.Unlock()
	oldObject := s.items[key]
	s.items[key] = obj
	s.updateIndices(oldObject, obj, key)
}

func (s *threadSafeStore) Delete(key string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if obj, exists := s.items[key]; exists {
		s.deleteFromIndices(obj, key)
		delete(s.items, key)
	}
}

func (s *threadSafeStore) Get(key string) (item interface{}, exists bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	item, exists = s.items[key]
	return item, exists
}

func (s *threadSafeStore) List() []interface{} {
	s.lock.RLock()
	defer s.lock.RUnlock()
	list := make([]interface{}, 0, len(s.items))
	for _, item := range s.items {
		list = append(list, item)
	}
	return list
}

func (s *threadSafeStore) ListKeys() []string {
	s.lock.RLock()
	defer s.lock.RUnlock()
	list := make([]string, 0, len(s.items))
	for key := range s.items {
		list = append(list, key)
	}
	return list
}

// updateIndices modifies the objects location in the managed indexes, if this is an update, you must provide an oldObj
// updateIndices must be called from a function that already has a lock on the cache
func (s *threadSafeStore) updateIndices(oldObj interface{}, newObj interface{}, key string) {
	// if we got an old object, we need to remove it before we add it again
	if oldObj != nil {
		s.deleteFromIndices(oldObj, key)
	}
	for name, indexFunc := range s.indexers {
		indexValues, err := indexFunc(newObj)
		if err != nil {
			panic(fmt.Errorf("unable to calculate an index entry for key %q on index %q: %v", key, name, err))
		}
		index := s.indices[name]
		if index == nil {
			index = cache.Index{}
			s.indices[name] = index
		}

		for _, indexValue := range indexValues {
			set := index[indexValue]
			if set == nil {
				set = sets.String{}
				index[indexValue] = set
			}
			set.Insert(key)
		}
	}
}

// deleteFromIndices removes the object from each of the managed indexes
// it is intended to be called from a function that already has a lock on the cache
func (s *threadSafeStore) deleteFromIndices(obj interface{}, key string) {
	for name, indexFunc := range s.indexers {
		indexValues, err := indexFunc(obj)
		if err != nil {
			panic(fmt.Errorf("unable to calculate an index entry for key %q on index %q: %v", key, name, err))
		}

		index := s.indices[name]
		if index == nil {
			continue
		}
		for _, indexValue := range indexValues {
			set := index[indexValue]
			if set != nil {
				set.Delete(key)

				// If we don't delete the set when zero, indices with high cardinality
				// short lived resources can cause memory to increase over time from
				// unused empty sets. See `kubernetes/kubernetes/issues/84959`.
				if len(set) == 0 {
					delete(index, indexValue)
				}
			}
		}
	}
}

// byIndex is a helper function returns a list of the items whose indexed values in the given index include the
// given indexed value
func (s *threadSafeStore) byIndex(indexName, indexedValue string) ([]interface{}, error) {
	indexFunc := s.indexers[indexName]
	if indexFunc == nil {
		return nil, fmt.Errorf("Index with name %s does not exist", indexName)
	}

	index := s.indices[indexName]

	set := index[indexedValue]
	list := make([]interface{}, 0, set.Len())
	for key := range set {
		list = append(list, s.items[key])
	}

	return list, nil
}

// Filter function returns a list of object matching the set of indexes
// The method is write-safe, all simultaneous writes and read will be blocked
func (s *threadSafeStore) Filter(keysAndValues map[string]string) ([]interface{}, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	var objects mapset.Set
	for indexName, indexValue := range keysAndValues {
		items, err := s.byIndex(indexName, indexValue)
		// Directly return empty slice if failed by single index filter, or single index filter return empty slice
		if err != nil || len(items) == 0 {
			return []interface{}{}, err
		}
		objs := mapset.NewSetFromSlice(items)
		if objects == nil {
			objects = objs
		} else {
			objects = objects.Intersect(objs)
			// Quick break if the length of the objects becomes zero
			if objects.Cardinality() == 0 {
				break
			}
		}
	}
	return objects.ToSlice(), nil
}

func (s *threadSafeStore) DoneSynced() {
	s.synced = true
}

func (s *threadSafeStore) Synced() bool {
	return s.synced
}

// NewStore creates a new instance of NSXStore.
func NewStore(indexers cache.Indexers) NSXStore {
	return &threadSafeStore{
		items:    map[string]interface{}{},
		indexers: indexers,
		indices:  cache.Indices{},
	}
}
