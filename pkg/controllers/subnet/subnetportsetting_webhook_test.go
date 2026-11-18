/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnet

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func TestSubnetPortSettingValidatorIdentity(t *testing.T) {
	identities := []struct {
		name    string
		user    authenticationv1.UserInfo
		allowed bool
	}{
		{name: "operator", user: authenticationv1.UserInfo{Username: NSXOperatorSA}, allowed: true},
		{name: "user", user: authenticationv1.UserInfo{Username: "alice"}},
		{name: "anonymous", user: authenticationv1.UserInfo{Username: "system:anonymous"}},
		{name: "empty identity", user: authenticationv1.UserInfo{}},
		{name: "foreign service account", user: authenticationv1.UserInfo{Username: "system:serviceaccount:tenant-1:application"}},
		{name: "operator account in another namespace", user: authenticationv1.UserInfo{Username: "system:serviceaccount:tenant-1:ncp-svc-account"}},
		{name: "different account in operator namespace", user: authenticationv1.UserInfo{Username: "system:serviceaccount:vmware-system-nsx:application"}},
		{name: "operator name prefix", user: authenticationv1.UserInfo{Username: NSXOperatorSA + "-other"}},
		{name: "masters group", user: authenticationv1.UserInfo{Username: "alice", Groups: []string{"system:masters"}}},
		{name: "service account groups", user: authenticationv1.UserInfo{Username: "alice", Groups: []string{"system:serviceaccounts", "system:serviceaccounts:vmware-system-nsx"}}},
		{name: "controller and operator names in groups", user: authenticationv1.UserInfo{Username: "alice", Groups: []string{NSXOperatorSA, "system:kube-controller-manager"}}},
		{name: "garbage collector in another namespace", user: authenticationv1.UserInfo{Username: "system:serviceaccount:tenant-1:generic-garbage-collector"}},
		{name: "namespace controller in another namespace", user: authenticationv1.UserInfo{Username: "system:serviceaccount:tenant-1:namespace-controller"}},
		{name: "controller name prefix", user: authenticationv1.UserInfo{Username: "system:kube-controller-manager-other"}},
	}
	operations := []struct {
		name        string
		operation   admissionv1.Operation
		subresource string
	}{
		{name: "create", operation: admissionv1.Create},
		{name: "update", operation: admissionv1.Update},
		{name: "delete", operation: admissionv1.Delete},
		{name: "status update", operation: admissionv1.Update, subresource: "status"},
	}
	for _, identity := range identities {
		t.Run(identity.name, func(t *testing.T) {
			for _, operation := range operations {
				t.Run(operation.name, func(t *testing.T) {
					oldSetting := portSettingWebhookObject()
					setting := oldSetting.DeepCopy()
					if operation.subresource == "" {
						setting.Spec.SubnetName = "subnet-2"
						setting.Annotations["nsx.vmware.com/associated-resource"] = "project-1:vpc-1:subnet-2:default"
					}
					setting.Status.Conditions[0].Status = corev1.ConditionFalse
					req := portSettingWebhookRequest(t, operation.operation, operation.subresource, identity.user, oldSetting, setting)

					response := newPortSettingWebhookValidator(t).Handle(context.Background(), req)

					assert.Equal(t, identity.allowed, response.Allowed)
					if !identity.allowed {
						require.NotNil(t, response.Result)
						assert.EqualValues(t, http.StatusForbidden, response.Result.Code)
						assert.Contains(t, response.Result.Message, "managed by NSX Operator")
					}
				})
			}
		})
	}
}

func TestSubnetPortSettingValidatorUserMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*v1alpha1.SubnetPortSetting)
	}{
		{name: "no operator metadata", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.Annotations = nil
			setting.OwnerReferences = nil
		}},
		{name: "forged source annotation", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.Annotations["nsx.vmware.com/associated-resource"] = "project-1:vpc-1:forged:default"
		}},
		{name: "source annotation removed", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			delete(setting.Annotations, "nsx.vmware.com/associated-resource")
		}},
		{name: "owner references removed", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.OwnerReferences = nil
		}},
		{name: "forged owner reference", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.OwnerReferences[0].Name = "forged-subnet"
			setting.OwnerReferences[0].UID = "forged-owner-uid"
		}},
		{name: "label change", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.Labels["example.com/team"] = "other"
		}},
		{name: "finalizer cleanup", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.Finalizers = nil
		}},
		{name: "spec change", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.Spec.SubnetName = "subnet-2"
		}},
		{name: "status change on main resource", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.Status.Conditions[0].Status = corev1.ConditionFalse
		}},
		{name: "no-op update", mutate: func(_ *v1alpha1.SubnetPortSetting) {}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, operation := range []admissionv1.Operation{admissionv1.Create, admissionv1.Update, admissionv1.Delete} {
				t.Run(string(operation), func(t *testing.T) {
					oldSetting := portSettingWebhookObject()
					setting := oldSetting.DeepCopy()
					tt.mutate(setting)
					if operation == admissionv1.Delete {
						oldSetting = setting
					}
					req := portSettingWebhookRequest(t, operation, "", authenticationv1.UserInfo{Username: "alice"}, oldSetting, setting)

					response := newPortSettingWebhookValidator(t).Handle(context.Background(), req)

					assert.False(t, response.Allowed)
					require.NotNil(t, response.Result)
					assert.EqualValues(t, http.StatusForbidden, response.Result.Code)
				})
			}
		})
	}
}

func TestSubnetPortSettingValidatorLifecycleControllers(t *testing.T) {
	controllers := []string{
		"system:kube-controller-manager",
		"system:serviceaccount:kube-system:generic-garbage-collector",
		"system:serviceaccount:kube-system:namespace-controller",
	}
	for _, username := range controllers {
		t.Run(username, func(t *testing.T) {
			for _, operation := range []struct {
				name        string
				operation   admissionv1.Operation
				subresource string
				allowed     bool
			}{
				{name: "delete", operation: admissionv1.Delete, allowed: true},
				{name: "create", operation: admissionv1.Create},
				{name: "status update", operation: admissionv1.Update, subresource: "status"},
				{name: "status create", operation: admissionv1.Create, subresource: "status"},
				{name: "status delete", operation: admissionv1.Delete, subresource: "status"},
			} {
				t.Run(operation.name, func(t *testing.T) {
					setting := portSettingWebhookObject()
					req := portSettingWebhookRequest(t, operation.operation, operation.subresource, authenticationv1.UserInfo{Username: username}, setting, setting)

					response := newPortSettingWebhookValidator(t).Handle(context.Background(), req)

					assert.Equal(t, operation.allowed, response.Allowed)
					if !operation.allowed {
						require.NotNil(t, response.Result)
						assert.EqualValues(t, http.StatusForbidden, response.Result.Code)
					}
				})
			}
			for _, tt := range portSettingLifecycleUpdateCases() {
				t.Run(tt.name, func(t *testing.T) {
					oldSetting := portSettingWebhookObject()
					setting := oldSetting.DeepCopy()
					tt.mutate(setting)
					oldBefore, newBefore := oldSetting.DeepCopy(), setting.DeepCopy()

					assert.Equal(t, tt.allowed, portSettingLifecycleUpdateOnly(oldSetting, setting))
					assert.Equal(t, oldBefore, oldSetting, "comparison must not mutate the old object")
					assert.Equal(t, newBefore, setting, "comparison must not mutate the new object")
					req := portSettingWebhookRequest(t, admissionv1.Update, "", authenticationv1.UserInfo{Username: username}, oldSetting, setting)
					response := newPortSettingWebhookValidator(t).Handle(context.Background(), req)

					assert.Equal(t, tt.allowed, response.Allowed)
					if !tt.allowed {
						require.NotNil(t, response.Result)
						assert.EqualValues(t, http.StatusForbidden, response.Result.Code)
					}
				})
			}
		})
	}
}

type portSettingLifecycleUpdateCase struct {
	name    string
	mutate  func(*v1alpha1.SubnetPortSetting)
	allowed bool
}

