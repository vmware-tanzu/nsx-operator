/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnet

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/yaml"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	subnetservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
)

// Exercise the installed manifest against the handler registered by the Subnet
// controller, so a missing runtime path or missing status rule is caught.
func TestSubnetPortSettingWebhookRegistration(t *testing.T) {
	manifest, err := os.ReadFile("../../../build/yaml/webhook/manifests.yaml")
	require.NoError(t, err)
	var config admissionregistrationv1.ValidatingWebhookConfiguration
	require.NoError(t, yaml.Unmarshal(manifest, &config))
	var hook *admissionregistrationv1.ValidatingWebhook
	for i := range config.Webhooks {
		if config.Webhooks[i].Name == "subnetportsetting.validating.crd.nsx.vmware.com" {
			hook = &config.Webhooks[i]
		}
	}
	require.NotNil(t, hook)
	require.NotNil(t, hook.ClientConfig.Service)
	require.NotNil(t, hook.ClientConfig.Service.Path)
	require.NotNil(t, hook.FailurePolicy)
	require.Equal(t, admissionregistrationv1.Fail, *hook.FailurePolicy)
	require.NotNil(t, hook.SideEffects)
	require.Equal(t, admissionregistrationv1.SideEffectClassNone, *hook.SideEffects)
	require.Equal(t, []string{"v1"}, hook.AdmissionReviewVersions)
	require.Len(t, hook.Rules, 1)
	require.Equal(t, []string{v1alpha1.GroupVersion.Group}, hook.Rules[0].APIGroups)
	require.Equal(t, []string{v1alpha1.GroupVersion.Version}, hook.Rules[0].APIVersions)
	require.ElementsMatch(t, []string{"subnetportsettings", "subnetportsettings/status"}, hook.Rules[0].Resources)
	require.ElementsMatch(t, []admissionregistrationv1.OperationType{
		admissionregistrationv1.Create, admissionregistrationv1.Update, admissionregistrationv1.Delete,
	}, hook.Rules[0].Operations)

	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	server := webhook.NewServer(webhook.Options{})
	reconciler := &SubnetReconciler{SubnetService: &subnetservice.SubnetService{}}
	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(reconciler), "setupWithManager", func(_ *SubnetReconciler, _ ctrl.Manager) error {
		return nil
	})
	defer patches.Reset()
	pollStarted := make(chan struct{})
	patches.ApplyPrivateMethod(reflect.TypeOf(reconciler), "pollSharedSubnets", func(_ *SubnetReconciler, _ chan bool) {
		close(pollStarted)
	})
	require.NoError(t, reconciler.start(&MockManager{scheme: scheme}, server))
	<-pollStarted

	setting := &v1alpha1.SubnetPortSetting{
		TypeMeta:   metav1.TypeMeta{APIVersion: v1alpha1.GroupVersion.String(), Kind: "SubnetPortSetting"},
		ObjectMeta: metav1.ObjectMeta{Name: "port-setting", Namespace: "test-namespace"},
		Spec:       v1alpha1.SubnetPortSettingSpec{SubnetName: "shared-subnet"},
	}
	raw, err := json.Marshal(setting)
	require.NoError(t, err)
	for _, tc := range []struct {
		name      string
		username  string
		operation admissionv1.Operation
		sub       string
		allowed   bool
	}{
		{"operator create", NSXOperatorSA, admissionv1.Create, "", true},
		{"operator status", NSXOperatorSA, admissionv1.Update, "status", true},
		{"user delete", "test-user", admissionv1.Delete, "", false},
		{"user status", "test-user", admissionv1.Update, "status", false},
		{"garbage collection", "system:serviceaccount:kube-system:generic-garbage-collector", admissionv1.Delete, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			review := admissionv1.AdmissionReview{
				TypeMeta: metav1.TypeMeta{APIVersion: admissionv1.SchemeGroupVersion.String(), Kind: "AdmissionReview"},
				Request: &admissionv1.AdmissionRequest{
					UID: "request", Name: setting.Name, Namespace: setting.Namespace,
					Kind:        metav1.GroupVersionKind{Group: v1alpha1.GroupVersion.Group, Version: v1alpha1.GroupVersion.Version, Kind: setting.Kind},
					Resource:    metav1.GroupVersionResource{Group: v1alpha1.GroupVersion.Group, Version: v1alpha1.GroupVersion.Version, Resource: "subnetportsettings"},
					SubResource: tc.sub, Operation: tc.operation,
					UserInfo:  authenticationv1.UserInfo{Username: tc.username},
					Object:    runtime.RawExtension{Raw: raw},
					OldObject: runtime.RawExtension{Raw: raw},
				},
			}
			body, err := json.Marshal(review)
			require.NoError(t, err)
			request := httptest.NewRequest(http.MethodPost, *hook.ClientConfig.Service.Path, bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.WebhookMux().ServeHTTP(response, request.WithContext(context.Background()))
			require.Equal(t, http.StatusOK, response.Code)
			var admitted admissionv1.AdmissionReview
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &admitted))
			require.NotNil(t, admitted.Response)
			require.Equal(t, review.Request.UID, admitted.Response.UID)
			require.Equal(t, tc.allowed, admitted.Response.Allowed)
		})
	}
}
