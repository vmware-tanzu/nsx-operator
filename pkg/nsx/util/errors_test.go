package util

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func TestExcept(t *testing.T) {
	notFound := CreateResourceNotFound("192.168.1.1", "ippool")
	assert.Equal(t, 0, notFound.ErrorCode)
}

func TestCreateFunc(t *testing.T) {
	funcMap := map[string]interface{}{
		"CreateObjectAlreadyExists":                 CreateObjectAlreadyExists,
		"CreateNotImplemented":                      CreateNotImplemented,
		"CreateObjectNotGenerated":                  CreateObjectNotGenerated,
		"CreateCertificateError":                    CreateCertificateError,
		"CreateNsxLibInvalidInput":                  CreateNsxLibInvalidInput,
		"CreateGeneralManagerError":                 CreateGeneralManagerError,
		"CreateResourceNotFound":                    CreateResourceNotFound,
		"CreateMultipleResourcesFound":              CreateMultipleResourcesFound,
		"CreateBackendResourceNotFound":             CreateBackendResourceNotFound,
		"CreateInvalidInput":                        CreateInvalidInput,
		"CreateRealizationError":                    CreateRealizationError,
		"CreateRealizationErrorStateError":          CreateRealizationErrorStateError,
		"CreateRealizationTimeoutError":             CreateRealizationTimeoutError,
		"CreateDetailedRealizationTimeoutError":     CreateDetailedRealizationTimeoutError,
		"CreateStaleRevision":                       CreateStaleRevision,
		"CreateGeneralServerBusy":                   CreateGeneralServerBusy,
		"CreateTooManyRequests":                     CreateTooManyRequests,
		"CreateServiceUnavailable":                  CreateServiceUnavailable,
		"CreateClientCertificateNotTrusted":         CreateClientCertificateNotTrusted,
		"CreateBadXSRFToken":                        CreateBadXSRFToken,
		"CreateInvalidCredentials":                  CreateInvalidCredentials,
		"CreateInvalidLicense":                      CreateInvalidLicense,
		"CreateBadJSONWebTokenProviderRequest":      CreateBadJSONWebTokenProviderRequest,
		"CreateServiceClusterUnavailable":           CreateServiceClusterUnavailable,
		"CreateNSGroupMemberNotFound":               CreateNSGroupMemberNotFound,
		"CreateNSGroupIsFull":                       CreateNSGroupIsFull,
		"CreateSecurityGroupMaximumCapacityReached": CreateSecurityGroupMaximumCapacityReached,
		"CreateNsxSearchInvalidQuery":               CreateNsxSearchInvalidQuery,
		"CreateGeneralNsxSearchError":               CreateGeneralNsxSearchError,
		"CreateNsxIndexingInProgress":               CreateNsxIndexingInProgress,
		"CreateNsxSearchTimeout":                    CreateNsxSearchTimeout,
		"CreateNsxSearchOutOfSync":                  CreateNsxSearchOutOfSync,
		"CreateNsxPendingDelete":                    CreateNsxPendingDelete,
		"CreateNsxSegmentWithVM":                    CreateNsxSegmentWithVM,
		"CreateNsxOverlapAddresses":                 CreateNsxOverlapAddresses,
		"CreateNsxOverlapVlan":                      CreateNsxOverlapVlan,
		"CreateAPITransactionAborted":               CreateAPITransactionAborted,
		"CreateCannotConnectToServer":               CreateCannotConnectToServer,
		"CreateResourceInUse":                       CreateResourceInUse,
		"CreateTimeout":                             CreateTimeout,
		"CreateConnectionError":                     CreateConnectionError,
	}
	for name := range funcMap {
		val := reflect.ValueOf(funcMap[name])
		typ := val.Type()
		in := make([]reflect.Value, 0)
		for i := 0; i < typ.NumIn(); i++ {
			in = append(in, reflect.ValueOf("go"))
		}
		values := val.Call(in)
		assert.NotEqual(t, values[0], nil)
	}
}

func TestMarkerInterfaceMethods(t *testing.T) {
	// Test that marker interface methods exist and can be called
	// These methods are used for type assertions

	// Test NsxLibInvalidInput marker
	var nsxLibInvalidInput NsxLibInvalidInput = &GeneralNsxLibInvalidInput{}
	_ = nsxLibInvalidInput

	// Test ManagerError marker
	var managerError ManagerError = &GeneralManagerError{}
	_ = managerError

	// Test ServerBusy marker
	var serverBusy ServerBusy = &GeneralServerBusy{}
	_ = serverBusy

	// Test NsxSearchError marker
	var nsxSearchError NsxSearchError = &GeneralSearchError{}
	_ = nsxSearchError
}

