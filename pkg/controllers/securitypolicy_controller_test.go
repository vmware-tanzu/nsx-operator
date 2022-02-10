/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	"reflect"
	"testing"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func NewFakeSecurityPolicyReconciler() *SecurityPolicyReconciler {
	return &SecurityPolicyReconciler{
		Client:  fake.NewClientBuilder().Build(),
		Scheme:  fake.NewClientBuilder().Build().Scheme(),
		Service: nil,
	}
}

func TestSecurityPolicyController_updateSecurityPolicyStatusConditions(t *testing.T) {
	r := NewFakeSecurityPolicyReconciler()
	ctx := context.TODO()
	dummySP := &v1alpha1.SecurityPolicy{}

	// Case: Security Policy CRD creation fails
	newConditions := []v1alpha1.SecurityPolicyCondition{
		{
			Type:    v1alpha1.SecurityPolicyReady,
			Status:  v1.ConditionFalse,
			Message: "NSX Security Policy could not be created/updated",
			Reason:  "Error occurred while processing the Security Policy CRD. Please check the config and try again",
		},
	}
	r.updateSecurityPolicyStatusConditions(&ctx, dummySP, newConditions)

	if !reflect.DeepEqual(dummySP.Status.Conditions, newConditions) {
		t.Fatalf("Failed to correctly update Status Conditions when conditions haven't changed")
	}

	// Case: No change in Conditions
	dummyConditions := []v1alpha1.SecurityPolicyCondition{
		{
			Type:    v1alpha1.SecurityPolicyReady,
			Status:  v1.ConditionFalse,
			Message: "NSX Security Policy could not be created/updated",
			Reason:  "Error occurred while processing the Security Policy CRD. Please check the config and try again",
		},
	}
	dummySP.Status.Conditions = dummyConditions

	newConditions = []v1alpha1.SecurityPolicyCondition{
		{
			Type:    v1alpha1.SecurityPolicyReady,
			Status:  v1.ConditionFalse,
			Message: "NSX Security Policy could not be created/updated",
			Reason:  "Error occurred while processing the Security Policy CRD. Please check the config and try again",
		},
	}

	r.updateSecurityPolicyStatusConditions(&ctx, dummySP, newConditions)

	if !reflect.DeepEqual(dummySP.Status.Conditions, newConditions) {
		t.Fatalf("Failed to correctly update Status Conditions when conditions haven't changed")
	}

	// Case: SP CRD Creation succeeds after failure
	newConditions = []v1alpha1.SecurityPolicyCondition{
		{
			Type:    v1alpha1.SecurityPolicyReady,
			Status:  v1.ConditionTrue,
			Message: "NSX Security Policy has been successfully created/updated",
			Reason:  "NSX API returned 200 response code for PATCH",
		},
	}

	r.updateSecurityPolicyStatusConditions(&ctx, dummySP, newConditions)

	if !reflect.DeepEqual(dummySP.Status.Conditions, newConditions) {
		t.Fatalf("Failed to correctly update Status Conditions when conditions haven't changed")
	}

	// Case: SP CRD Update failed
	newConditions = []v1alpha1.SecurityPolicyCondition{
		{
			Type:    v1alpha1.SecurityPolicyReady,
			Status:  v1.ConditionFalse,
			Message: "NSX Security Policy could not be created/updated",
			Reason:  "Error occurred while processing the Security Policy CRD. Please check the config and try again",
		},
	}

	r.updateSecurityPolicyStatusConditions(&ctx, dummySP, newConditions)

	if !reflect.DeepEqual(dummySP.Status.Conditions, newConditions) {
		t.Fatalf("Failed to correctly update Status Conditions when conditions haven't changed")
	}
}

func TestSecurityPolicyController_isCRRequestedInSystemNamespace(t *testing.T) {
	r := NewFakeSecurityPolicyReconciler()
	ctx := context.TODO()
	dummyNs := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "dummy"}}
	r.Client.Create(ctx, dummyNs)
	req := &controllerruntime.Request{NamespacedName: types.NamespacedName{Namespace: "dummy", Name: "dummy"}}

	isCRInSysNs, err := r.isCRRequestedInSystemNamespace(&ctx, req)

	if err != nil {
		t.Fatalf(err.Error())
	}
	if isCRInSysNs {
		t.Fatalf("Non-system namespace identied as a system namespace")
	}
	r.Client.Delete(ctx, dummyNs)

	sysNs := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "sys-ns",
			Namespace:   "sys-ns",
			Annotations: map[string]string{"vmware-system-shared-t1": "true"},
		},
	}
	r.Client.Create(ctx, sysNs)
	req = &controllerruntime.Request{NamespacedName: types.NamespacedName{Namespace: "sys-ns", Name: "dummy"}}

	isCRInSysNs, err = r.isCRRequestedInSystemNamespace(&ctx, req)

	if err != nil {
		t.Fatalf(err.Error())
	}
	if !isCRInSysNs {
		t.Fatalf("System namespace not identied as a system namespace")
	}
	r.Client.Delete(ctx, sysNs)
}
