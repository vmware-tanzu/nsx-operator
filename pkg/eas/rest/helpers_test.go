/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package rest

import (
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
)

// fakeVPCInfoProvider is a test double for eas.VPCInfoProvider.
type fakeVPCInfoProvider struct {
	namespaces []string
}

func (f fakeVPCInfoProvider) ListVPCInfo(string) []eas.VPCEntry { return nil }
func (f fakeVPCInfoProvider) ListAllVPCNamespaces() []string    { return f.namespaces }

func newTestFakeK8sClient() *fake.ClientBuilder {
	s := runtime.NewScheme()
	_ = vpcv1alpha1.AddToScheme(s)
	return fake.NewClientBuilder().WithScheme(s)
}
