/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"errors"
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

const (
	InvalidLicenseErrorCode                   = 505
	ProviderNotReadyErrorCode                 = 500042
	PendingDeleteErrorCode                    = 500045
	IPAllocationErrorCode                     = 8212
	IPPoolExhaustedErrorCode                  = 520054
	ReservedIPRangesOverlappedErrorCode       = 508134
	ReservedIPRangesOutOfSubnetRangeErrorCode = 508135
	VpcOverlapVlanErrorCode                   = 640873
	// MixedModeNotSupportedErrorCode is returned by NSX when both
	// static_ip_allocation.enabled=true and subnet_dhcp_config.mode=DHCP_SERVER
	// are set simultaneously and the NSX version does not support mixed-mode subnets.
	MixedModeNotSupportedErrorCode = 508128
	// mixedModeIPAllocationErrorMin and mixedModeIPAllocationErrorMax define the
	// inclusive NSX error-code range reserved for VPC Subnet mixed IP allocation
	// validation failures (overlapping pool ranges, ranges outside CIDR, conflicts
	// with reserved ranges, etc.). All codes in this range are permanent user-
	// configuration errors; the controller should not requeue on them.
	mixedModeIPAllocationErrorMin = 660000
	mixedModeIPAllocationErrorMax = 660011

	// Policy Load Balancer Service capacity error codes.
	LBSEdgeNodeNoCapacityForSizeErrorCode = 502103
	LBSEdgeNodeSizeNotSupportedErrorCode  = 502105

	// Edge routing allocation error codes.
	EdgeClusterInsufficientResourcesErrorCode = 10087
	EdgeClusterNoCapacityPoolErrorCode        = 10145
)

// IsMixedModeIPAllocationError reports whether code is an NSX error in the
// 660000–660011 range (VPC Subnet mixed IP allocation validation failures).
// These are permanent user-configuration errors that the controller must not
// requeue.
func IsMixedModeIPAllocationError(code int64) bool {
	return code >= mixedModeIPAllocationErrorMin && code <= mixedModeIPAllocationErrorMax
}

type NsxError interface {
	setDetail(detail *ErrorDetail)
	Error() string
}

type nsxErrorImpl struct {
	ErrorDetail
	msg string
}

type GeneralNsxError struct {
	nsxErrorImpl
}

func (impl *nsxErrorImpl) setDetail(detail *ErrorDetail) {
	impl.ErrorDetail = *detail
	if len(detail.RelatedErrorCodes) > 0 {
		impl.ErrorDetail.RelatedErrorCodes = append([]int{}, detail.RelatedErrorCodes...)
	}
	if len(detail.RelatedStatusCodes) > 0 {
		impl.ErrorDetail.RelatedStatusCodes = append([]string{}, detail.RelatedStatusCodes...)
	}
}

func (impl *nsxErrorImpl) Error() string {
	if impl.ErrorDetail.StatusCode != 0 {
		return impl.msg + impl.ErrorDetail.Error()
	}
	return impl.msg
}

// ObjectAlreadyExists means object already exsists on the backend
type ObjectAlreadyExists struct {
	nsxErrorImpl
}

func CreateObjectAlreadyExists(objectType string) *ObjectAlreadyExists {
	m := fmt.Sprintf("%s already exists", objectType)
	nsxErr := &ObjectAlreadyExists{}
	nsxErr.msg = m
	return nsxErr
}

type NotImplemented struct {
	nsxErrorImpl
}

func CreateNotImplemented(operation string) *NotImplemented {
	m := fmt.Sprintf("%s is not supported", operation)
	nsxErr := &NotImplemented{}
	nsxErr.msg = m
	return nsxErr
}

type ObjectNotGenerated struct {
	nsxErrorImpl
}

func CreateObjectNotGenerated(objectType string) *ObjectNotGenerated {
	m := fmt.Sprintf("%s was not generated", objectType)
	nsxErr := &ObjectNotGenerated{}
	nsxErr.msg = m
	return nsxErr
}

type CertificateError struct {
	nsxErrorImpl
}

func CreateCertificateError(msg string) *CertificateError {
	m := fmt.Sprintf("Certificate error: %s", msg)
	nsxErr := &CertificateError{}
	nsxErr.msg = m
	return nsxErr
}

type NsxLibInvalidInput interface {
	NsxError
	nsxLibInvalidInput()
}

type NsxLibInvalidInputImpl struct {
	nsxErrorImpl
}

