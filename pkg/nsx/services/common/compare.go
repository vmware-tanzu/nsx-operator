package common

import (
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data/serializers/cleanjson"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
)

var log = logger.Log

type Comparable interface {
	Key() string
	Value() data.DataValue
}

func CompareResource(existing Comparable, expected Comparable) (isChanged bool) {
	var dataValueToJSONEncoder = cleanjson.NewDataValueToJsonEncoder()
	s1, _ := dataValueToJSONEncoder.Encode(existing.Value())
	s2, _ := dataValueToJSONEncoder.Encode(expected.Value())
	if s1 != s2 {
		return true
	}
	return false
}

func CompareResources(existing []Comparable, expected []Comparable) (changed []Comparable, stale []Comparable) {
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
			} else {
				log.V(1).Info("resource changed", "existing", existed_item, "expected", expected_item)
			}
		}
		changed = append(changed, expected_item)
	}
	for key, existed_item := range existingMap {
		if _, ok := expectedMap[key]; !ok {
			log.V(1).Info("resource stale", "existing", existed_item)
			stale = append(stale, existed_item)
		}
	}
	log.V(1).Info("resources differ", "stale", stale, "changed", changed)
	return changed, stale
}
