package v1alpha1

import (
	"encoding/json"
	"testing"
)

func TestIPAddressAllocationUsedForJSON(t *testing.T) {
	var allocation IPAddressAllocation
	if err := json.Unmarshal([]byte(`{"spec":{"usedFor":["LB_FRONTEND"]}}`), &allocation); err != nil {
		t.Fatal(err)
	}
	if len(allocation.Spec.UsedFor) != 1 || allocation.Spec.UsedFor[0] != UsedForLBFrontend {
		t.Fatalf("unexpected usedFor: %v", allocation.Spec.UsedFor)
	}
	encoded, err := json.Marshal(allocation.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"usedFor":["LB_FRONTEND"]}` {
		t.Fatalf("unexpected spec JSON: %s", encoded)
	}
	if err := json.Unmarshal([]byte(`{"spec":{"usedFor":"LB_FRONTEND"}}`), &allocation); err == nil {
		t.Fatal("scalar usedFor should be rejected")
	}
}

func TestIPAddressAllocationUsedForDeepCopy(t *testing.T) {
	original := &IPAddressAllocation{
		Spec: IPAddressAllocationSpec{UsedFor: []string{UsedForLBFrontend}},
	}
	copied := original.DeepCopy()
	copied.Spec.UsedFor[0] = "changed"
	if original.Spec.UsedFor[0] != UsedForLBFrontend {
		t.Fatal("deep copy aliases usedFor")
	}
	list := &IPAddressAllocationList{Items: []IPAddressAllocation{*original}}
	copiedList := list.DeepCopy()
	copiedList.Items[0].Spec.UsedFor[0] = "changed"
	if list.Items[0].Spec.UsedFor[0] != UsedForLBFrontend {
		t.Fatal("list deep copy aliases usedFor")
	}
}