type GeneralNsxLibInvalidInput struct {
	NsxLibInvalidInputImpl
}

func (NsxLibInvalidInputImpl) nsxLibInvalidInput() {}

func CreateNsxLibInvalidInput(errorMessage string) *GeneralNsxLibInvalidInput {
	m := fmt.Sprintf("Invalid input for operation: %s.", errorMessage)
	nsxErr := &GeneralNsxLibInvalidInput{}
	nsxErr.msg = m
	return nsxErr
}

type ManagerError interface {
	NsxError
	managerError()
}
type managerErrorImpl struct {
	nsxErrorImpl
}

func (impl managerErrorImpl) managerError() {}

type GeneralManagerError struct {
	managerErrorImpl
}

func CreateGeneralManagerError(manager string, operation string, details string) *GeneralManagerError {
	m := fmt.Sprintf("Unexpected error from backend manager (%s) for %s%s", manager, operation, details)
	nsxErr := &GeneralManagerError{}
	nsxErr.msg = m
	return nsxErr
}

// ResourceNotFound indicates resource not found by backend
type ResourceNotFound struct {
	managerErrorImpl
}

func CreateResourceNotFound(manager string, operation string) *ResourceNotFound {
	m := fmt.Sprintf("Resource could not be found on backend (%s) for %s", manager, operation)
	nsxErr := &ResourceNotFound{}
	nsxErr.msg = m
	return nsxErr
}

type MultipleResourcesFound struct {
	managerErrorImpl
}

func CreateMultipleResourcesFound(manager string, operation string) *MultipleResourcesFound {
	m := fmt.Sprintf("Multiple resources are found on backend (%s) for %s, where only one is expected", manager, operation)
	nsxErr := &MultipleResourcesFound{}
	nsxErr.msg = m
	return nsxErr
}

type BackendResourceNotFound struct {
	managerErrorImpl
}

func CreateBackendResourceNotFound(details string, manager string, operation string) BackendResourceNotFound {
	m := fmt.Sprintf("%s On backend (%s) with Operation: %s", details, manager, operation)
	nsxErr := BackendResourceNotFound{}
	nsxErr.msg = m
	return nsxErr
}

type InvalidInput struct {
	managerErrorImpl
}

func CreateInvalidInput(operation string, argVal string, argName string) *InvalidInput {
	m := fmt.Sprintf("%s failed: Invalid input %s for %s", operation, argVal, argName)
	nsxErr := &InvalidInput{}
	nsxErr.msg = m
	return nsxErr
}

type RealizationError struct {
	managerErrorImpl
}

func CreateRealizationError(operation string, argVal string, argName string) *RealizationError {
	m := fmt.Sprintf("%s failed: Invalid input %s for %s", operation, argVal, argName)
	nsxErr := &RealizationError{}
	nsxErr.msg = m
	return nsxErr
}

type RealizationErrorStateError struct {
	msg string `parent:"RealizationError"`
}

func CreateRealizationErrorStateError(resourceType string, resourceID string, error string) *RealizationErrorStateError {
	m := fmt.Sprintf("%s ID %s is in ERROR state: %s", resourceType, resourceID, error)
	nsxErr := &RealizationErrorStateError{msg: m}
	return nsxErr
}

type RealizationTimeoutError struct {
	msg string `parent:"RealizationError"`
}

func CreateRealizationTimeoutError(resourceType string, resourceID string, attempts string, sleep string) *RealizationTimeoutError {
	m := fmt.Sprintf("%s ID %s was not realized after %s attempts with %s seconds sleep", resourceType, resourceID, attempts, sleep)
	nsxErr := &RealizationTimeoutError{msg: m}
	return nsxErr
}

type DetailedRealizationTimeoutError struct {
	msg string `parent:"RealizationError"`
}

func CreateDetailedRealizationTimeoutError(resourceType string, resourceID string, realizedType string, relatedType string, relatedID string, attempts string, sleep string) *DetailedRealizationTimeoutError {
	m := fmt.Sprintf("%s ID %s was not realized to %s for %s %s after %s attempts with %s seconds sleep", resourceType, resourceID, realizedType, relatedType, relatedID, attempts, sleep)
	nsxErr := &DetailedRealizationTimeoutError{msg: m}
	return nsxErr
}

type StaleRevision struct {
	managerErrorImpl
}

