/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package common

import (
	"testing"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

func vpcNamespace(name string, vpc bool) *corev1.Namespace {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if vpc {
		ns.Annotations = map[string]string{config.VPCNetworkConfigAnnotation: "default"}
	}
	return ns
}

func TestVPCNamespacePredicate(t *testing.T) {
	// Force per-namespace providers so IsVPCNamespace honors the annotation.
	patches := gomonkey.ApplyFunc(config.IsPerNamespaceProvidersSupported, func() bool { return true })
	defer patches.Reset()

	vpcNs := vpcNamespace("vpc-ns", true)
	t1Ns := vpcNamespace("t1-ns", false)
	c := fake.NewClientBuilder().WithObjects(vpcNs, t1Ns).Build()
	pred := VPCNamespacePredicate(c)

	obj := func(ns string) *corev1.Pod {
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: ns}}
	}

	tests := []struct {
		name  string
		ns    string
		allow bool
	}{
		{"VPC namespace allowed", "vpc-ns", true},
		{"T1 namespace blocked", "t1-ns", false},
		{"cluster-scoped allowed", "", true},
		{"missing namespace fail-open", "missing-ns", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.allow, pred.Create(event.CreateEvent{Object: obj(tt.ns)}))
			assert.Equal(t, tt.allow, pred.Update(event.UpdateEvent{ObjectNew: obj(tt.ns)}))
			assert.Equal(t, tt.allow, pred.Generic(event.GenericEvent{Object: obj(tt.ns)}))
			// Delete is always allowed regardless of namespace metadata.
			assert.True(t, pred.Delete(event.DeleteEvent{Object: obj(tt.ns)}))
		})
	}
}
