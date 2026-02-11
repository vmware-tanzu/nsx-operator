package util

import (
	"reflect"
	"sync"
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

func TestSetDetail(t *testing.T) {
	impl := &nsxErrorImpl{}
	detail := &ErrorDetail{
		RelatedErrorCodes:  []int{101},
		RelatedStatusCodes: []string{"INITIAL_STATUS"},
	}

	impl.setDetail(detail)

	if len(impl.ErrorDetail.RelatedErrorCodes) != 1 || impl.ErrorDetail.RelatedErrorCodes[0] != 101 {
		t.Errorf("Expected RelatedErrorCode [101], got %v", impl.ErrorDetail.RelatedErrorCodes)
	}
	if len(impl.ErrorDetail.RelatedStatusCodes) != 1 || impl.ErrorDetail.RelatedStatusCodes[0] != "INITIAL_STATUS" {
		t.Errorf("Expected RelatedStatusCodes ['INITIAL_STATUS'], got %v", impl.ErrorDetail.RelatedStatusCodes)
	}

	// race condition test
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		impl.setDetail(detail)
		wg.Done()
	}()
	impl.setDetail(detail)
}