func CreateStaleRevision(resourceType string, resourceID string, realizedType string, relatedType string, relatedID string, attempts string, sleep string) *StaleRevision {
	m := fmt.Sprintf("%s ID %s was not realized to %s for %s %s after %s attempts with %s seconds sleep", resourceType, resourceID, realizedType, relatedType, relatedID, attempts, sleep)
	nsxErr := &StaleRevision{}
	nsxErr.msg = m
	return nsxErr
}

type ServerBusy interface {
	ManagerError
	serverBusy()
}

type ServerBusyImpl struct {
	managerErrorImpl
	msg string
}

func (ServerBusyImpl) serverBusy() {}

type GeneralServerBusy struct {
	ServerBusyImpl
}

func CreateGeneralServerBusy(resourceType string, resourceID string, realizedType string, relatedType string, relatedID string, attempts string, sleep string) *GeneralServerBusy {
	m := fmt.Sprintf("%s ID %s was not realized to %s for %s %s after %s attempts with %s seconds sleep", resourceType, resourceID, realizedType, relatedType, relatedID, attempts, sleep)
	nsxErr := &GeneralServerBusy{}
	nsxErr.msg = m
	return nsxErr
}

type TooManyRequests struct {
	ServerBusyImpl
}

func CreateTooManyRequests(resourceType string, resourceID string, realizedType string, relatedType string, relatedID string, attempts string, sleep string) *TooManyRequests {
	m := fmt.Sprintf("%s ID %s was not realized to %s for %s %s after %s attempts with %s seconds sleep", resourceType, resourceID, realizedType, relatedType, relatedID, attempts, sleep)
	nsxErr := &TooManyRequests{}
	nsxErr.msg = m
	return nsxErr
}

type ServiceUnavailable struct {
	ServerBusyImpl
}

func CreateServiceUnavailable(resourceType string, resourceID string, realizedType string, relatedType string, relatedID string, attempts string, sleep string) *ServiceUnavailable {
	m := fmt.Sprintf("%s ID %s was not realized to %s for %s %s after %s attempts with %s seconds sleep", resourceType, resourceID, realizedType, relatedType, relatedID, attempts, sleep)
	nsxErr := &ServiceUnavailable{}
	nsxErr.msg = m
	return nsxErr
}

type ClientCertificateNotTrusted struct {
	managerErrorImpl
}

func CreateClientCertificateNotTrusted() *ClientCertificateNotTrusted {
	nsxErr := &ClientCertificateNotTrusted{}
	nsxErr.msg = "Certificate not trusted"
	return nsxErr
}

type BadXSRFToken struct {
	managerErrorImpl
}

func CreateBadXSRFToken() *BadXSRFToken {
	nsxErr := &BadXSRFToken{}
	nsxErr.msg = "Bad or expired XSRF token"
	return nsxErr
}

type InvalidCredentials struct {
	managerErrorImpl
}

func CreateInvalidCredentials(msg string) *InvalidCredentials {
	m := fmt.Sprintf("Failed to authenticate with NSX: %s", msg)
	nsxErr := &InvalidCredentials{}
	nsxErr.msg = m
	return nsxErr
}

type InvalidLicense struct {
	managerErrorImpl
}

func CreateInvalidLicense(msg string) *InvalidLicense {
	m := fmt.Sprintf("No valid License to configure NSX resources: %s", msg)
	nsxErr := &InvalidLicense{}
	nsxErr.msg = m
	return nsxErr
}

type BadJSONWebTokenProviderRequest struct {
	nsxErrorImpl
}

func CreateBadJSONWebTokenProviderRequest(msg string) *BadJSONWebTokenProviderRequest {
	m := fmt.Sprintf("Bad or expired JSON web token request from provider: %s", msg)
	nsxErr := &BadJSONWebTokenProviderRequest{}
	nsxErr.msg = m
	return nsxErr
}

type ServiceClusterUnavailable struct {
	managerErrorImpl
}

func CreateServiceClusterUnavailable(clusterID string) *ServiceClusterUnavailable {
	m := fmt.Sprintf("Service cluster: '%s' is unavailable. Please, check NSX setup and/or configuration", clusterID)
	nsxErr := &ServiceClusterUnavailable{}
	nsxErr.msg = m
	return nsxErr
}

type NSGroupMemberNotFound struct {
	managerErrorImpl
}

func CreateNSGroupMemberNotFound(nsgroupID string, memberID string) *NSGroupMemberNotFound {
	m := fmt.Sprintf("Could not find NSGroup %s member %s for removal.", nsgroupID, memberID)
	nsxErr := &NSGroupMemberNotFound{}
	nsxErr.msg = m
	return nsxErr
}

