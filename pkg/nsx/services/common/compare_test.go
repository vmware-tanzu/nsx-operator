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

func (m mockComparable) Key() string {
	return m.key
}

func (m mockComparable) Value() data.DataValue {
	return m.value
}

func TestCompareResources(t *testing.T) {
	tests := []struct {
		name        string
		existing    []Comparable
		expected    []Comparable
		wantChanged []Comparable
		wantStale   []Comparable
	}{
		{
			name: "No changes",
			existing: []Comparable{
				mockComparable{key: "key1", value: data.NewStringValue("value1")},
				mockComparable{key: "key2", value: data.NewStringValue("value2")},
			},
			expected: []Comparable{
				mockComparable{key: "key1", value: data.NewStringValue("value1")},
				mockComparable{key: "key2", value: data.NewStringValue("value2")},
			},
			wantChanged: []Comparable{},
			wantStale:   []Comparable{},
		},
		{
			name: "Changed resources",
			existing: []Comparable{
				mockComparable{key: "key1", value: data.NewStringValue("value1")},
				mockComparable{key: "key2", value: data.NewStringValue("value2")},
			},
			expected: []Comparable{
				mockComparable{key: "key1", value: data.NewStringValue("value1")},
				mockComparable{key: "key2", value: data.NewStringValue("value2_changed")},
			},
			wantChanged: []Comparable{
				mockComparable{key: "key2", value: data.NewStringValue("value2_changed")},
			},
			wantStale: []Comparable{},
		},
		{
			name: "Stale resources",
			existing: []Comparable{
				mockComparable{key: "key1", value: data.NewStringValue("value1")},
				mockComparable{key: "key2", value: data.NewStringValue("value2")},
			},
			expected: []Comparable{
				mockComparable{key: "key1", value: data.NewStringValue("value1")},
			},
			wantChanged: []Comparable{},
			wantStale: []Comparable{
				mockComparable{key: "key2", value: data.NewStringValue("value2")},
			},
		},
		{
			name: "Changed and stale resources",
			existing: []Comparable{
				mockComparable{key: "key1", value: data.NewStringValue("value1")},
				mockComparable{key: "key2", value: data.NewStringValue("value2")},
			},
			expected: []Comparable{
				mockComparable{key: "key1", value: data.NewStringValue("value1_changed")},
			},
			wantChanged: []Comparable{
				mockComparable{key: "key1", value: data.NewStringValue("value1_changed")},
			},
			wantStale: []Comparable{
				mockComparable{key: "key2", value: data.NewStringValue("value2")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotChanged, gotStale := CompareResources(tt.existing, tt.expected)
			assert.Equal(t, tt.wantChanged, gotChanged)
			assert.Equal(t, tt.wantStale, gotStale)
		})
	}
}
