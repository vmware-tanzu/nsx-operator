/* Copyright © 2025 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"context"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestUtil_IsNsInSystemNamespace(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	ctx := context.TODO()
	dummyNs := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "dummy"}}
	client.Create(ctx, dummyNs)
	ns := types.NamespacedName{Namespace: "dummy", Name: "dummy"}

	isCRInSysNs, err := IsSystemNamespace(client, ns.Namespace, nil, false)
	if err != nil {
		t.Fatalf("%s", err.Error())
	}
	if isCRInSysNs {
		t.Fatalf("Non-system namespace identied as a system namespace")
	}
	client.Delete(ctx, dummyNs)

	sysNs := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "sys-ns",
			Namespace:   "sys-ns",
			Annotations: map[string]string{"vmware-system-shared-t1": "true"},
		},
	}
	client.Create(ctx, sysNs)
	ns = types.NamespacedName{Namespace: "sys-ns", Name: "dummy"}

	isCRInSysNs, err = IsSystemNamespace(client, ns.Namespace, nil, false)
	if err != nil {
		t.Fatalf("%s", err.Error())
	}
	if !isCRInSysNs {
		t.Fatalf("System namespace not identied as a system namespace")
	}
	client.Delete(ctx, sysNs)
}
