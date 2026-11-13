package subnet

import (
	"errors"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/utils/ptr"
)

// SharedSubnetRefreshInterval bounds the lifetime of shared subnet snapshots and the periodic refresh interval.
const SharedSubnetRefreshInterval = 10 * time.Minute

var errPortSettingsCacheInvalidated = errors.New("PortSettings cache invalidated during fetch")

// portSettingsCacheEntry holds the identity and default flag from every enumerated
// PortSetting, not its configuration. A zero fetchedAt marks an in-flight fetch token.
type portSettingsCacheEntry struct {
	settings  []model.VpcSubnetPortSetting
	fetchedAt time.Time
}

// clonePortSettingIdentities detaches both the slice and SDK pointers from callers.
func clonePortSettingIdentities(settings []model.VpcSubnetPortSetting) []model.VpcSubnetPortSetting {
	result := make([]model.VpcSubnetPortSetting, len(settings))
	for i, ps := range settings {
		if ps.Id != nil {
			result[i].Id = ptr.To(*ps.Id)
		}
		if ps.UniqueId != nil {
			result[i].UniqueId = ptr.To(*ps.UniqueId)
		}
		if ps.IsDefault != nil {
			result[i].IsDefault = ptr.To(*ps.IsDefault)
		}
	}
	return result
}

// GetPortSettingsFromCacheOrAPI returns a complete PortSettings identity snapshot.
// Empty snapshots are valid cache entries. Callers own the slice and its pointers.
func (service *SubnetService) GetPortSettingsFromCacheOrAPI(associatedResource string, forceAPI bool) ([]model.VpcSubnetPortSetting, error) {
	service.nsxSubnetCacheMutex.Lock()
	if !forceAPI {
		if entry := service.portSettingsCache[associatedResource]; entry != nil && !entry.fetchedAt.IsZero() && time.Since(entry.fetchedAt) < SharedSubnetRefreshInterval {
			settings := clonePortSettingIdentities(entry.settings)
			service.nsxSubnetCacheMutex.Unlock()
			return settings, nil
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
	settings := clonePortSettingIdentities(portSettings)

	service.nsxSubnetCacheMutex.Lock()
	defer service.nsxSubnetCacheMutex.Unlock()
	if service.portSettingsCache[associatedResource] != token {
		// The caller must not publish a snapshot invalidated while NSX I/O was in progress.
		return nil, errPortSettingsCacheInvalidated
	}
	token.settings = settings
	token.fetchedAt = time.Now()
	return clonePortSettingIdentities(settings), nil
}

// InvalidatePortSettingsCache discards the identity snapshot and invalidates any in-flight fetch for this source.
func (service *SubnetService) InvalidatePortSettingsCache(associatedResource string) {
	service.nsxSubnetCacheMutex.Lock()
	defer service.nsxSubnetCacheMutex.Unlock()
	delete(service.portSettingsCache, associatedResource)
}
