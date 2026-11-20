package subnet

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const portSettingsCacheSource = "project-1:vpc-1:subnet-1"

func newPortSettingsCacheService(list func(*string) (model.VpcSubnetPortSettingListResult, error)) *SubnetService {
	return &SubnetService{
		Service: common.Service{NSXClient: &nsx.Client{
			PortSettingsClient: &fakePortSettingsClient{
				list: func(_, _, _, _ string, cursor *string) (model.VpcSubnetPortSettingListResult, error) {
					return list(cursor)
				},
			},
		}},
		SharedSubnetData: SharedSubnetData{
			NSXSubnetCache: make(map[string]struct {
				Subnet     *model.VpcSubnet
				StatusList []model.VpcSubnetStatus
			}),
			SharedSubnetResourceMap: make(map[string]sets.Set[types.NamespacedName]),
		},
	}
}

func portSettingsListWithID(id string) model.VpcSubnetPortSettingListResult {
	return model.VpcSubnetPortSettingListResult{
		Results: []model.VpcSubnetPortSetting{{Id: common.String(id)}},
	}
}

func TestPortSettingsCacheSnapshotIsolationAndForce(t *testing.T) {
	calls := 0
	sdkID := "setting-original"
	service := newPortSettingsCacheService(func(_ *string) (model.VpcSubnetPortSettingListResult, error) {
		calls++
		return model.VpcSubnetPortSettingListResult{Results: []model.VpcSubnetPortSetting{
			{Id: &sdkID},
		}}, nil
	})
	require.Nil(t, service.portSettingsCache, "the identity cache is initialized lazily")

	settings, err := service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
	require.NoError(t, err)
	require.Equal(t, portSettingIdentitiesForTest("setting-original"), settings)
	*settings[0].Id = "caller-mutated"
	sdkID = "sdk-mutated"

	for range 3 {
		settings, err = service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
		require.NoError(t, err)
		require.Equal(t, portSettingIdentitiesForTest("setting-original"), settings)
		*settings[0].Id = "cache-hit-mutated"
	}
	assert.Equal(t, 1, calls)

	settings, err = service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, true)
	require.NoError(t, err)
	require.Equal(t, portSettingIdentitiesForTest("sdk-mutated"), settings)
	assert.Equal(t, 2, calls)
	*settings[0].Id = "forced-result-mutated"
	settings, err = service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
	require.NoError(t, err)
	assert.Equal(t, portSettingIdentitiesForTest("sdk-mutated"), settings)
	assert.Equal(t, 2, calls)
}

func TestPortSettingsCacheEmptySnapshot(t *testing.T) {
	calls := 0
	service := newPortSettingsCacheService(func(_ *string) (model.VpcSubnetPortSettingListResult, error) {
		calls++
		return model.VpcSubnetPortSettingListResult{}, nil
	})
	for range 3 {
		settings, err := service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
		require.NoError(t, err)
		assert.Empty(t, settings)
	}
	assert.Equal(t, 1, calls, "an empty successful enumeration is a cache hit")
}

func TestPortSettingsCacheExpiry(t *testing.T) {
	calls := 0
	service := newPortSettingsCacheService(func(_ *string) (model.VpcSubnetPortSettingListResult, error) {
		calls++
		return portSettingsListWithID(fmt.Sprintf("setting-%d", calls)), nil
	})
	_, err := service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
	require.NoError(t, err)
	service.nsxSubnetCacheMutex.Lock()
	service.portSettingsCache[portSettingsCacheSource].fetchedAt = time.Now().Add(-SharedSubnetRefreshInterval)
	service.nsxSubnetCacheMutex.Unlock()

	beforeFetch := time.Now()
	settings, err := service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
	require.NoError(t, err)
	assert.Equal(t, portSettingIdentitiesForTest("setting-2"), settings)
	assert.Equal(t, 2, calls)
	assert.False(t, service.portSettingsCache[portSettingsCacheSource].fetchedAt.Before(beforeFetch))
	_, err = service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
}

