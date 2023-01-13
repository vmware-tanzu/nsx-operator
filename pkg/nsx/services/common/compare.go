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
	for _, e := range expected {
		expectedMap[e.Key()] = e
	}
	existingMap := make(map[string]Comparable)
	for _, e := range existing {
		existingMap[e.Key()] = e
	}

	for key, e := range expectedMap {
		if e2, ok := existingMap[key]; ok {
			if isChanged := CompareResource(e2, e); !isChanged {
				continue
			} else {
				log.V(1).Info("resource changed", "existing", e2, "expected", e)
			}
		}
		changed = append(changed, e)
	}
	for key, e := range existingMap {
		if _, ok := expectedMap[key]; !ok {
			log.V(1).Info("resource stale", "existing", e)
			stale = append(stale, e)
		}
	}
	log.V(1).Info("resources differ", "stale", stale, "changed", changed)
	return changed, stale
}
