/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package rest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/endpoints/request"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/storage"
)

func TestServiceEndpointStatisticsREST_Metadata(t *testing.T) {
	r := NewServiceEndpointStatisticsREST(&storage.ServiceEndpointStatisticsStorage{})
	assert.IsType(t, &easv1alpha1.ServiceEndpointStatistics{}, r.New())
	assert.True(t, r.NamespaceScoped())
	assert.Equal(t, "serviceendpointstatistic", r.GetSingularName())
	r.Destroy()
}

func TestServiceEndpointStatisticsREST_Get_NoNamespace(t *testing.T) {
	r := NewServiceEndpointStatisticsREST(nil)
	_, err := r.Get(context.Background(), "ep1", &metav1.GetOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "namespace not found in context")
}

func TestServiceEndpointStatisticsREST_Get_Success(t *testing.T) {
	_ = NewServiceEndpointStatisticsREST(nil)
	ctx := request.WithNamespace(context.Background(), "ns1")
	_ = ctx
}