func TestPortSettingsCacheForceFailureDiscardsSnapshot(t *testing.T) {
	calls := 0
	listErr := errors.New("list failed")
	service := newPortSettingsCacheService(func(_ *string) (model.VpcSubnetPortSettingListResult, error) {
		calls++
		if calls == 2 {
			return model.VpcSubnetPortSettingListResult{}, listErr
		}
		return portSettingsListWithID(fmt.Sprintf("setting-%d", calls)), nil
	})
	_, err := service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
	require.NoError(t, err)
	settings, err := service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, true)
	require.ErrorIs(t, err, listErr)
	assert.Same(t, listErr, err, "the original List error must be preserved")
	assert.Nil(t, settings)
	assert.Empty(t, service.portSettingsCache)

	settings, err = service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
	require.NoError(t, err)
	assert.Equal(t, portSettingIdentitiesForTest("setting-3"), settings)
	assert.Equal(t, 3, calls)
}

func TestPortSettingsCachePaginationFailureIsNotCached(t *testing.T) {
	calls := 0
	listErr := errors.New("second page failed")
	service := newPortSettingsCacheService(func(cursor *string) (model.VpcSubnetPortSettingListResult, error) {
		calls++
		switch calls {
		case 1:
			assert.Nil(t, cursor)
			result := portSettingsListWithID("partial-setting")
			result.Cursor = common.String("next-page")
			return result, nil
		case 2:
			require.NotNil(t, cursor)
			assert.Equal(t, "next-page", *cursor)
			return model.VpcSubnetPortSettingListResult{}, listErr
		default:
			assert.Nil(t, cursor, "a retry starts from the first page")
			return portSettingsListWithID("complete-setting"), nil
		}
	})
	settings, err := service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
	require.ErrorIs(t, err, listErr)
	assert.Nil(t, settings)
	assert.Empty(t, service.GetNSXSubnetCacheKeys())

	settings, err = service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
	require.NoError(t, err)
	assert.Equal(t, portSettingIdentitiesForTest("complete-setting"), settings)
	assert.Equal(t, 3, calls)
}

func TestPortSettingsCacheCompletePagination(t *testing.T) {
	calls := 0
	service := newPortSettingsCacheService(func(cursor *string) (model.VpcSubnetPortSettingListResult, error) {
		calls++
		if cursor == nil {
			result := portSettingsListWithID("first-setting")
			result.Cursor = common.String("last-page")
			return result, nil
		}
		assert.Equal(t, "last-page", *cursor)
		return portSettingsListWithID("last-setting"), nil
	})
	for range 2 {
		settings, err := service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
		require.NoError(t, err)
		assert.Equal(t, portSettingIdentitiesForTest("first-setting", "last-setting"), settings)
	}
	assert.Equal(t, 2, calls, "all pages are read once and the complete enumeration is cached")
}

func TestPortSettingsCacheInvalidSource(t *testing.T) {
	service := newPortSettingsCacheService(func(_ *string) (model.VpcSubnetPortSettingListResult, error) {
		t.Fatal("an invalid source must not reach the NSX API")
		return model.VpcSubnetPortSettingListResult{}, nil
	})
	settings, err := service.GetPortSettingsFromCacheOrAPI("invalid-source", false)
	require.Error(t, err)
	assert.Nil(t, settings)
	assert.Empty(t, service.portSettingsCache)
}

