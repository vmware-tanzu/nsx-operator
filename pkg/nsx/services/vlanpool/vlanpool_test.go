package vlanpool

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/sets"
)

type fakeVlanCollector struct {
	used sets.Set[int]
	err  error
}

func (f *fakeVlanCollector) CollectUsedVlansOnParentSubnetsFromCache(_ []string, _ string) (sets.Set[int], error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.used, nil
}

func (f *fakeVlanCollector) CollectUsedVlansOnParentSubnetsFromNSX(_ []string, _ string) (sets.Set[int], error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.used, nil
}

func TestAllocate_PrefersSubnetVlan(t *testing.T) {
	svc := &Service{collector: &fakeVlanCollector{used: sets.New(100)}}
	vlan, err := svc.Allocate([]string{"/parent"}, "", 201, false)
	require.NoError(t, err)
	assert.Equal(t, int64(201), vlan)
}

func TestAllocate_FallbackOnConflict(t *testing.T) {
	svc := &Service{collector: &fakeVlanCollector{used: sets.New(201)}}
	vlan, err := svc.Allocate([]string{"/parent"}, "", 201, false)
	require.NoError(t, err)
	assert.Equal(t, int64(1), vlan)
}

func TestAllocate_PoolExhausted(t *testing.T) {
	used := sets.New[int]()
	for i := 1; i <= 4094; i++ {
		used.Insert(i)
	}
	svc := &Service{collector: &fakeVlanCollector{used: used}}
	_, err := svc.Allocate([]string{"/parent"}, "", -1, false)
	require.Error(t, err)
}

func TestValidateManualVlan_Conflict(t *testing.T) {
	svc := &Service{collector: &fakeVlanCollector{used: sets.New(50)}}
	err := svc.ValidateManualVlan([]string{"/parent"}, 50, "", false)
	require.Error(t, err)
}

func TestAllocate_ParallelPreferredDoesNotDuplicate(t *testing.T) {
	svc := &Service{collector: &fakeVlanCollector{used: sets.New[int]()}}
	parentPaths := []string{"/parent"}
	preferred := int64(201)

	var wg sync.WaitGroup
	results := make([]int64, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = svc.Allocate(parentPaths, "", preferred, false)
		}(i)
	}
	wg.Wait()

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	assert.NotEqual(t, results[0], results[1])
	assert.Subset(t, []int64{preferred, 1}, []int64{results[0], results[1]})
}

func TestAllocate_PendingBlocksManualValidation(t *testing.T) {
	svc := &Service{collector: &fakeVlanCollector{used: sets.New[int]()}}
	parentPaths := []string{"/parent"}

	vlan, err := svc.Allocate(parentPaths, "", 88, false)
	require.NoError(t, err)
	assert.Equal(t, int64(88), vlan)

	err = svc.ValidateManualVlan(parentPaths, 88, "", false)
	require.Error(t, err)
}

func TestReleasePending_AllowsReuse(t *testing.T) {
	svc := &Service{collector: &fakeVlanCollector{used: sets.New[int]()}}
	parentPaths := []string{"/parent"}

	vlan, err := svc.Allocate(parentPaths, "", 42, false)
	require.NoError(t, err)
	svc.ReleasePending(parentPaths, vlan)

	vlan2, err := svc.Allocate(parentPaths, "", 42, false)
	require.NoError(t, err)
	assert.Equal(t, vlan, vlan2)
}