type NSGroupIsFull struct {
	managerErrorImpl
}

func CreateNSGroupIsFull(nsgroupID string) *NSGroupIsFull {
	m := fmt.Sprintf("NSGroup %s contains has reached its maximum capacity, unable to add additional members.", nsgroupID)
	nsxErr := &NSGroupIsFull{}
	nsxErr.msg = m
	return nsxErr
}

type SecurityGroupMaximumCapacityReached struct {
	managerErrorImpl
}

func CreateSecurityGroupMaximumCapacityReached(sgID string) *SecurityGroupMaximumCapacityReached {
	m := fmt.Sprintf("Security Group %s has reached its maximum capacity, no more ports can be associated with this security-group.", sgID)
	nsxErr := &SecurityGroupMaximumCapacityReached{}
	nsxErr.msg = m
	return nsxErr
}

type NsxSearchInvalidQuery struct {
	nsxErrorImpl
}

func CreateNsxSearchInvalidQuery(reason string) *NsxSearchInvalidQuery {
	m := fmt.Sprintf("Invalid input for NSX search query. Reason: %s", reason)
	nsxErr := &NsxSearchInvalidQuery{}
	nsxErr.msg = m
	return nsxErr
}

type NsxSearchError interface {
	NsxError
	nsxSearchError()
}

type NsxSearchErrorImpl struct {
	nsxErrorImpl
}

func (NsxSearchErrorImpl) nsxSearchError() {}

type GeneralSearchError struct {
	NsxSearchErrorImpl
}

func CreateGeneralNsxSearchError() *GeneralSearchError {
	nsxErr := &GeneralSearchError{}
	nsxErr.msg = "Search failed due to error"
	return nsxErr
}

type NsxIndexingInProgress struct {
	NsxSearchErrorImpl
}

func CreateNsxIndexingInProgress() *NsxIndexingInProgress {
	nsxErr := &NsxIndexingInProgress{}
	nsxErr.msg = "Bad Request due to indexing is in progress, please retry after sometime"
	return nsxErr
}

type NsxSearchTimeout struct {
	NsxSearchErrorImpl
}

func CreateNsxSearchTimeout() *NsxSearchTimeout {
	nsxErr := &NsxSearchTimeout{}
	nsxErr.msg = "Request timed out. This may occur when system is under load or running low on resources"
	return nsxErr
}

type NsxSearchOutOfSync struct {
	NsxSearchErrorImpl
}

func CreateNsxSearchOutOfSync() *NsxSearchOutOfSync {
	nsxErr := &NsxSearchOutOfSync{}
	nsxErr.msg = "Index is currently out of sync"
	return nsxErr
}

// RetryAfterError is an error interface for errors that specify a retry delay in seconds.
type RetryAfterError interface {
	error
	RetryAfterSeconds() int
}

// AsRetryAfterError checks if err or any error in its chain implements RetryAfterError.
func AsRetryAfterError(err error) (RetryAfterError, bool) {
	var retryAfterErr RetryAfterError
	if errors.As(err, &retryAfterErr) {
		return retryAfterErr, true
	}
	return nil, false
}

// DefaultPendingDeleteRetryAfterSeconds defines the retry delay (5 minutes = 300s) for PendingDeleteErrorCode (500045).
// Per NSX Policy Framework error definition:
// "An object with the same path=[{0}] is marked for deletion. Either use another path or wait for the purge cycle
// (which runs every 5 minutes) for permanent removal of the object."
// Source: https://github-vcf.devops.broadcom.net/vcf/nsx/blob/nsx-main/mp/policy/policy-framework-api/src/main/resources/META-INF/messages/PolicyFrameworkErrorMessages.properties (errorcode.500045)
const DefaultPendingDeleteRetryAfterSeconds = 300

type NsxPendingDelete struct {
	nsxErrorImpl
}

func CreateNsxPendingDelete() *NsxPendingDelete {
	nsxErr := &NsxPendingDelete{}
	nsxErr.msg = "An object with the same name is marked for deletion. Either use another path or wait for the purge cycle to permanently remove the deleted object"
	return nsxErr
}

func (e *NsxPendingDelete) RetryAfterSeconds() int {
	return DefaultPendingDeleteRetryAfterSeconds
}

type NsxSegmentWithVM struct {
	managerErrorImpl
}