func portSettingLifecycleUpdateCases() []portSettingLifecycleUpdateCase {
	return []portSettingLifecycleUpdateCase{
		{name: "no-op", mutate: func(_ *v1alpha1.SubnetPortSetting) {}, allowed: true},
		{name: "orphan owner reference removal", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.OwnerReferences = nil
		}, allowed: true},
		{name: "garbage collector unblocks owner deletion", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			block := false
			setting.OwnerReferences[0].BlockOwnerDeletion = &block
		}, allowed: true},
		{name: "finalizer cleanup", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.Finalizers = nil
		}, allowed: true},
		{name: "managed fields bookkeeping", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.ManagedFields[0].Manager = "kube-controller-manager"
		}, allowed: true},
		{name: "orphan and finalizer cleanup with bookkeeping", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.OwnerReferences = nil
			setting.Finalizers = nil
			setting.ManagedFields = nil
		}, allowed: true},
		{name: "spec change", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.Spec.SubnetName = "subnet-2"
		}},
		{name: "status change on main resource", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.Status.Conditions[0].Status = corev1.ConditionFalse
		}},
		{name: "source annotation change", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.Annotations["nsx.vmware.com/associated-resource"] = "project-1:vpc-1:subnet-2:default"
		}},
		{name: "source annotation removal", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.Annotations = nil
		}},
		{name: "unrelated annotation addition", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.Annotations["example.com/annotation"] = "changed"
		}},
		{name: "label change", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.Labels["example.com/team"] = "other"
		}},
		{name: "label removal", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.Labels = nil
		}},
		{name: "resource version change", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.ResourceVersion = "2"
		}},
		{name: "deletion timestamp change", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			stamp := metav1.NewTime(time.Unix(1700000000, 0).UTC())
			setting.DeletionTimestamp = &stamp
		}},
		{name: "cleanup combined with spec change", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.OwnerReferences = nil
			setting.Finalizers = nil
			setting.ManagedFields = nil
			setting.Spec.SubnetName = "subnet-2"
		}},
		{name: "cleanup combined with status change", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.Finalizers = nil
			setting.Status.Conditions = nil
		}},
		{name: "cleanup combined with annotation change", mutate: func(setting *v1alpha1.SubnetPortSetting) {
			setting.OwnerReferences = nil
			setting.Annotations = nil
		}},
	}
}

func TestSubnetPortSettingValidatorUnsupportedRequests(t *testing.T) {
	tests := []struct {
		name        string
		operation   admissionv1.Operation
		subresource string
	}{
		{name: "connect", operation: admissionv1.Connect},
		{name: "empty operation"},
		{name: "unknown operation", operation: admissionv1.Operation("PATCH")},
		{name: "status create", operation: admissionv1.Create, subresource: "status"},
		{name: "status delete", operation: admissionv1.Delete, subresource: "status"},
		{name: "unknown subresource create", operation: admissionv1.Create, subresource: "scale"},
		{name: "unknown subresource update", operation: admissionv1.Update, subresource: "scale"},
		{name: "unknown subresource delete", operation: admissionv1.Delete, subresource: "scale"},
	}
	for _, username := range []string{NSXOperatorSA, "alice", "system:kube-controller-manager"} {
		t.Run(username, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					setting := portSettingWebhookObject()
					req := portSettingWebhookRequest(t, tt.operation, tt.subresource, authenticationv1.UserInfo{Username: username}, setting, setting)

					response := newPortSettingWebhookValidator(t).Handle(context.Background(), req)

					assert.False(t, response.Allowed)
					require.NotNil(t, response.Result)
					assert.EqualValues(t, http.StatusForbidden, response.Result.Code)
				})
			}
		})
	}
}