func TestPortSettingsCacheLifecycle(t *testing.T) {
	calls := 0
	service := newPortSettingsCacheService(func(_ *string) (model.VpcSubnetPortSettingListResult, error) {
		calls++
		return portSettingsListWithID("setting-1"), nil
	})
	_, err := service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
	require.NoError(t, err)
	entry := service.portSettingsCache[portSettingsCacheSource]
	service.UpdateNSXSubnetCache(portSettingsCacheSource, &model.VpcSubnet{}, nil)
	assert.Same(t, entry, service.portSettingsCache[portSettingsCacheSource])
	_, err = service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
	require.NoError(t, err)
	assert.Equal(t, 1, calls, "updating the parent subnet preserves the PortSettings snapshot")

	service.InvalidatePortSettingsCache(portSettingsCacheSource)
	assert.Contains(t, service.NSXSubnetCache, portSettingsCacheSource)
	assert.Empty(t, service.portSettingsCache)
	_, err = service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
	require.NoError(t, err)
	service.RemoveSubnetFromCache(portSettingsCacheSource, "deleted")
	assert.Empty(t, service.NSXSubnetCache)
	assert.Empty(t, service.portSettingsCache)
	assert.Empty(t, service.GetNSXSubnetCacheKeys())

	// Exercise unshared cleanup of a source with only PortSettings cached.
	_, err = service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
	require.NoError(t, err)
	sharedName := types.NamespacedName{Namespace: "test-namespace", Name: "shared-subnet"}
	service.AddSharedSubnetToResourceMap(portSettingsCacheSource, sharedName)
	assert.False(t, service.RemoveSubnetFromCacheIfUnshared(portSettingsCacheSource))
	assert.Contains(t, service.portSettingsCache, portSettingsCacheSource)
	service.UpdateNSXSubnetCache(portSettingsCacheSource, &model.VpcSubnet{}, nil)
	service.RemoveSharedSubnetFromResourceMap(portSettingsCacheSource, sharedName)
	assert.True(t, service.RemoveSubnetFromCacheIfUnshared(portSettingsCacheSource))
	assert.Empty(t, service.NSXSubnetCache)
	assert.Empty(t, service.portSettingsCache)
	assert.False(t, service.RemoveSubnetFromCacheIfUnshared(portSettingsCacheSource))

	_, err = service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
	require.NoError(t, err)
	assert.Empty(t, service.NSXSubnetCache)
	assert.True(t, service.RemoveSubnetFromCacheIfUnshared(portSettingsCacheSource))
	assert.Empty(t, service.portSettingsCache)
}

func TestPortSettingsCacheKeyUnion(t *testing.T) {
	service := newPortSettingsCacheService(func(_ *string) (model.VpcSubnetPortSettingListResult, error) {
		return portSettingsListWithID("setting-1"), nil
	})
	parentOnlySource := "project-1:vpc-1:parent-only"
	portOnlySource := "project-1:vpc-1:port-only"
	service.UpdateNSXSubnetCache(parentOnlySource, &model.VpcSubnet{}, nil)
	service.UpdateNSXSubnetCache(portSettingsCacheSource, &model.VpcSubnet{}, nil)
	for _, source := range []string{portOnlySource, portSettingsCacheSource} {
		_, err := service.GetPortSettingsFromCacheOrAPI(source, false)
		require.NoError(t, err)
	}
	keys := service.GetNSXSubnetCacheKeys()
	assert.ElementsMatch(t, []string{parentOnlySource, portOnlySource, portSettingsCacheSource}, keys)
	keys[0] = "caller-mutated"
	assert.ElementsMatch(t, []string{parentOnlySource, portOnlySource, portSettingsCacheSource}, service.GetNSXSubnetCacheKeys())
	service.RemoveSubnetFromCache(portOnlySource, "deleted")
	assert.NotContains(t, service.portSettingsCache, portOnlySource)
}

type portSettingsFetchResult struct {
	settings []model.VpcSubnetPortSetting
	err      error
}

func TestPortSettingsCacheInvalidatesInFlightFetch(t *testing.T) {
	for _, action := range []string{"invalidate", "remove", "remove-unshared"} {
		t.Run(action, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			releaseFetch := sync.OnceFunc(func() { close(release) })
			defer releaseFetch()
			service := newPortSettingsCacheService(func(_ *string) (model.VpcSubnetPortSettingListResult, error) {
				close(started)
				<-release
				return portSettingsListWithID("stale-setting"), nil
			})
			resultCh := make(chan portSettingsFetchResult, 1)
			go func() {
				settings, err := service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
				resultCh <- portSettingsFetchResult{settings: settings, err: err}
			}()
			<-started
			keysWhileFetching := service.GetNSXSubnetCacheKeys()
			removed := false
			switch action {
			case "invalidate":
				service.InvalidatePortSettingsCache(portSettingsCacheSource)
			case "remove":
				service.RemoveSubnetFromCache(portSettingsCacheSource, "deleted")
			case "remove-unshared":
				removed = service.RemoveSubnetFromCacheIfUnshared(portSettingsCacheSource)
			}
			releaseFetch()
			result := <-resultCh
			assert.Equal(t, []string{portSettingsCacheSource}, keysWhileFetching, "in-flight port-only sources must be visible to GC")
			if action == "remove-unshared" {
				assert.True(t, removed)
			}
			require.ErrorIs(t, result.err, errPortSettingsCacheInvalidated)
			assert.Nil(t, result.settings, "an invalidated response must not reach a CR writer")
			assert.Empty(t, service.portSettingsCache)
			assert.Empty(t, service.GetNSXSubnetCacheKeys())
		})
	}
}