func CreateNsxSegmentWithVM() *NsxSegmentWithVM {
	nsxErr := &NsxSegmentWithVM{}
	nsxErr.msg = "Cannot delete segment as it still has VMs or VIFs attached"
	return nsxErr
}

type NsxOverlapAddresses struct {
	NsxLibInvalidInputImpl
}

func CreateNsxOverlapAddresses(details string) *NsxOverlapAddresses {
	m := fmt.Sprintf("Overlapping addresses found: %s", details)
	nsxErr := &NsxOverlapAddresses{}
	nsxErr.msg = m
	return nsxErr
}

type NsxOverlapVlan struct {
	NsxLibInvalidInputImpl
}

func CreateNsxOverlapVlan() *NsxOverlapVlan {
	nsxErr := &NsxOverlapVlan{}
	nsxErr.msg = "Duplicate logical-switch vlan-ids sharing same underlying physical devices resulting in a conflict"
	return nsxErr
}

type APITransactionAborted struct {
	managerErrorImpl
}

func CreateAPITransactionAborted() *APITransactionAborted {
	nsxErr := &APITransactionAborted{}
	nsxErr.msg = "API transaction aborted as MP cluster is reconfiguring"
	return nsxErr
}

type CannotConnectToServer struct {
	managerErrorImpl
}

func CreateCannotConnectToServer() *CannotConnectToServer {
	nsxErr := &CannotConnectToServer{}
	nsxErr.msg = "Cannot connect to server"
	return nsxErr
}

type ResourceInUse struct {
	managerErrorImpl
}

func CreateResourceInUse() *ResourceInUse {
	nsxErr := &ResourceInUse{}
	nsxErr.msg = "The object cannot be deleted as either it has children or it is being referenced by other objects"
	return nsxErr
}

type Timeout struct {
	nsxErrorImpl
}

func CreateTimeout(host string) *Timeout {
	m := fmt.Sprintf("Connect to %s timeout", host)
	nsxErr := &Timeout{}
	nsxErr.msg = m
	return nsxErr
}

type ConnectionError struct {
	nsxErrorImpl
}

func CreateConnectionError(host string) *ConnectionError {
	m := fmt.Sprintf("Connect to %s error", host)
	nsxErr := &ConnectionError{}
	nsxErr.msg = m
	return nsxErr
}

// PageMaxError For client usage
type PageMaxError struct {
	Desc string
}

func (err PageMaxError) Error() string {
	return err.Desc
}

type PodIPNotFound struct {
	Desc string
}

func (err PodIPNotFound) Error() string {
	return err.Desc
}

type PodNotRunning struct {
	Desc string
}

func (err PodNotRunning) Error() string {
	return err.Desc
}

type NoEffectiveOption struct {
	Desc string
}

func (err NoEffectiveOption) Error() string {
	return err.Desc
}

type RestrictionError struct {
	Desc string
}

func (err RestrictionError) Error() string {
	return err.Desc
}

type IPBlockAllExhaustedError struct {
	Desc string
}

func (err IPBlockAllExhaustedError) Error() string {
	return err.Desc
}

type ExceedTagsError struct {
	Desc string
}

func (err ExceedTagsError) Error() string { return err.Desc }

type ValidationError struct {
	Desc string
}

func (err ValidationError) Error() string {
	return err.Desc
}

type Status struct {
	Code    uint32
	Message string
}

func (s Status) Error() string {
	return s.Message
}

var (
	ValidationFailed         = Status{Code: 1, Message: "failed to validate config"}
	GetNSXClientFailed       = Status{Code: 2, Message: "failed to get nsx client"}
	InitCleanupServiceFailed = Status{Code: 3, Message: "failed to initialize cleanup service"}
	CleanupResourceFailed    = Status{Code: 4, Message: "failed to clean up"}
	TimeoutFailed            = Status{Code: 5, Message: "failed because of timeout"}
)

type RealizeStateError struct {
	message      string
	code         int
	relatedCodes []int
}

func (e *RealizeStateError) Error() string {
	return e.message
}

func (e *RealizeStateError) GetCode() int {
	return e.code
}

func (e *RealizeStateError) GetRelatedCodes() []int {
	return e.relatedCodes
}

func NewRealizeStateError(msg string, code int, relatedCodes ...int) *RealizeStateError {
	return &RealizeStateError{message: msg, code: code, relatedCodes: relatedCodes}
}

func IsRealizeStateError(err error) bool {
	_, ok := err.(*RealizeStateError)
	return ok
}

