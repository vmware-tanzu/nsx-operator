/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestNetworkResourceTransitionRollbackDeepCopy(t *testing.T) {
	original := &NetworkResourceTransition{
		Spec: NetworkResourceTransitionSpec{SubnetPortReverts: []SubnetPortRevertSpec{{
			SubnetPort:          NamespacedName{Namespace: "tenant", Name: "port"},
			SubnetIPReservation: NamespacedName{Namespace: "destination", Name: "reservation"},
			ReservedIPs:         []string{"192.0.2.10", "2001:db8::10"},
		}}},
		Status: NetworkResourceTransitionStatus{SubnetPortReverts: []SubnetPortRevertStatus{{SubnetIPReservation: &NamespacedName{Namespace: "destination", Name: "reservation"}, ReservedIPs: []string{"192.0.2.10", "2001:db8::10"}}}},
	}
	copied := original.DeepCopy()
	copied.Status.SubnetPortReverts[0].SubnetIPReservation.Name = "changed"
	if original.Status.SubnetPortReverts[0].SubnetIPReservation.Name != "reservation" {
		t.Fatal("deep copy aliases destination reference")
	}
	copied.Spec.SubnetPortReverts[0].ReservedIPs[0] = "192.0.2.20"
	copied.Status.SubnetPortReverts[0].ReservedIPs[0] = "192.0.2.30"
	if original.Spec.SubnetPortReverts[0].ReservedIPs[0] != "192.0.2.10" || original.Status.SubnetPortReverts[0].ReservedIPs[0] != "192.0.2.10" {
		t.Fatal("deep copy aliases rollback reservation addresses")
	}
}

func TestNamespacedNameJSON(t *testing.T) {
	data, err := json.Marshal(NamespacedName{Namespace: "source", Name: "reservation"})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]string
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields["namespace"] != "source" || fields["name"] != "reservation" {
		t.Fatalf("NamespacedName must preserve lowercase API fields: %s", data)
	}
}

func TestMigrationSchemeRegistration(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"NetworkResourceTransition", "NetworkResourceTransitionList"} {
		gvk := GroupVersion.WithKind(kind)
		if _, err := scheme.New(gvk); err != nil {
			t.Fatalf("register %s: %v", kind, err)
		}
	}
	if GroupVersion.Group != "migration.vmware.com" {
		t.Fatalf("unexpected group: %s", GroupVersion.Group)
	}
}