func TestSubnetPortSettingValidatorDecodeErrors(t *testing.T) {
	tests := []struct {
		name        string
		operation   admissionv1.Operation
		subresource string
		username    string
		oldObject   bool
		raw         []byte
	}{
		{name: "empty create", operation: admissionv1.Create, username: NSXOperatorSA},
		{name: "malformed create", operation: admissionv1.Create, username: NSXOperatorSA, raw: []byte(`{"spec":`)},
		{name: "invalid field type", operation: admissionv1.Create, username: NSXOperatorSA, raw: []byte(`{"spec":{"subnetName":23}}`)},
		{name: "empty update", operation: admissionv1.Update, username: NSXOperatorSA},
		{name: "malformed update", operation: admissionv1.Update, username: NSXOperatorSA, raw: []byte(`{"spec":`)},
		{name: "malformed status", operation: admissionv1.Update, subresource: "status", username: NSXOperatorSA, raw: []byte(`{"status":`)},
		{name: "empty delete old object", operation: admissionv1.Delete, username: NSXOperatorSA, oldObject: true},
		{name: "malformed delete old object", operation: admissionv1.Delete, username: NSXOperatorSA, oldObject: true, raw: []byte(`{"spec":`)},
		{name: "empty controller update old object", operation: admissionv1.Update, username: "system:kube-controller-manager", oldObject: true},
		{name: "malformed controller update old object", operation: admissionv1.Update, username: "system:kube-controller-manager", oldObject: true, raw: []byte(`{"spec":`)},
		{name: "malformed garbage collector update", operation: admissionv1.Update, username: "system:serviceaccount:kube-system:generic-garbage-collector", raw: []byte(`{"spec":`)},
		{name: "malformed namespace controller delete", operation: admissionv1.Delete, username: "system:serviceaccount:kube-system:namespace-controller", oldObject: true, raw: []byte(`{"spec":`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setting := portSettingWebhookObject()
			req := portSettingWebhookRequest(t, tt.operation, tt.subresource, authenticationv1.UserInfo{Username: tt.username}, setting, setting)
			if tt.oldObject {
				req.OldObject = runtime.RawExtension{Raw: tt.raw}
			} else {
				req.Object = runtime.RawExtension{Raw: tt.raw}
			}

			response := newPortSettingWebhookValidator(t).Handle(context.Background(), req)

			assert.False(t, response.Allowed)
			require.NotNil(t, response.Result)
			assert.EqualValues(t, http.StatusBadRequest, response.Result.Code)
			assert.NotEmpty(t, response.Result.Message)
		})
	}
}

func newPortSettingWebhookValidator(t *testing.T) *SubnetPortSettingValidator {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	return &SubnetPortSettingValidator{decoder: admission.NewDecoder(scheme)}
}

func portSettingWebhookObject() *v1alpha1.SubnetPortSetting {
	controller, blockOwnerDeletion := true, true
	return &v1alpha1.SubnetPortSetting{
		TypeMeta: metav1.TypeMeta{APIVersion: v1alpha1.GroupVersion.String(), Kind: "SubnetPortSetting"},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       "tenant-1",
			Name:            "subnet-1-default",
			UID:             types.UID("setting-uid"),
			ResourceVersion: "1",
			Annotations:     map[string]string{"nsx.vmware.com/associated-resource": "project-1:vpc-1:subnet-1:default"},
			Labels:          map[string]string{"example.com/team": "tenant-1"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         v1alpha1.GroupVersion.String(),
				Kind:               "Subnet",
				Name:               "subnet-1",
				UID:                types.UID("subnet-uid"),
				Controller:         &controller,
				BlockOwnerDeletion: &blockOwnerDeletion,
			}},
			Finalizers: []string{metav1.FinalizerDeleteDependents},
			ManagedFields: []metav1.ManagedFieldsEntry{{
				Manager:    "nsx-operator",
				Operation:  metav1.ManagedFieldsOperationUpdate,
				APIVersion: v1alpha1.GroupVersion.String(),
			}},
		},
		Spec: v1alpha1.SubnetPortSettingSpec{SubnetName: "subnet-1"},
		Status: v1alpha1.SubnetPortSettingStatus{Conditions: []v1alpha1.Condition{{
			Type: v1alpha1.Ready, Status: corev1.ConditionTrue, Reason: "Ready",
		}}},
	}
}

func portSettingWebhookRequest(t *testing.T, operation admissionv1.Operation, subresource string, user authenticationv1.UserInfo, oldSetting, setting *v1alpha1.SubnetPortSetting) admission.Request {
	t.Helper()
	req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		UID:         types.UID("request-uid"),
		Kind:        metav1.GroupVersionKind{Group: v1alpha1.GroupVersion.Group, Version: v1alpha1.GroupVersion.Version, Kind: "SubnetPortSetting"},
		Resource:    metav1.GroupVersionResource{Group: v1alpha1.GroupVersion.Group, Version: v1alpha1.GroupVersion.Version, Resource: "subnetportsettings"},
		Namespace:   setting.Namespace,
		Name:        setting.Name,
		Operation:   operation,
		SubResource: subresource,
		UserInfo:    user,
	}}
	if operation != admissionv1.Delete {
		raw, err := json.Marshal(setting)
		require.NoError(t, err)
		req.Object = runtime.RawExtension{Raw: raw}
	}
	if operation == admissionv1.Update || operation == admissionv1.Delete {
		raw, err := json.Marshal(oldSetting)
		require.NoError(t, err)
		req.OldObject = runtime.RawExtension{Raw: raw}
	}
	return req
}