type RetryRealizeError struct {
	message string
}

func (e *RetryRealizeError) Error() string {
	return e.message
}

func NewRetryRealizeError(msg string) *RetryRealizeError {
	return &RetryRealizeError{message: msg}
}

func IsRetryRealizeError(alarm model.PolicyAlarmResource) bool {
	// The ProviderNotReady error indicates NSX get timeout when waiting for the dependencies
	// and may become Realized after retry.
	if alarm.ErrorDetails != nil && alarm.ErrorDetails.ErrorCode != nil && *alarm.ErrorDetails.ErrorCode == ProviderNotReadyErrorCode {
		return true
	}
	return false
}

var (
	// LBSEdgeCapacityErrorCodes contains all NSX error codes that signify edge node/cluster capacity
	// or sizing constraints for Load Balancer Services, ordered by priority (most specific first).
	LBSEdgeCapacityErrorCodes = []int64{
		LBSEdgeNodeSizeNotSupportedErrorCode,
		LBSEdgeNodeNoCapacityForSizeErrorCode,
		EdgeClusterInsufficientResourcesErrorCode,
		EdgeClusterNoCapacityPoolErrorCode,
	}
)

// ExtractAllErrorCodes extracts all distinct error codes from a structured error object (including wrapped
// NSXApiError, RealizeStateError, and GeneralNsxError).
func ExtractAllErrorCodes(err error) []int64 {
	if err == nil {
		return nil
	}
	var codes []int64
	seen := make(map[int64]bool)

	addCode := func(code int64) {
		if code > 0 && !seen[code] {
			seen[code] = true
			codes = append(codes, code)
		}
	}

	// 1. Structured NSXApiError (supports Go 1.13+ error wrapping via errors.As)
	var apiErr *NSXApiError
	if errors.As(err, &apiErr) && apiErr != nil && apiErr.ApiError != nil {
		if apiErr.ErrorCode != nil {
			addCode(*apiErr.ErrorCode)
		}
		for _, rel := range apiErr.RelatedErrors {
			if rel.ErrorCode != nil {
				addCode(*rel.ErrorCode)
			}
		}
	}

	// 2. Structured RealizeStateError (supports Go 1.13+ error wrapping via errors.As)
	var realizeErr *RealizeStateError
	if errors.As(err, &realizeErr) && realizeErr != nil {
		if realizeErr.GetCode() > 0 {
			addCode(int64(realizeErr.GetCode()))
		}
		for _, rc := range realizeErr.GetRelatedCodes() {
			if rc > 0 {
				addCode(int64(rc))
			}
		}
	}

	// 3. Structured GeneralNsxError (supports Go 1.13+ error wrapping via errors.As)
	var generalNsxErr *GeneralNsxError
	if errors.As(err, &generalNsxErr) && generalNsxErr != nil {
		addCode(int64(generalNsxErr.ErrorCode))
		for _, c := range generalNsxErr.RelatedErrorCodes {
			addCode(int64(c))
		}
	}

	return codes
}

// HasAnyErrorCode returns true if err contains any of targetCodes.
func HasAnyErrorCode(err error, targetCodes ...int64) bool {
	if err == nil || len(targetCodes) == 0 {
		return false
	}
	extracted := ExtractAllErrorCodes(err)
	for _, c := range extracted {
		for _, t := range targetCodes {
			if c == t {
				return true
			}
		}
	}
	return false
}

// GetFirstMatchingErrorCode returns the first error code in err matching targetCodes in targetCodes' priority order, or 0 if none match.
func GetFirstMatchingErrorCode(err error, targetCodes ...int64) int64 {
	if err == nil || len(targetCodes) == 0 {
		return 0
	}
	extracted := ExtractAllErrorCodes(err)
	for _, t := range targetCodes {
		for _, c := range extracted {
			if c == t {
				return t
			}
		}
	}
	return 0
}

// IsLBSEdgeCapacityError reports whether the error indicates insufficient Edge cluster/node capacity
// or unsupported sizing for Load Balancer Service based on NSX error codes.
func IsLBSEdgeCapacityError(err error) bool {
	return HasAnyErrorCode(err, LBSEdgeCapacityErrorCodes...)
}

// GetLBSEdgeCapacityErrorCode returns the matched capacity error code, or 0 if none matched.
func GetLBSEdgeCapacityErrorCode(err error) int64 {
	return GetFirstMatchingErrorCode(err, LBSEdgeCapacityErrorCodes...)
}
