package networkinfo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/yaml"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	controllercommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestVPCNetworkConfigurationWebhookRegistration(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	patches := gomonkey.ApplyFunc((*NetworkInfoReconciler).Start, func(*NetworkInfoReconciler, ctrl.Manager) error { return nil })
	defer patches.Reset()
	gcStarted := make(chan struct{})
	patches.ApplyFunc(controllercommon.GenericGarbageCollector, func(chan bool, time.Duration, func(context.Context) error) {
		close(gcStarted)
	})
	server := webhook.NewServer(webhook.Options{})
	r := &NetworkInfoReconciler{}
	require.NoError(t, r.StartController(&MockManager{scheme: scheme}, server))
	<-gcStarted
	review := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{Kind: "AdmissionReview", APIVersion: "admission.k8s.io/v1"},
		Request: &admissionv1.AdmissionRequest{
			UID: "test-request", Name: "system", Operation: admissionv1.Create,
			Object: runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"system","annotations":{"nsx.vmware.com/lb-provider":"avi"}}}`)},
		},
	}
	body, err := json.Marshal(review)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/validate-crd-nsx-vmware-com-v1alpha1-vpcnetworkconfiguration", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.WebhookMux().ServeHTTP(response, req)
	require.Equal(t, http.StatusOK, response.Code)
	result := &admissionv1.AdmissionReview{}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), result))
	require.NotNil(t, result.Response)
	assert.Equal(t, review.Request.UID, result.Response.UID)
	assert.False(t, result.Response.Allowed)
}

func TestVPCNetworkConfigurationValidator(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	v := &VPCNetworkConfigurationValidator{decoder: admission.NewDecoder(scheme)}
	for _, tc := range []struct {
		name           string
		operation      admissionv1.Operation
		oldAnnotations map[string]string
		annotations    map[string]string
		username       string
		configName     string
		allowed        bool
	}{
		{name: "create without provider", operation: admissionv1.Create, allowed: true},
		{name: "create with provider denied", operation: admissionv1.Create, annotations: map[string]string{common.AnnotationLBProvider: "avi"}},
		{name: "create with empty provider denied", operation: admissionv1.Create, annotations: map[string]string{common.AnnotationLBProvider: ""}},
		{name: "add provider denied", operation: admissionv1.Update, annotations: map[string]string{common.AnnotationLBProvider: "avi"}},
		{name: "change provider denied", operation: admissionv1.Update, oldAnnotations: map[string]string{common.AnnotationLBProvider: "avi"}, annotations: map[string]string{common.AnnotationLBProvider: "nsx-lb"}},
		{name: "remove provider denied", operation: admissionv1.Update, oldAnnotations: map[string]string{common.AnnotationLBProvider: "avi"}},
		{name: "remove empty provider denied", operation: admissionv1.Update, oldAnnotations: map[string]string{common.AnnotationLBProvider: ""}},
		{name: "change to empty provider denied", operation: admissionv1.Update, oldAnnotations: map[string]string{common.AnnotationLBProvider: "avi"}, annotations: map[string]string{common.AnnotationLBProvider: ""}},
		{name: "other metadata and spec updates allowed", operation: admissionv1.Update, oldAnnotations: map[string]string{common.AnnotationLBProvider: "avi", "other": "old"}, annotations: map[string]string{common.AnnotationLBProvider: "avi", "other": "new"}, allowed: true},
		{name: "no provider change allowed", operation: admissionv1.Update, annotations: map[string]string{"other": "value"}, allowed: true},
		{name: "operator and NCP may initialize", operation: admissionv1.Create, username: lbProviderWriter, annotations: map[string]string{common.AnnotationLBProvider: "avi"}, allowed: true},
		{name: "operator and NCP may change", operation: admissionv1.Update, username: lbProviderWriter, oldAnnotations: map[string]string{common.AnnotationLBProvider: "avi"}, annotations: map[string]string{common.AnnotationLBProvider: "nsx-lb"}, allowed: true},
		{name: "operator and NCP may remove", operation: admissionv1.Update, username: lbProviderWriter, oldAnnotations: map[string]string{common.AnnotationLBProvider: "avi"}, allowed: true},
		{name: "same account in other namespace denied", operation: admissionv1.Update, username: "system:serviceaccount:other:ncp-svc-account", annotations: map[string]string{common.AnnotationLBProvider: "avi"}},
		{name: "other account in same namespace denied", operation: admissionv1.Update, username: "system:serviceaccount:vmware-system-nsx:other", annotations: map[string]string{common.AnnotationLBProvider: "avi"}},
		{name: "other network configurations unaffected", operation: admissionv1.Update, configName: "custom", oldAnnotations: map[string]string{common.AnnotationLBProvider: "avi"}, allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := tc.configName
			if name == "" {
				name = common.SystemVPCNetworkConfigurationName
			}
			old := &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: tc.oldAnnotations}}
			updated := old.DeepCopy()
			updated.Annotations = tc.annotations
			updated.Labels = map[string]string{"other": "changed"}
			updated.Spec.PrivateIPs = []string{"10.0.0.0/24"}
			oldRaw, err := json.Marshal(old)
			require.NoError(t, err)
			newRaw, err := json.Marshal(updated)
			require.NoError(t, err)
			req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
				Name: name, Operation: tc.operation,
				UserInfo: authenticationv1.UserInfo{Username: tc.username},
				Object:   runtime.RawExtension{Raw: newRaw}, OldObject: runtime.RawExtension{Raw: oldRaw},
			}}
			response := v.Handle(context.Background(), req)
			assert.Equal(t, tc.allowed, response.Allowed, "%v", response.Result)
			if !tc.allowed {
				assert.EqualValues(t, http.StatusForbidden, response.Result.Code)
			}
		})
	}
}

func TestVPCNetworkConfigurationValidatorMalformedRequest(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	v := &VPCNetworkConfigurationValidator{decoder: admission.NewDecoder(scheme)}
	for _, malformedOld := range []bool{false, true} {
		req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			Object:    runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"system"}}`)},
			OldObject: runtime.RawExtension{Raw: []byte(`{`)},
		}}
		if !malformedOld {
			req.Object.Raw = []byte(`{`)
		}
		response := v.Handle(context.Background(), req)
		assert.False(t, response.Allowed)
		assert.EqualValues(t, http.StatusBadRequest, response.Result.Code)
	}
}

