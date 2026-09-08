/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	apierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func TestValidateDNSHostname(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		wantErr  bool
	}{
		{
			name:     "valid simple hostname",
			hostname: "test-pod.qe",
			wantErr:  false,
		},
		{
			name:     "valid multi-label hostname",
			hostname: "my-app.subdomain.example.com",
			wantErr:  false,
		},
		{
			name:     "valid with trailing dot",
			hostname: "test-pod.qe.",
			wantErr:  false,
		},
		{
			name:     "empty hostname",
			hostname: "",
			wantErr:  true,
		},
		{
			name:     "empty label",
			hostname: "test..qe",
			wantErr:  true,
		},
		{
			name:     "label exceeds 63 characters",
			hostname: strings.Repeat("a", 64) + ".qe",
			wantErr:  true,
		},
		{
			name:     "label exactly 63 characters",
			hostname: strings.Repeat("a", 63) + ".qe",
			wantErr:  false,
		},
		{
			name:     "FQDN exceeds 255 characters",
			hostname: strings.Repeat("a.", 130), // > 255 chars
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDNSHostname(tt.hostname)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBuildDNSHostname(t *testing.T) {
	assert.Equal(t, "test-pod.qe", BuildDNSHostname("Test-Pod", "QE"))
	assert.Equal(t, "app-1.ns-dev", BuildDNSHostname("app-1", "ns-dev"))
}

func TestIsNSXConflictError(t *testing.T) {
	assert.False(t, IsNSXConflictError(nil))
	assert.False(t, IsNSXConflictError(errors.New("generic error")))
	assert.False(t, IsNSXConflictError(errors.New("HTTP 412 Precondition Failed: concurrent_change")))

	// Direct 409 error string
	assert.True(t, IsNSXConflictError(errors.New("HTTP 409 Conflict")))
	assert.True(t, IsNSXConflictError(errors.New("DNS name already exists")))

	// NSXApiError with AlreadyExists
	errType := apierrors.ErrorType_ALREADY_EXISTS
	nsxErr := NewNSXApiError(&model.ApiError{}, errType)
	assert.True(t, IsNSXConflictError(nsxErr))

	// NSXApiError with ErrorCode 409
	code409 := int64(409)
	nsxErrCode := NewNSXApiError(&model.ApiError{ErrorCode: &code409}, apierrors.ErrorType_ERROR)
	assert.True(t, IsNSXConflictError(nsxErrCode))

	// ObjectAlreadyExists
	assert.True(t, IsNSXConflictError(CreateObjectAlreadyExists("DNS")))
}

func TestIsNSXBadRequestError(t *testing.T) {
	assert.False(t, IsNSXBadRequestError(nil))
	assert.False(t, IsNSXBadRequestError(errors.New("generic error")))

	// Direct 400 error string
	assert.True(t, IsNSXBadRequestError(errors.New("HTTP 400 Bad Request")))

	// NSXApiError with InvalidRequest
	errType := apierrors.ErrorType_INVALID_REQUEST
	nsxErr := NewNSXApiError(&model.ApiError{}, errType)
	assert.True(t, IsNSXBadRequestError(nsxErr))

	// NSXApiError with ErrorCode 400
	code400 := int64(400)
	nsxErrCode := NewNSXApiError(&model.ApiError{ErrorCode: &code400}, apierrors.ErrorType_ERROR)
	assert.True(t, IsNSXBadRequestError(nsxErrCode))
}

func TestClassifyDNSError(t *testing.T) {
	// Success
	outcome := ClassifyDNSError(nil)
	assert.Equal(t, ConditionReasonDNSSuccess, outcome.ConditionReason)
	assert.Equal(t, EventReasonDNSRecordCreated, outcome.EventReason)
	assert.Equal(t, "Normal", outcome.EventType)
	assert.False(t, outcome.IsPermanent)

	// Conflict (409)
	outcome409 := ClassifyDNSError(errors.New("409 Conflict"))
	assert.Equal(t, ConditionReasonDNSConflict, outcome409.ConditionReason)
	assert.Equal(t, EventReasonDNSConflict, outcome409.EventReason)
	assert.Equal(t, "Warning", outcome409.EventType)
	assert.True(t, outcome409.IsPermanent)

	// Bad Request (400)
	outcome400 := ClassifyDNSError(errors.New("400 Bad Request"))
	assert.Equal(t, ConditionReasonDNSInvalidName, outcome400.ConditionReason)
	assert.Equal(t, EventReasonDNSConfigFailed, outcome400.EventReason)
	assert.Equal(t, "Warning", outcome400.EventType)
	assert.True(t, outcome400.IsPermanent)

	// Other NSX error (429/5xx)
	outcome500 := ClassifyDNSError(fmt.Errorf("500 Internal Server Error"))
	assert.Equal(t, ConditionReasonDNSNSXApiError, outcome500.ConditionReason)
	assert.Equal(t, EventReasonDNSNSXApiError, outcome500.EventReason)
	assert.Equal(t, "Warning", outcome500.EventType)
	assert.False(t, outcome500.IsPermanent)
}
