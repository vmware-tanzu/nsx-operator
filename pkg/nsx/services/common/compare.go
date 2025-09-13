package common

import (
	"reflect"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data/serializers/cleanjson"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
)

var log = &logger.Log

type Comparable interface {
	Key() string
	Value() data.DataValue
}

func CompareResource(existing Comparable, expected Comparable) (isChanged bool) {
	//  avoid nil pointer
	if reflect.ValueOf(existing).IsNil() || reflect.ValueOf(expected).IsNil() {
		return true
	}
	dataValueToJSONEncoder := cleanjson.NewDataValueToJsonEncoder()
	s1, _ := dataValueToJSONEncoder.Encode(existing.Value())
	s2, _ := dataValueToJSONEncoder.Encode(expected.Value())
	if s1 != s2 {
		return true
	}
	return false
}

func CompareResources(existing []Comparable, expected []Comparable) (changed []Comparable, stale []Comparable) {
	// remove nil item in existing and expected
	for i := 0; i < len(existing); i++ {
		if reflect.ValueOf(existing[i]).IsNil() {
			existing = append(existing[:i], existing[i+1:]...)
			i--
		}
	}
	for i := 0; i < len(expected); i++ {
		if reflect.ValueOf(expected[i]).IsNil() {
			expected = append(expected[:i], expected[i+1:]...)
			i--
		}
	}

	stale = make([]Comparable, 0)
	changed = make([]Comparable, 0)

	expectedMap := make(map[string]Comparable)
	for _, expected_item := range expected {
		expectedMap[expected_item.Key()] = expected_item
	}
	existingMap := make(map[string]Comparable)
	for _, existed_item := range existing {
		existingMap[existed_item.Key()] = existed_item
	}

	for key, expected_item := range expectedMap {
		if existed_item, ok := existingMap[key]; ok {
			if isChanged := CompareResource(existed_item, expected_item); !isChanged {
				continue
			}
			log.V(1).Info("Resource changed", "existing", existed_item, "expected", expected_item)
		}
		changed = append(changed, expected_item)
	}
	for key, existed_item := range existingMap {
		if _, ok := expectedMap[key]; !ok {
			log.V(1).Info("Resource stale", "existing", existed_item)
			stale = append(stale, existed_item)
		}
	}
	log.V(1).Info("Resources differ", "stale", stale, "changed", changed)
	return changed, stale
}
