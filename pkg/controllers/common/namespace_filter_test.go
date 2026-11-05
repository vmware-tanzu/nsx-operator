/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package common

import (
	"context"
	"testing"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type unavailableOwnerReader struct{ client.Reader }

func (r unavailableOwnerReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if key.Name == "owner" {
		return context.DeadlineExceeded
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}

func TestSubnetPortOwnerLookupFailurePreservesEvent(t *testing.T) {
	patches := gomonkey.ApplyFunc(config.IsPerNamespaceProvidersSupported, func() bool { return true })
	defer patches.Reset()
	child := vpcNamespace("staging", false)
	child.Annotations = map[string]string{servicecommon.AnnotationSharedVPCNamespace: "owner"}
	api := fake.NewClientBuilder().WithObjects(child, vpcNamespace("owner", true)).Build()
	pred := SubnetPortNamespacePredicate(unavailableOwnerReader{api})
	obj := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "port", Namespace: child.Name}}
	assert.True(t, pred.Create(event.CreateEvent{Object: obj}))
	assert.True(t, pred.Update(event.UpdateEvent{ObjectNew: obj}))
	assert.True(t, pred.Generic(event.GenericEvent{Object: obj}))
}

func vpcNamespace(name string, vpc bool) *corev1.Namespace {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if vpc {
		ns.Annotations = map[string]string{config.VPCNetworkConfigAnnotation: "default"}
	}
	return ns
}

func TestSubnetPortSharedVPCNamespacePredicate(t *testing.T) {
	patches := gomonkey.ApplyFunc(config.IsPerNamespaceProvidersSupported, func() bool { return true })
	defer patches.Reset()
	config.SetMixedModeStateForTest(true, true)
	t.Cleanup(func() { config.SetMixedModeStateForTest(false, false) })
	for _, tc := range []struct {
		name        string
		shared      string
		annotations map[string]string
		allow       bool
	}{
		{name: "system shared VPC", shared: "kube-system", allow: true},
		{name: "system prefix alone"},
		{name: "missing shared namespace retries", shared: "missing", allow: true},
		{name: "shared T1 namespace", shared: "t1-ns"},
		{name: "self reference", shared: "vmware-system-net-staging"},
		{name: "T1 default conflict", shared: "kube-system", annotations: map[string]string{config.T1DefaultConfigAnnotation: "true"}},
		{name: "T1 shared conflict", shared: "kube-system", annotations: map[string]string{config.T1VmwareSystemSharedAnnotation: "true"}},
		{name: "T1 network config conflict", shared: "kube-system", annotations: map[string]string{config.T1NsxNetworkConfigAnnotation: "legacy"}},
		{name: "explicit VPC", annotations: map[string]string{config.VPCNetworkConfigAnnotation: "system"}, allow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns := vpcNamespace("vmware-system-net-staging", false)
			ns.Annotations = map[string]string{}
			for key, value := range tc.annotations {
				ns.Annotations[key] = value
			}
			if tc.shared != "" {
				ns.Annotations[servicecommon.AnnotationSharedVPCNamespace] = tc.shared
			}
			api := fake.NewClientBuilder().WithObjects(ns, vpcNamespace("kube-system", true), t1Namespace("t1-ns")).Build()
			pred := SubnetPortNamespacePredicate(api)
			obj := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "owner", Namespace: ns.Name}}
			assert.Equal(t, tc.allow, pred.Create(event.CreateEvent{Object: obj}))
			assert.Equal(t, tc.allow, pred.Update(event.UpdateEvent{ObjectNew: obj}))
			assert.Equal(t, tc.allow, pred.Generic(event.GenericEvent{Object: obj}))
			assert.True(t, pred.Delete(event.DeleteEvent{Object: obj}))
			now := metav1.Now()
			obj.DeletionTimestamp = &now
			assert.True(t, pred.Update(event.UpdateEvent{ObjectNew: obj}))
			// The exception does not admit the namespace to unrelated controllers.
			assert.Equal(t, config.IsVPCNamespace(ns), VPCNamespacePredicate(api).Create(event.CreateEvent{Object: obj}))
		})
	}
}

func TestVPCNamespacePredicate(t *testing.T) {
	// Force per-namespace providers and HasVPCNamespaces so cluster-scoped resources are allowed.
	patches := gomonkey.ApplyFunc(config.IsPerNamespaceProvidersSupported, func() bool { return true })
	defer patches.Reset()
	config.SetMixedModeStateForTest(true, true)
	t.Cleanup(func() { config.SetMixedModeStateForTest(false, false) })

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

			// Update event with DeletionTimestamp set should always be allowed
			delObj := obj(tt.ns)
			now := metav1.Now()
			delObj.SetDeletionTimestamp(&now)
			assert.True(t, pred.Update(event.UpdateEvent{ObjectNew: delObj}))

			assert.Equal(t, tt.allow, pred.Generic(event.GenericEvent{Object: obj(tt.ns)}))
			// Delete is always allowed regardless of namespace metadata.
			assert.True(t, pred.Delete(event.DeleteEvent{Object: obj(tt.ns)}))
		})
	}
}

func t1Namespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Annotations: map[string]string{
				config.T1DefaultConfigAnnotation: "true",
			},
		},
	}
}

func TestT1NamespacePredicate(t *testing.T) {
	patches := gomonkey.ApplyFunc(config.IsPerNamespaceProvidersSupported, func() bool { return true })
	defer patches.Reset()
	config.SetMixedModeStateForTest(true, true)
	t.Cleanup(func() { config.SetMixedModeStateForTest(false, false) })

	t1Ns := t1Namespace("t1-ns")
	vpcNs := vpcNamespace("vpc-ns", true)
	bareNs := vpcNamespace("bare-ns", false)
	c := fake.NewClientBuilder().WithObjects(t1Ns, vpcNs, bareNs).Build()
	pred := T1NamespacePredicate(c)

	obj := func(ns string) *corev1.Pod {
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: ns}}
	}

	tests := []struct {
		name  string
		ns    string
		allow bool
	}{
		{"T1 namespace allowed", "t1-ns", true},
		{"VPC namespace blocked", "vpc-ns", false},
		{"Bare namespace blocked", "bare-ns", false},
		{"cluster-scoped allowed", "", true},
		{"missing namespace fail-open", "missing-ns", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.allow, pred.Create(event.CreateEvent{Object: obj(tt.ns)}))
			assert.Equal(t, tt.allow, pred.Update(event.UpdateEvent{ObjectNew: obj(tt.ns)}))

			delObj := obj(tt.ns)
			now := metav1.Now()
			delObj.SetDeletionTimestamp(&now)
			assert.True(t, pred.Update(event.UpdateEvent{ObjectNew: delObj}))

			assert.Equal(t, tt.allow, pred.Generic(event.GenericEvent{Object: obj(tt.ns)}))
			assert.True(t, pred.Delete(event.DeleteEvent{Object: obj(tt.ns)}))
		})
	}
}