func TestSimpleErrorMethods(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "PageMaxError",
			err:      PageMaxError{Desc: "page max error"},
			expected: "page max error",
		},
		{
			name:     "PodIPNotFound",
			err:      PodIPNotFound{Desc: "pod IP not found"},
			expected: "pod IP not found",
		},
		{
			name:     "PodNotRunning",
			err:      PodNotRunning{Desc: "pod not running"},
			expected: "pod not running",
		},
		{
			name:     "NoEffectiveOption",
			err:      NoEffectiveOption{Desc: "no effective option"},
			expected: "no effective option",
		},
		{
			name:     "RestrictionError",
			err:      RestrictionError{Desc: "restriction error"},
			expected: "restriction error",
		},
		{
			name:     "IPBlockAllExhaustedError",
			err:      IPBlockAllExhaustedError{Desc: "IP block exhausted"},
			expected: "IP block exhausted",
		},
		{
			name:     "ExceedTagsError",
			err:      ExceedTagsError{Desc: "exceed tags"},
			expected: "exceed tags",
		},
		{
			name:     "ValidationError",
			err:      ValidationError{Desc: "validation error"},
			expected: "validation error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.err.Error())
		})
	}
}

func TestStatusError(t *testing.T) {
	tests := []struct {
		name     string
		status   Status
		expected string
	}{
		{
			name:     "ValidationFailed",
			status:   ValidationFailed,
			expected: "failed to validate config",
		},
		{
			name:     "GetNSXClientFailed",
			status:   GetNSXClientFailed,
			expected: "failed to get nsx client",
		},
		{
			name:     "InitCleanupServiceFailed",
			status:   InitCleanupServiceFailed,
			expected: "failed to initialize cleanup service",
		},
		{
			name:     "CleanupResourceFailed",
			status:   CleanupResourceFailed,
			expected: "failed to clean up",
		},
		{
			name:     "TimeoutFailed",
			status:   TimeoutFailed,
			expected: "failed because of timeout",
		},
		{
			name:     "CustomStatus",
			status:   Status{Code: 999, Message: "custom message"},
			expected: "custom message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.Error())
		})
	}
}

func TestRealizeStateError(t *testing.T) {
	t.Run("NewRealizeStateError", func(t *testing.T) {
		err := NewRealizeStateError("test message", 42)
		assert.NotNil(t, err)
		assert.Equal(t, "test message", err.Error())
		assert.Equal(t, 42, err.GetCode())
	})

	t.Run("IsRealizeStateError", func(t *testing.T) {
		// Test with RealizeStateError
		realizeErr := NewRealizeStateError("test", 1)
		assert.True(t, IsRealizeStateError(realizeErr))

		// Test with non-RealizeStateError
		assert.False(t, IsRealizeStateError(assert.AnError))
		assert.False(t, IsRealizeStateError(nil))
	})
}

func TestRetryRealizeError(t *testing.T) {
	t.Run("NewRetryRealizeError", func(t *testing.T) {
		err := NewRetryRealizeError("retry message")
		assert.NotNil(t, err)
		assert.Equal(t, "retry message", err.Error())
	})
}

