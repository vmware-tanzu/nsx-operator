package subnet

import (
	"errors"
	"slices"
	"time"
)

// SharedSubnetRefreshInterval bounds the lifetime of shared subnet snapshots and the periodic refresh interval.
const SharedSubnetRefreshInterval = 10 * time.Minute

var errPortSettingsCacheInvalidated = errors.New("PortSettings cache invalidated during fetch")

// portSettingsCacheEntry holds a complete enumeration of non-empty PortSettings IDs,
// not the full SDK objects. A zero fetchedAt marks an in-flight fetch token.
type portSettingsCacheEntry struct {
	ids       []string
	fetchedAt time.Time
}

// GetPortSettingIDsFromCacheOrAPI returns a complete PortSettings ID snapshot.
// Empty snapshots are valid cache entries. Callers receive their own slice.
func (service *SubnetService) GetPortSettingIDsFromCacheOrAPI(associatedResource string, forceAPI bool) ([]string, error) {
	service.nsxSubnetCacheMutex.Lock()
	if !forceAPI {
		if entry := service.portSettingsCache[associatedResource]; entry != nil && !entry.fetchedAt.IsZero() && time.Since(entry.fetchedAt) < SharedSubnetRefreshInterval {
			ids := slices.Clone(entry.ids)
			service.nsxSubnetCacheMutex.Unlock()
			return ids, nil
		}
	}
	if service.portSettingsCache == nil {
		service.portSettingsCache = make(map[string]*portSettingsCacheEntry)
	}
	// Installing a fresh token also invalidates any earlier snapshot or fetch.
	token := &portSettingsCacheEntry{}
	service.portSettingsCache[associatedResource] = token
	service.nsxSubnetCacheMutex.Unlock()

	portSettings, err := service.ListPortSettingsByAssociatedResource(associatedResource)
	if err != nil {
		service.nsxSubnetCacheMutex.Lock()
		if service.portSettingsCache[associatedResource] == token {
			delete(service.portSettingsCache, associatedResource)
		}
		service.nsxSubnetCacheMutex.Unlock()
		return nil, err
	}
	ids := make([]string, 0, len(portSettings))
	for _, portSetting := range portSettings {
		if portSetting.Id != nil && *portSetting.Id != "" {
			ids = append(ids, *portSetting.Id)
		}
	}

	service.nsxSubnetCacheMutex.Lock()
	defer service.nsxSubnetCacheMutex.Unlock()
	if service.portSettingsCache[associatedResource] != token {
		// The caller must not publish a snapshot invalidated while NSX I/O was in progress.
		return nil, errPortSettingsCacheInvalidated
	}
	token.ids = ids
	token.fetchedAt = time.Now()
	return slices.Clone(ids), nil
}

// InvalidatePortSettingsCache discards the ID snapshot and invalidates any in-flight fetch for this source.
func (service *SubnetService) InvalidatePortSettingsCache(associatedResource string) {
	service.nsxSubnetCacheMutex.Lock()
	defer service.nsxSubnetCacheMutex.Unlock()
	delete(service.portSettingsCache, associatedResource)
}
