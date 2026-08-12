package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
)

type mockComparable struct {
	key   string
	value data.DataValue
}

func (m *mockComparable) Key() string {
	return m.key
}

func (m *mockComparable) Value() data.DataValue {
	return m.value
}

func mockComparableToComparable(mc []*mockComparable) []Comparable {
	res := make([]Comparable, 0, len(mc))
	for i := range mc {
		res = append(res, (*mockComparable)(mc[i]))
	}
	return res
}

func comparableToMockComparable(c []Comparable) []*mockComparable {
	res := make([]*mockComparable, 0, len(c))
	for i := range c {
		res = append(res, c[i].(*mockComparable))
	}
	return res
}

func TestCompareResources(t *testing.T) {
	tests := []struct {
		name        string
		existing    []*mockComparable
		expected    []*mockComparable
		wantChanged []*mockComparable
		wantStale   []*mockComparable
	}{
		{
			name: "No changes",
			existing: []*mockComparable{
				{key: "key1", value: data.NewStringValue("value1")},
				{key: "key2", value: data.NewStringValue("value2")},
			},
			expected: []*mockComparable{
				{key: "key1", value: data.NewStringValue("value1")},
				{key: "key2", value: data.NewStringValue("value2")},
			},
			wantChanged: []*mockComparable{},
			wantStale:   []*mockComparable{},
		},
		{
			name: "Changed resources",
			existing: []*mockComparable{
				{key: "key1", value: data.NewStringValue("value1")},
				{key: "key2", value: data.NewStringValue("value2")},
			},
			expected: []*mockComparable{
				{key: "key1", value: data.NewStringValue("value1")},
				{key: "key2", value: data.NewStringValue("value2_changed")},
			},
			wantChanged: []*mockComparable{
				{key: "key2", value: data.NewStringValue("value2_changed")},
			},
			wantStale: []*mockComparable{},
		},
		{
			name: "Stale resources",
			existing: []*mockComparable{
				{key: "key1", value: data.NewStringValue("value1")},
				{key: "key2", value: data.NewStringValue("value2")},
			},
			expected: []*mockComparable{
				{key: "key1", value: data.NewStringValue("value1")},
			},
			wantChanged: []*mockComparable{},
			wantStale: []*mockComparable{
				{key: "key2", value: data.NewStringValue("value2")},
			},
		},
		{
			name: "Changed and stale resources",
			existing: []*mockComparable{
				{key: "key1", value: data.NewStringValue("value1")},
				{key: "key2", value: data.NewStringValue("value2")},
			},
			expected: []*mockComparable{
				{key: "key1", value: data.NewStringValue("value1_changed")},
			},
			wantChanged: []*mockComparable{
				{key: "key1", value: data.NewStringValue("value1_changed")},
			},
			wantStale: []*mockComparable{
				{key: "key2", value: data.NewStringValue("value2")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotChanged, gotStale := CompareResources(mockComparableToComparable(tt.existing), mockComparableToComparable(tt.expected))
			assert.Equal(t, tt.wantChanged, comparableToMockComparable(gotChanged))
			assert.Equal(t, tt.wantStale, comparableToMockComparable(gotStale))
		})
	}
}