func TestVPCNetworkConfigurationWebhookManifest(t *testing.T) {
	data, err := os.ReadFile("../../../build/yaml/webhook/manifests.yaml")
	require.NoError(t, err)
	manifest := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	require.NoError(t, yaml.UnmarshalStrict(data, manifest))
	for _, hook := range manifest.Webhooks {
		if hook.Name != "vpcnetworkconfiguration.validating.crd.nsx.vmware.com" {
			continue
		}
		require.NotNil(t, hook.ClientConfig.Service)
		assert.Equal(t, "/validate-crd-nsx-vmware-com-v1alpha1-vpcnetworkconfiguration", *hook.ClientConfig.Service.Path)
		assert.Equal(t, admissionregistrationv1.Fail, *hook.FailurePolicy)
		require.Len(t, hook.Rules, 1)
		assert.Equal(t, []string{"vpcnetworkconfigurations"}, hook.Rules[0].Resources)
		assert.Equal(t, admissionregistrationv1.ClusterScope, *hook.Rules[0].Scope)
		assert.ElementsMatch(t, []admissionregistrationv1.OperationType{admissionregistrationv1.Create, admissionregistrationv1.Update}, hook.Rules[0].Operations)
		assert.Contains(t, hook.MatchConditions, admissionregistrationv1.MatchCondition{
			Name: "system-network-configuration", Expression: "request.name == 'system'",
		})
		// Restore runs before controller/webhook registration. Trusted writes must not call an offline webhook.
		assert.Contains(t, hook.MatchConditions, admissionregistrationv1.MatchCondition{
			Name: "untrusted-writer", Expression: "request.userInfo.username != '" + lbProviderWriter + "'",
		})
		return
	}
	t.Fatal("VPCNetworkConfiguration webhook is missing from the manifest")
}
