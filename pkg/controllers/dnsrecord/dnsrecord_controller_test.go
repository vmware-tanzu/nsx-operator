/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dnsrecord

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	ctlcommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	mockdns "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnsrecordprovider"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func setupScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	return scheme
}

func TestCalculateFQDN(t *testing.T) {
	tests := []struct {
		name       string
		recordName string
		domainName string
		want       string
	}{
		{
			name:       "standard record and domain",
			recordName: "api",
			domainName: "example.com",
			want:       "api.example.com",
		},
		{
			name:       "apex record @",
			recordName: "@",
			domainName: "example.com",
			want:       "example.com",
		},
		{
			name:       "empty record name",
			recordName: "",
			domainName: "example.com.",
			want:       "example.com",
		},
		{
			name:       "PTR record name",
			recordName: "10",
			domainName: "0.0.10.in-addr.arpa",
			want:       "10.0.0.10.in-addr.arpa",
		},
		{
			name:       "uppercase and trailing dots",
			recordName: "Web.",
			domainName: "EXAMPLE.COM.",
			want:       "web.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateFQDN(tt.recordName, tt.domainName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDNSRecordReconciler_Reconcile_NotFound(t *testing.T) {
	scheme := setupScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &DNSRecordReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Service:       nil,
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}

	ctx := context.Background()
	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "nonexistent"},
	}

	res, err := r.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ResultNormal, res)
}

func TestDNSRecordReconciler_Reconcile_CreateAndUpdate(t *testing.T) {
	scheme := setupScheme()
	ttl := int32(600)
	dnsRecord := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-dnsrecord",
		},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName:   "example.com",
			RecordName:   "api",
			RecordType:   v1alpha1.DNSRecordTypeA,
			RecordValues: []string{"10.0.0.1"},
			TTL:          &ttl,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(dnsRecord).Build()
	r := &DNSRecordReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Service:       nil, // Service is nil -> tests controller FQDN calculation & finalizer addition
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}

	ctx := context.Background()
	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-dnsrecord"},
	}

	// First reconcile adds finalizer and computes FQDN
	res, err := r.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ResultNormal, res)

	updatedCR := &v1alpha1.DNSRecord{}
	err = fakeClient.Get(ctx, req.NamespacedName, updatedCR)
	assert.NoError(t, err)
	assert.Contains(t, updatedCR.Finalizers, servicecommon.DNSRecordFinalizerName)
	assert.Equal(t, "api.example.com", updatedCR.Spec.FQDN)
	assert.Len(t, updatedCR.Status.Conditions, 1)
	assert.Equal(t, string(v1alpha1.Ready), updatedCR.Status.Conditions[0].Type)
	assert.Equal(t, metav1.ConditionTrue, updatedCR.Status.Conditions[0].Status)
	assert.Equal(t, "DNSRecordReady", updatedCR.Status.Conditions[0].Reason)
}

func TestDNSRecordReconciler_Reconcile_Delete(t *testing.T) {
	scheme := setupScheme()
	now := metav1.Now()
	dnsRecord := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         "default",
			Name:              "test-dnsrecord",
			DeletionTimestamp: &now,
			Finalizers:        []string{servicecommon.DNSRecordFinalizerName},
		},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName:   "example.com",
			RecordName:   "api",
			RecordType:   v1alpha1.DNSRecordTypeA,
			RecordValues: []string{"10.0.0.1"},
			FQDN:         "api.example.com",
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dnsRecord).Build()
	r := &DNSRecordReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Service:       nil,
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}

	ctx := context.Background()
	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-dnsrecord"},
	}

	res, err := r.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ResultNormal, res)

	updatedCR := &v1alpha1.DNSRecord{}
	err = fakeClient.Get(ctx, req.NamespacedName, updatedCR)
	assert.Error(t, err)
}

func TestDNSRecordReconciler_RestoreAndGC(t *testing.T) {
	scheme := setupScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &DNSRecordReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Service:       nil,
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}

	ctx := context.Background()
	assert.NoError(t, r.RestoreReconcile())
	assert.NoError(t, r.CollectGarbage(ctx))
}

func TestDNSRecordReconciler_UpdateStatusConditions(t *testing.T) {
	scheme := setupScheme()
	dnsRecord := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-status",
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(dnsRecord).Build()
	ctx := context.Background()

	setDNSRecordReadyStatusTrue(fakeClient, ctx, dnsRecord, metav1.Now())
	updated := &v1alpha1.DNSRecord{}
	err := fakeClient.Get(ctx, client.ObjectKeyFromObject(dnsRecord), updated)
	assert.NoError(t, err)
	assert.Len(t, updated.Status.Conditions, 1)
	assert.Equal(t, metav1.ConditionTrue, updated.Status.Conditions[0].Status)

	setDNSRecordReadyStatusFalse(fakeClient, ctx, dnsRecord, metav1.Now(), assert.AnError)
	err = fakeClient.Get(ctx, client.ObjectKeyFromObject(dnsRecord), updated)
	assert.NoError(t, err)
	assert.Len(t, updated.Status.Conditions, 1)
	assert.Equal(t, metav1.ConditionFalse, updated.Status.Conditions[0].Status)
	assert.Equal(t, "DNSRecordFailed", updated.Status.Conditions[0].Reason)
}

func TestDNSRecordReconciler_MockDNSRecordProvider(t *testing.T) {
	ctrlMock := gomock.NewController(t)
	defer ctrlMock.Finish()

	_ = mockdns.NewMockDNSRecordProvider(ctrlMock)
	time.Sleep(10 * time.Millisecond)
}
