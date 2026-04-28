/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestWriteError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		message    string
		reason     metav1.StatusReason
	}{
		{
			name:       "not found",
			statusCode: http.StatusNotFound,
			message:    "unknown resource: demo",
			reason:     metav1.StatusReasonNotFound,
		},
		{
			name:       "internal error",
			statusCode: http.StatusInternalServerError,
			message:    "backend exploded",
			reason:     metav1.StatusReasonInternalError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()

			writeError(recorder, tt.statusCode, tt.message)

			require.Equal(t, tt.statusCode, recorder.Code)
			require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))

			var status metav1.Status
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &status))
			assert.Equal(t, "Status", status.Kind)
			assert.Equal(t, metav1.Unversioned.String(), status.APIVersion)
			assert.Equal(t, metav1.StatusFailure, status.Status)
			assert.Equal(t, tt.reason, status.Reason)
			assert.Equal(t, tt.message, status.Message)
			assert.Equal(t, int32(tt.statusCode), status.Code)
		})
	}
}

func TestWriteResponseAsTableIncludesMetadataObject(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/apis/eas.nsx.vmware.com/v1alpha1/subnetippools", nil)
	req.Header.Set("Accept", "application/json;as=Table;g=meta.k8s.io;v=v1")
	recorder := httptest.NewRecorder()

	writeResponse(recorder, req, map[string]string{"name": "ignored"}, []metav1.TableColumnDefinition{
		{Name: "Name", Type: "string", Format: "name"},
		{Name: "RESULTS", Type: "string"},
	}, []metav1.TableRow{
		tableRow("pool-1", "ns-1", "summary"),
	})

	require.Equal(t, http.StatusOK, recorder.Code)

	var table metav1.Table
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &table))
	assert.Equal(t, "Table", table.Kind)
	assert.Equal(t, metav1.SchemeGroupVersion.String(), table.APIVersion)
	require.Len(t, table.Rows, 1)
	assert.Equal(t, []interface{}{"pool-1", "summary"}, table.Rows[0].Cells)

	var metadata metav1.PartialObjectMetadata
	require.NoError(t, json.Unmarshal(table.Rows[0].Object.Raw, &metadata))
	assert.Equal(t, "PartialObjectMetadata", metadata.Kind)
	assert.Equal(t, metav1.SchemeGroupVersion.String(), metadata.APIVersion)
	assert.Equal(t, "pool-1", metadata.Name)
	assert.Equal(t, "ns-1", metadata.Namespace)
}
