/*
Copyright © 2025 VMware, Inc. All Rights Reserved.

	SPDX-License-Identifier: Apache-2.0
*/
package staticroute

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	gomonkey "github.com/agiledragon/gomonkey/v2"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
)

func TestHandle(t *testing.T) {
	tests := []struct {
		name      string
		operation admissionv1.Operation
		namespace string
		objects   []client.Object
		allowed   bool
	}{
		{
			name:      "delete operation allowed",
			operation: admissionv1.Delete,
			namespace: "default",
			objects:   []client.Object{},
			allowed:   true,
		},

		{
			name:      "create with FullStackVPC VPC",
			operation: admissionv1.Create,
			namespace: "default",
			objects: []client.Object{
				&v1alpha1.NetworkInfo{
					ObjectMeta: v1.ObjectMeta{Name: "test", Namespace: "default"},
					VPCs: []v1alpha1.VPCState{
						{NetworkStack: "FullStackVPC"},
					},
				},
			},
			allowed: true,
		},
		{
			name:      "create with VLANBackedVPC VPC denied",
			operation: admissionv1.Create,
			namespace: "default",
			objects: []client.Object{
				&v1alpha1.NetworkInfo{
					ObjectMeta: v1.ObjectMeta{Name: "test", Namespace: "default"},
					VPCs: []v1alpha1.VPCState{
						{NetworkStack: "VLANBackedVPC"},
					},
				},
			},
			allowed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			v1alpha1.AddToScheme(scheme)
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.objects...).Build()

			validator := &StaticRouteValidator{
				Client:  client,
				decoder: admission.NewDecoder(scheme),
			}
			sr1, _ := json.Marshal(&v1alpha1.StaticRoute{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "ns1",
					Name:      "sr1",
				},
				Spec: v1alpha1.StaticRouteSpec{
					Network:  "192.168.0.1/28",
					NextHops: []v1alpha1.NextHop{{IPAddress: "10.0.0.1"}},
				},
			})
			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation: tt.operation,
					Namespace: tt.namespace,
					Name:      "test-sr",
					Object:    runtime.RawExtension{Raw: sr1},
				},
			}

			response := validator.Handle(context.Background(), req)

			if response.Allowed != tt.allowed {
				t.Errorf("Handle() allowed = %v, want %v", response.Allowed, tt.allowed)
			}
		})
	}
	t.Run("create when networkinfo list fails returns 503", func(t *testing.T) {
		patches := gomonkey.ApplyFunc(common.CheckNetworkStack, func(_ client.Client, _ context.Context, ns string, _ string) error {
			return fmt.Errorf("%w in namespace %s: %v", common.ErrFailedToListNetworkInfo, ns, fmt.Errorf("mock list error"))
		})
		defer patches.Reset()

		scheme := runtime.NewScheme()
		v1alpha1.AddToScheme(scheme)
		validator := &StaticRouteValidator{Client: fake.NewClientBuilder().WithScheme(scheme).Build(), decoder: admission.NewDecoder(scheme)}
		sr1, _ := json.Marshal(&v1alpha1.StaticRoute{ObjectMeta: v1.ObjectMeta{Namespace: "ns1", Name: "sr1"}})
		req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Namespace: "ns1", Object: runtime.RawExtension{Raw: sr1}}}
		resp := validator.Handle(context.Background(), req)
		if resp.Result == nil || resp.Result.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %+v", resp)
		}
	})
}