func TestIsRetryRealizeError(t *testing.T) {
	tests := []struct {
		name     string
		alarm    model.PolicyAlarmResource
		expected bool
	}{
		{
			name: "ProviderNotReady error - should retry",
			alarm: model.PolicyAlarmResource{
				ErrorDetails: &model.PolicyApiError{
					ErrorCode: int64Ptr(ProviderNotReadyErrorCode),
				},
			},
			expected: true,
		},
		{
			name: "Different error code - should not retry",
			alarm: model.PolicyAlarmResource{
				ErrorDetails: &model.PolicyApiError{
					ErrorCode: int64Ptr(999),
				},
			},
			expected: false,
		},
		{
			name: "Nil ErrorDetails - should not retry",
			alarm: model.PolicyAlarmResource{
				ErrorDetails: nil,
			},
			expected: false,
		},
		{
			name: "Nil ErrorCode - should not retry",
			alarm: model.PolicyAlarmResource{
				ErrorDetails: &model.PolicyApiError{
					ErrorCode: nil,
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRetryRealizeError(tt.alarm)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRetryAfterError(t *testing.T) {
	delErr := CreateNsxPendingDelete()
	assert.Equal(t, DefaultPendingDeleteRetryAfterSeconds, delErr.RetryAfterSeconds())
	assert.Equal(t, 300, delErr.RetryAfterSeconds())

	// Interface check
	var retriable RetryAfterError = delErr
	assert.Equal(t, 300, retriable.RetryAfterSeconds())

	// AsRetryAfterError direct
	r, ok := AsRetryAfterError(delErr)
	assert.True(t, ok)
	assert.Equal(t, 300, r.RetryAfterSeconds())

	// AsRetryAfterError wrapped
	wrapped := fmt.Errorf("wrapped context: %w", delErr)
	r, ok = AsRetryAfterError(wrapped)
	assert.True(t, ok)
	assert.Equal(t, 300, r.RetryAfterSeconds())

	// Non-retriable error
	r, ok = AsRetryAfterError(errors.New("other error"))
	assert.False(t, ok)
	assert.Nil(t, r)
}

func TestConvertToRetryAfterError(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	// Nil error
	r, ok := ConvertToRetryAfterError(nil)
	assert.False(t, ok)
	assert.Nil(t, r)

	// Non-retriable error
	r, ok = ConvertToRetryAfterError(errors.New("generic error"))
	assert.False(t, ok)
	assert.Nil(t, r)

	// Direct NsxPendingDelete
	delErr := CreateNsxPendingDelete()
	r, ok = ConvertToRetryAfterError(delErr)
	assert.True(t, ok)
	assert.Equal(t, 300, r.RetryAfterSeconds())

	// Wrapped NsxPendingDelete
	r, ok = ConvertToRetryAfterError(fmt.Errorf("wrap: %w", delErr))
	assert.True(t, ok)
	assert.Equal(t, 300, r.RetryAfterSeconds())

	// NSXApiError with 500045
	apiErr := &NSXApiError{
		ApiError: &model.ApiError{
			ErrorCode:    int64Ptr(PendingDeleteErrorCode),
			ErrorMessage: strPtr("object marked for deletion"),
		},
	}
	r, ok = ConvertToRetryAfterError(apiErr)
	assert.True(t, ok)
	assert.Equal(t, 300, r.RetryAfterSeconds())
	assert.Equal(t, "object marked for deletion", r.Error())

	// Wrapped NSXApiError with 500045
	r, ok = ConvertToRetryAfterError(fmt.Errorf("failed: %w", apiErr))
	assert.True(t, ok)
	assert.Equal(t, 300, r.RetryAfterSeconds())

	// NSXApiError with RelatedErrors containing 500045
	relApiErr := &NSXApiError{
		ApiError: &model.ApiError{
			ErrorCode: int64Ptr(100),
			RelatedErrors: []model.RelatedApiError{
				{
					ErrorCode:    int64Ptr(PendingDeleteErrorCode),
					ErrorMessage: strPtr("related object marked for deletion"),
				},
			},
		},
	}
	r, ok = ConvertToRetryAfterError(relApiErr)
	assert.True(t, ok)
	assert.Equal(t, 300, r.RetryAfterSeconds())

	// NSXApiError with other error code
	otherApiErr := &NSXApiError{ApiError: &model.ApiError{ErrorCode: int64Ptr(99999)}}
	r, ok = ConvertToRetryAfterError(otherApiErr)
	assert.False(t, ok)
	assert.Nil(t, r)
}

// Helper function
func int64Ptr(i int64) *int64 {
	return &i
}

func TestExtractAllErrorCodes(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		assert.Nil(t, ExtractAllErrorCodes(nil))
	})

	t.Run("from NSXApiError with related errors", func(t *testing.T) {
		mainCode := int64(502001)
		relCode1 := int64(502103)
		relCode2 := int64(10087)
		apiErr := &NSXApiError{
			ApiError: &model.ApiError{
				ErrorCode: &mainCode,
				RelatedErrors: []model.RelatedApiError{
					{ErrorCode: &relCode1},
					{ErrorCode: &relCode2},
				},
			},
		}
		codes := ExtractAllErrorCodes(apiErr)
		assert.Equal(t, []int64{502001, 502103, 10087}, codes)

		// Test wrapped error with fmt.Errorf %w
		wrappedErr := fmt.Errorf("wrapped context: %w", apiErr)
		assert.Equal(t, []int64{502001, 502103, 10087}, ExtractAllErrorCodes(wrappedErr))
	})

	t.Run("from NSXApiError with nil ApiError", func(t *testing.T) {
		apiErr := &NSXApiError{
			ApiError: nil,
		}
		codes := ExtractAllErrorCodes(apiErr)
		assert.Nil(t, codes)
	})

	t.Run("from RealizeStateError", func(t *testing.T) {
		realizeErr := NewRealizeStateError("alarm occurred", 10087, 502103)
		codes := ExtractAllErrorCodes(realizeErr)
		assert.Equal(t, []int64{10087, 502103}, codes)

		// Test wrapped error with fmt.Errorf %w
		wrappedRealizeErr := fmt.Errorf("controller failed: %w", realizeErr)
		assert.Equal(t, []int64{10087, 502103}, ExtractAllErrorCodes(wrappedRealizeErr))
	})

	t.Run("from GeneralNsxError", func(t *testing.T) {
		generalErr := &GeneralNsxError{
			nsxErrorImpl: nsxErrorImpl{
				ErrorDetail: ErrorDetail{
					ErrorCode:         502001,
					RelatedErrorCodes: []int{502103, 10087},
				},
			},
		}
		codes := ExtractAllErrorCodes(generalErr)
		assert.Equal(t, []int64{502001, 502103, 10087}, codes)

		// Test wrapped error with fmt.Errorf %w
		wrappedGeneralErr := fmt.Errorf("operation failed: %w", generalErr)
		assert.Equal(t, []int64{502001, 502103, 10087}, ExtractAllErrorCodes(wrappedGeneralErr))
	})

	t.Run("unstructured error returns nil", func(t *testing.T) {
		err := fmt.Errorf("generic connection error")
		assert.Nil(t, ExtractAllErrorCodes(err))
	})

	t.Run("priority order matching in GetFirstMatchingErrorCode", func(t *testing.T) {
		// RealizeStateError has 10087 as main code, and 502105 in related codes.
		// When targetCodes defines 502105 before 10087, 502105 must be returned!
		realizeErr := NewRealizeStateError("alarm occurred", 10087, 502105)
		matched := GetFirstMatchingErrorCode(realizeErr, 502105, 10087)
		assert.Equal(t, int64(502105), matched)

		// When targetCodes defines 10087 before 502105, 10087 must be returned.
		matchedReverse := GetFirstMatchingErrorCode(realizeErr, 10087, 502105)
		assert.Equal(t, int64(10087), matchedReverse)
	})
}

func TestIsLBSEdgeCapacityError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		expected     bool
		expectedCode int64
	}{
		{
			name:         "nil error",
			err:          nil,
			expected:     false,
			expectedCode: 0,
		},
		{
			name: "NSXApiError with 502103 in related errors",
			err: func() error {
				mainCode := int64(502001)
				relCode := int64(LBSEdgeNodeNoCapacityForSizeErrorCode)
				return &NSXApiError{
					ApiError: &model.ApiError{
						ErrorCode: &mainCode,
						RelatedErrors: []model.RelatedApiError{
							{ErrorCode: &relCode},
						},
					},
				}
			}(),
			expected:     true,
			expectedCode: LBSEdgeNodeNoCapacityForSizeErrorCode,
		},
		{
			name: "502105 size not supported on edge node",
			err: func() error {
				code := int64(LBSEdgeNodeSizeNotSupportedErrorCode)
				return &NSXApiError{
					ApiError: &model.ApiError{
						ErrorCode: &code,
					},
				}
			}(),
			expected:     true,
			expectedCode: LBSEdgeNodeSizeNotSupportedErrorCode,
		},
		{
			name:         "10087 routing allocation insufficient resources",
			err:          NewRealizeStateError("failed", int(EdgeClusterInsufficientResourcesErrorCode)),
			expected:     true,
			expectedCode: EdgeClusterInsufficientResourcesErrorCode,
		},
		{
			name:         "10145 no member capacity pool",
			err:          NewRealizeStateError("no capacity pool", int(EdgeClusterNoCapacityPoolErrorCode)),
			expected:     true,
			expectedCode: EdgeClusterNoCapacityPoolErrorCode,
		},
		{
			name:         "unrelated error code",
			err:          NewRealizeStateError("provider not ready", 500042),
			expected:     false,
			expectedCode: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsLBSEdgeCapacityError(tt.err))
			assert.Equal(t, tt.expectedCode, GetLBSEdgeCapacityErrorCode(tt.err))
		})
	}
}
