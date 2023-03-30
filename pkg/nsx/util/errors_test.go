package util

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
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
