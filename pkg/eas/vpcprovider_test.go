/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package eas

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(vpcv1alpha1.AddToScheme(s))
	return s
}

const testVPCPath = "/orgs/o1/projects/proj1/vpcs/vpc-xyz"

func TestK8sVPCInfoProvider_ListVPCInfo_FromNamespaceAnnotation(t *testing.T) {
	scheme := testScheme(t)
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-1",
			Annotations: map[string]string{
				annotationVPCNetworkConfig: "nc-annotated",
			},
		},
	}
	nc := &vpcv1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "nc-annotated"},
		Spec:       vpcv1alpha1.VPCNetworkConfigurationSpec{VPC: testVPCPath},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns, nc).Build()
	p := NewK8sVPCInfoProvider(c)

	infos := p.ListVPCInfo("tenant-1")
	require.Len(t, infos, 1)
	assert.Equal(t, "o1", infos[0].OrgID)
	assert.Equal(t, "proj1", infos[0].ProjectID)
	assert.Equal(t, "vpc-xyz", infos[0].VPCID)
}

func TestK8sVPCInfoProvider_ListVPCInfo_DefaultNetworkConfig(t *testing.T) {
	scheme := testScheme(t)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "no-annotation"}}
	defaultNC := &vpcv1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default-nc",
			Annotations: map[string]string{
				annotationDefaultConfig: "true",
			},
		},
		Spec: vpcv1alpha1.VPCNetworkConfigurationSpec{VPC: testVPCPath},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns, defaultNC).Build()
	p := NewK8sVPCInfoProvider(c)

	infos := p.ListVPCInfo("no-annotation")
	require.Len(t, infos, 1)
	assert.Equal(t, "vpc-xyz", infos[0].VPCID)
}

func TestK8sVPCInfoProvider_ListVPCInfo_MissingNamespace(t *testing.T) {
	scheme := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	p := NewK8sVPCInfoProvider(c)

	assert.Empty(t, p.ListVPCInfo("nonexistent"))
}

func TestK8sVPCInfoProvider_ExtractVPCInfoFromNC_StatusVPCs(t *testing.T) {
	p := &K8sVPCInfoProvider{}
	nc := &vpcv1alpha1.VPCNetworkConfiguration{
		Status: vpcv1alpha1.VPCNetworkConfigurationStatus{
			VPCs: []vpcv1alpha1.VPCInfo{
				{VPCPath: testVPCPath},
			},
		},
	}
	infos := p.extractVPCInfoFromNC(nc)
	require.Len(t, infos, 1)
	assert.Equal(t, "o1", infos[0].OrgID)
	assert.Equal(t, "proj1", infos[0].ProjectID)
}

func TestK8sVPCInfoProvider_ExtractVPCInfoFromNC_SpecTakesPrecedence(t *testing.T) {
	p := &K8sVPCInfoProvider{}
	nc := &vpcv1alpha1.VPCNetworkConfiguration{
		Spec: vpcv1alpha1.VPCNetworkConfigurationSpec{VPC: testVPCPath},
		Status: vpcv1alpha1.VPCNetworkConfigurationStatus{
			VPCs: []vpcv1alpha1.VPCInfo{{VPCPath: "/orgs/other/projects/p2/vpcs/other"}},
		},
	}
	infos := p.extractVPCInfoFromNC(nc)
	require.Len(t, infos, 1)
	assert.Equal(t, "vpc-xyz", infos[0].VPCID)
}

func TestK8sVPCInfoProvider_ExtractVPCInfoFromNC_ParseErrorSkipped(t *testing.T) {
	p := &K8sVPCInfoProvider{}
	nc := &vpcv1alpha1.VPCNetworkConfiguration{
		Spec: vpcv1alpha1.VPCNetworkConfigurationSpec{VPC: "not-a-valid-path"},
	}
	assert.Empty(t, p.extractVPCInfoFromNC(nc))
}

// Compile-time check that *K8sVPCInfoProvider implements VPCInfoProvider.
var _ VPCInfoProvider = (*K8sVPCInfoProvider)(nil)