func TestPortSettingsCacheSupersededFetchPreservesNewSnapshot(t *testing.T) {
	oldListErr := errors.New("superseded list failed")
	for _, oldErr := range []error{nil, oldListErr} {
		name := "success"
		if oldErr != nil {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			releaseFetch := sync.OnceFunc(func() { close(release) })
			defer releaseFetch()
			var calls atomic.Int32
			service := newPortSettingsCacheService(func(_ *string) (model.VpcSubnetPortSettingListResult, error) {
				if calls.Add(1) == 1 {
					close(started)
					<-release
					return portSettingsListWithID("old-setting"), oldErr
				}
				return portSettingsListWithID("new-setting"), nil
			})
			resultCh := make(chan portSettingsFetchResult, 1)
			go func() {
				settings, err := service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
				resultCh <- portSettingsFetchResult{settings: settings, err: err}
			}()
			<-started
			settings, err := service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, true)
			releaseFetch()
			oldResult := <-resultCh
			require.NoError(t, err)
			assert.Equal(t, portSettingIdentitiesForTest("new-setting"), settings)
			if oldErr == nil {
				require.ErrorIs(t, oldResult.err, errPortSettingsCacheInvalidated)
			} else {
				require.ErrorIs(t, oldResult.err, oldErr)
				assert.Same(t, oldErr, oldResult.err)
			}
			assert.Nil(t, oldResult.settings)
			settings, err = service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
			require.NoError(t, err)
			assert.Equal(t, portSettingIdentitiesForTest("new-setting"), settings)
			assert.EqualValues(t, 2, calls.Load(), "the obsolete fetch must neither replace nor delete the new snapshot")
		})
	}
}

func portSettingIdentitiesForTest(ids ...string) []model.VpcSubnetPortSetting {
	result := make([]model.VpcSubnetPortSetting, len(ids))
	for i, id := range ids {
		result[i].Id = common.String(id)
	}
	return result
}

func TestPortSettingsCachePreservesNamingIdentityAcrossPages(t *testing.T) {
	id, uniqueID, isDefault := "Invalid_ID", "stable-unique-id", false
	calls := 0
	service := newPortSettingsCacheService(func(cursor *string) (model.VpcSubnetPortSettingListResult, error) {
		calls++
		if cursor == nil {
			return model.VpcSubnetPortSettingListResult{
				Results: []model.VpcSubnetPortSetting{{Id: &id, UniqueId: &uniqueID, IsDefault: &isDefault}},
				Cursor:  common.String("next"),
			}, nil
		}
		return model.VpcSubnetPortSettingListResult{Results: []model.VpcSubnetPortSetting{
			{Id: common.String("default-setting"), IsDefault: common.Bool(true)},
			{}, // Missing identities must reach reconciliation, not silently disappear.
		}}, nil
	})
	settings, err := service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
	require.NoError(t, err)
	require.Len(t, settings, 3)
	*settings[0].Id, *settings[0].UniqueId, *settings[0].IsDefault = "caller", "caller-uid", true
	*settings[1].IsDefault = false
	id, uniqueID, isDefault = "sdk", "sdk-uid", true
	cached, err := service.GetPortSettingsFromCacheOrAPI(portSettingsCacheSource, false)
	require.NoError(t, err)
	require.Len(t, cached, 3)
	assert.Equal(t, "Invalid_ID", *cached[0].Id)
	assert.Equal(t, "stable-unique-id", *cached[0].UniqueId)
	assert.False(t, *cached[0].IsDefault)
	assert.True(t, *cached[1].IsDefault)
	assert.Nil(t, cached[2].Id)
	assert.Equal(t, 2, calls)
}
