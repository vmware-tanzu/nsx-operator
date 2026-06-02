package vlanpool

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetbinding"
)

const (
	poolStart = int64(1)
	poolEnd   = int64(4094)
)

// VlanCollector collects VLAN IDs in use on parent Subnets from NSX.
type VlanCollector interface {
	CollectUsedVlansOnParentSubnetsFromCache(parentSubnetPaths []string, excludeCRUID string) (sets.Set[int], error)
	CollectUsedVlansOnParentSubnetsFromNSX(parentSubnetPaths []string, excludeCRUID string) (sets.Set[int], error)
}

type poolState struct {
	mu      sync.Mutex
	pending sets.Set[int]
}

// Service allocates VLAN IDs for SubnetConnectionBindingMap from the range [1, 4094].
type Service struct {
	collector  VlanCollector
	poolStates sync.Map // poolKey -> *poolState
}

func NewService(collector *subnetbinding.BindingService) *Service {
	return &Service{collector: collector}
}

func poolKey(parentSubnetPaths []string) string {
	paths := append([]string(nil), parentSubnetPaths...)
	sort.Strings(paths)
	return strings.Join(paths, "\x00")
}

func (s *Service) getPoolState(parentSubnetPaths []string) *poolState {
	key := poolKey(parentSubnetPaths)
	if state, ok := s.poolStates.Load(key); ok {
		return state.(*poolState)
	}
	state := &poolState{pending: sets.New[int]()}
	actual, _ := s.poolStates.LoadOrStore(key, state)
	return actual.(*poolState)
}

func (s *Service) collectUsed(parentSubnetPaths []string, excludeCRUID string, fromNSX bool) (sets.Set[int], error) {
	if fromNSX {
		return s.collector.CollectUsedVlansOnParentSubnetsFromNSX(parentSubnetPaths, excludeCRUID)
	}
	return s.collector.CollectUsedVlansOnParentSubnetsFromCache(parentSubnetPaths, excludeCRUID)
}

func cleanupCommittedPending(used, pending sets.Set[int]) {
	for vlan := range pending {
		if used.Has(vlan) {
			pending.Delete(vlan)
		}
	}
}

func unavailableVlans(used, pending sets.Set[int]) sets.Set[int] {
	unavailable := used.Clone()
	unavailable.Insert(pending.UnsortedList()...)
	return unavailable
}

// Allocate picks an available VLAN. When preferred is valid and unused on the target, it is returned;
// otherwise the smallest free ID from [1, 4094] is used.
// Pending allocations are tracked per parent Subnet path set so parallel reconciles do not pick the same VLAN.
func (s *Service) Allocate(parentSubnetPaths []string, excludeCRUID string, preferred int64, fromNSX bool) (int64, error) {
	state := s.getPoolState(parentSubnetPaths)
	state.mu.Lock()
	defer state.mu.Unlock()

	used, err := s.collectUsed(parentSubnetPaths, excludeCRUID, fromNSX)
	if err != nil {
		return 0, err
	}
	cleanupCommittedPending(used, state.pending)
	unavailable := unavailableVlans(used, state.pending)

	if preferred >= poolStart && preferred <= poolEnd && !unavailable.Has(int(preferred)) {
		state.pending.Insert(int(preferred))
		return preferred, nil
	}

	for id := poolStart; id <= poolEnd; id++ {
		if !unavailable.Has(int(id)) {
			state.pending.Insert(int(id))
			return id, nil
		}
	}
	return 0, fmt.Errorf("no available VLAN in pool [%d, %d] for target Subnet or SubnetSet", poolStart, poolEnd)
}

// CommitPending removes a VLAN from the in-flight allocation set after it is realized on NSX.
func (s *Service) CommitPending(parentSubnetPaths []string, vlan int64) {
	state := s.getPoolState(parentSubnetPaths)
	state.mu.Lock()
	defer state.mu.Unlock()
	state.pending.Delete(int(vlan))
}

// ReleasePending removes a VLAN from the in-flight allocation set when allocation did not complete.
func (s *Service) ReleasePending(parentSubnetPaths []string, vlan int64) {
	s.CommitPending(parentSubnetPaths, vlan)
}

// ValidateManualVlan checks that vlan is not already used on the parent Subnet paths.
func (s *Service) ValidateManualVlan(parentSubnetPaths []string, vlan int64, excludeCRUID string, fromNSX bool) error {
	state := s.getPoolState(parentSubnetPaths)
	state.mu.Lock()
	defer state.mu.Unlock()

	used, err := s.collectUsed(parentSubnetPaths, excludeCRUID, fromNSX)
	if err != nil {
		return err
	}
	cleanupCommittedPending(used, state.pending)
	if unavailableVlans(used, state.pending).Has(int(vlan)) {
		return fmt.Errorf("vlanTrafficTag %d is already used on target Subnet or SubnetSet", vlan)
	}
	return nil
}
