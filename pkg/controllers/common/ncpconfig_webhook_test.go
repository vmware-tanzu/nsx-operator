package common

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/yaml"

	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func assertNCPConfigWebhookDeniesUntrustedWrite(t *testing.T, server webhook.Server) {
	t.Helper()
	review := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{Kind: "AdmissionReview", APIVersion: "admission.k8s.io/v1"},
		Request: &admissionv1.AdmissionRequest{
			UID: "test-request", Name: util.NSXRestoreStatus, Operation: admissionv1.Create,
			Object: runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"nsx-restore-status","annotations":{"nsx.vmware.com/lb-provider":"avi"}}}`)},
		},
	}
	body, err := json.Marshal(review)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/validate-nsx-vmware-com-v1-ncpconfig", bytes.NewReader(body))
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

func TestNCPConfigValidator(t *testing.T) {
	scheme := runtime.NewScheme()
	v := &NCPConfigValidator{decoder: admission.NewDecoder(scheme)}
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
		{name: "create with provider denied", operation: admissionv1.Create, annotations: map[string]string{servicecommon.AnnotationLBProvider: "avi"}},
		{name: "create with empty provider denied", operation: admissionv1.Create, annotations: map[string]string{servicecommon.AnnotationLBProvider: ""}},
		{name: "add provider denied", operation: admissionv1.Update, annotations: map[string]string{servicecommon.AnnotationLBProvider: "avi"}},
		{name: "change provider denied", operation: admissionv1.Update, oldAnnotations: map[string]string{servicecommon.AnnotationLBProvider: "avi"}, annotations: map[string]string{servicecommon.AnnotationLBProvider: "nsx-lb"}},
		{name: "remove provider denied", operation: admissionv1.Update, oldAnnotations: map[string]string{servicecommon.AnnotationLBProvider: "avi"}},
		{name: "remove empty provider denied", operation: admissionv1.Update, oldAnnotations: map[string]string{servicecommon.AnnotationLBProvider: ""}},
		{name: "change to empty provider denied", operation: admissionv1.Update, oldAnnotations: map[string]string{servicecommon.AnnotationLBProvider: "avi"}, annotations: map[string]string{servicecommon.AnnotationLBProvider: ""}},
		{name: "other metadata and spec updates allowed", operation: admissionv1.Update, oldAnnotations: map[string]string{servicecommon.AnnotationLBProvider: "avi", "other": "old"}, annotations: map[string]string{servicecommon.AnnotationLBProvider: "avi", "other": "new"}, allowed: true},
		{name: "no provider change allowed", operation: admissionv1.Update, annotations: map[string]string{"other": "value"}, allowed: true},
		{name: "operator and NCP may initialize", operation: admissionv1.Create, username: lbProviderWriter, annotations: map[string]string{servicecommon.AnnotationLBProvider: "avi"}, allowed: true},
		{name: "operator and NCP may change", operation: admissionv1.Update, username: lbProviderWriter, oldAnnotations: map[string]string{servicecommon.AnnotationLBProvider: "avi"}, annotations: map[string]string{servicecommon.AnnotationLBProvider: "nsx-lb"}, allowed: true},
		{name: "operator and NCP may remove", operation: admissionv1.Update, username: lbProviderWriter, oldAnnotations: map[string]string{servicecommon.AnnotationLBProvider: "avi"}, allowed: true},
		{name: "same account in other namespace denied", operation: admissionv1.Update, username: "system:serviceaccount:other:ncp-svc-account", annotations: map[string]string{servicecommon.AnnotationLBProvider: "avi"}},
		{name: "other account in same namespace denied", operation: admissionv1.Update, username: "system:serviceaccount:vmware-system-nsx:other", annotations: map[string]string{servicecommon.AnnotationLBProvider: "avi"}},
		{name: "other NCPConfigs unaffected", operation: admissionv1.Update, configName: "custom", oldAnnotations: map[string]string{servicecommon.AnnotationLBProvider: "avi"}, allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := tc.configName
			if name == "" {
				name = util.NSXRestoreStatus
			}
			old := util.NewNSXRestoreStatus()
			old.SetName(name)
			old.SetAnnotations(tc.oldAnnotations)
			updated := old.DeepCopy()
			updated.SetAnnotations(tc.annotations)
			updated.SetLabels(map[string]string{"other": "changed"})
			updated.Object["custom"] = "changed"
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

func TestNCPConfigValidatorMalformedRequest(t *testing.T) {
	scheme := runtime.NewScheme()
	v := &NCPConfigValidator{decoder: admission.NewDecoder(scheme)}
	for _, malformedOld := range []bool{false, true} {
		req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			Object:    runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"nsx-restore-status"}}`)},
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

func TestNCPConfigWebhookManifest(t *testing.T) {
	data, err := os.ReadFile("../../../build/yaml/webhook/manifests.yaml")
	require.NoError(t, err)
	manifest := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	require.NoError(t, yaml.UnmarshalStrict(data, manifest))
	for _, hook := range manifest.Webhooks {
		if hook.Name != "ncpconfig.validating.nsx.vmware.com" {
			continue
		}
		require.NotNil(t, hook.ClientConfig.Service)
		assert.Equal(t, "/validate-nsx-vmware-com-v1-ncpconfig", *hook.ClientConfig.Service.Path)
		assert.Equal(t, admissionregistrationv1.Fail, *hook.FailurePolicy)
		require.Len(t, hook.Rules, 1)
		assert.Equal(t, []string{"ncpconfigs"}, hook.Rules[0].Resources)
		assert.Equal(t, []string{"nsx.vmware.com"}, hook.Rules[0].APIGroups)
		assert.Equal(t, []string{"v1"}, hook.Rules[0].APIVersions)
		assert.Equal(t, admissionregistrationv1.ClusterScope, *hook.Rules[0].Scope)
		assert.ElementsMatch(t, []admissionregistrationv1.OperationType{admissionregistrationv1.Create, admissionregistrationv1.Update}, hook.Rules[0].Operations)
		assert.Contains(t, hook.MatchConditions, admissionregistrationv1.MatchCondition{
			Name: "restore-status", Expression: "request.name == 'nsx-restore-status'",
		})
		// Restore runs before controller/webhook registration. Trusted writes must not call an offline webhook.
		assert.Contains(t, hook.MatchConditions, admissionregistrationv1.MatchCondition{
			Name: "untrusted-writer", Expression: "request.userInfo.username != '" + lbProviderWriter + "'",
		})
		return
	}
	t.Fatal("NCPConfig webhook is missing from the manifest")
}
