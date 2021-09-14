// Copyright (c) 2021 VMware, Inc. All rights reserved. VMware Confidential.

package util

import (
	"fmt"
)

type nsxError interface {
	setDetail(detail *ErrorDetail)
}

type NsxLibException struct {
	msg    string `parent:"Exception"`
	detail *ErrorDetail
}

func createNsxLibException() *NsxLibException {
	err := &NsxLibException{msg: "An unknown exception occurred."}
	return err
}

func (e *NsxLibException) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *NsxLibException) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type ObjectAlreadyExists struct {
	msg    string `parent:"NsxLibException"`
	detail *ErrorDetail
}

func createObjectAlreadyExists(objectType string) *ObjectAlreadyExists {
	m := fmt.Sprintf("%s already exists", objectType)
	err := &ObjectAlreadyExists{msg: m}
	return err
}

func (e *ObjectAlreadyExists) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *ObjectAlreadyExists) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type NotImplemented struct {
	msg    string `parent:"NsxLibException"`
	detail *ErrorDetail
}

func createNotImplemented(operation string) *NotImplemented {
	m := fmt.Sprintf("%s is not supported", operation)
	err := &NotImplemented{msg: m}
	return err
}

func (e *NotImplemented) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *NotImplemented) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type ObjectNotGenerated struct {
	msg    string `parent:"NsxLibException"`
	detail *ErrorDetail
}

func createObjectNotGenerated(objectType string) *ObjectNotGenerated {
	m := fmt.Sprintf("%s was not generated", objectType)
	err := &ObjectNotGenerated{msg: m}
	return err
}

func (e *ObjectNotGenerated) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *ObjectNotGenerated) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type CertificateError struct {
	msg    string `parent:"NsxLibException"`
	detail *ErrorDetail
}

func createCertificateError(msg string) *CertificateError {
	m := fmt.Sprintf("Certificate error: %s", msg)
	err := &CertificateError{msg: m}
	return err
}

func (e *CertificateError) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *CertificateError) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type NsxLibInvalidInput struct {
	msg    string `parent:"NsxLibException"`
	detail *ErrorDetail
}

func createNsxLibInvalidInput(errorMessage string) *NsxLibInvalidInput {
	m := fmt.Sprintf("Invalid input for operation: %s.", errorMessage)
	err := &NsxLibInvalidInput{msg: m}
	return err
}

func (e *NsxLibInvalidInput) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *NsxLibInvalidInput) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type ManagerError struct {
	msg    string `parent:"NsxLibException"`
	detail *ErrorDetail
}

func CreateManagerError(manager string, operation string, details string) *ManagerError {
	m := fmt.Sprintf("Unexpected error from backend manager (%s) for %s%s", manager, operation, details)
	err := &ManagerError{msg: m}
	return err
}

func (e *ManagerError) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *ManagerError) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type ResourceNotFound struct {
	msg    string `parent:"ManagerError"`
	detail *ErrorDetail
}

func createResourceNotFound(manager string, operation string) *ResourceNotFound {
	m := fmt.Sprintf("Resource could not be found on backend (%s) for %s", manager, operation)
	err := &ResourceNotFound{msg: m}
	return err
}

func (e *ResourceNotFound) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *ResourceNotFound) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type MultipleResourcesFound struct {
	msg    string `parent:"ManagerError"`
	detail *ErrorDetail
}

func createMultipleResourcesFound(manager string, operation string) *MultipleResourcesFound {
	m := fmt.Sprintf("Multiple resources are found on backend (%s) for %s, where only one is expected", manager, operation)
	err := &MultipleResourcesFound{msg: m}
	return err
}

func (e *MultipleResourcesFound) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *MultipleResourcesFound) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type BackendResourceNotFound struct {
	msg    string `parent:"ResourceNotFound"`
	detail *ErrorDetail
}

func createBackendResourceNotFound(details string, manager string, operation string) *BackendResourceNotFound {
	m := fmt.Sprintf("%s On backend (%s) with Operation: %s", details, manager, operation)
	err := &BackendResourceNotFound{msg: m}
	return err
}

func (e *BackendResourceNotFound) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *BackendResourceNotFound) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type InvalidInput struct {
	msg    string `parent:"ManagerError"`
	detail *ErrorDetail
}

func createInvalidInput(operation string, argVal string, argName string) *InvalidInput {
	m := fmt.Sprintf("%s failed: Invalid input %s for %s", operation, argVal, argName)
	err := &InvalidInput{msg: m}
	return err
}

func (e *InvalidInput) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *InvalidInput) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type RealizationError struct {
	msg    string `parent:"ManagerError"`
	detail *ErrorDetail
}

func createRealizationError(operation string, argVal string, argName string) *RealizationError {
	m := fmt.Sprintf("%s failed: Invalid input %s for %s", operation, argVal, argName)
	err := &RealizationError{msg: m}
	return err
}

func (e *RealizationError) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *RealizationError) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type RealizationErrorStateError struct {
	msg    string `parent:"RealizationError"`
	detail *ErrorDetail
}

func createRealizationErrorStateError(resourceType string, resourceID string, error string) *RealizationErrorStateError {
	m := fmt.Sprintf("%s ID %s is in ERROR state: %s", resourceType, resourceID, error)
	err := &RealizationErrorStateError{msg: m}
	return err
}

func (e *RealizationErrorStateError) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *RealizationErrorStateError) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type RealizationTimeoutError struct {
	msg    string `parent:"RealizationError"`
	detail *ErrorDetail
}

func createRealizationTimeoutError(resourceType string, resourceID string, attempts string, sleep string) *RealizationTimeoutError {
	m := fmt.Sprintf("%s ID %s was not realized after %s attempts with %s seconds sleep", resourceType, resourceID, attempts, sleep)
	err := &RealizationTimeoutError{msg: m}
	return err
}

func (e *RealizationTimeoutError) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *RealizationTimeoutError) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type DetailedRealizationTimeoutError struct {
	msg    string `parent:"RealizationError"`
	detail *ErrorDetail
}

func createDetailedRealizationTimeoutError(resourceType string, resourceID string, realizedType string, relatedType string, relatedID string, attempts string, sleep string) *DetailedRealizationTimeoutError {
	m := fmt.Sprintf("%s ID %s was not realized to %s for %s %s after %s attempts with %s seconds sleep", resourceType, resourceID, realizedType, relatedType, relatedID, attempts, sleep)
	err := &DetailedRealizationTimeoutError{msg: m}
	return err
}

func (e *DetailedRealizationTimeoutError) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *DetailedRealizationTimeoutError) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type StaleRevision struct {
	msg    string `parent:"ManagerError"`
	detail *ErrorDetail
}

func createStaleRevision(resourceType string, resourceID string, realizedType string, relatedType string, relatedID string, attempts string, sleep string) *StaleRevision {
	m := fmt.Sprintf("%s ID %s was not realized to %s for %s %s after %s attempts with %s seconds sleep", resourceType, resourceID, realizedType, relatedType, relatedID, attempts, sleep)
	err := &StaleRevision{msg: m}
	return err
}

func (e *StaleRevision) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *StaleRevision) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type ServerBusy struct {
	msg    string `parent:"ManagerError"`
	detail *ErrorDetail
}

func createServerBusy(resourceType string, resourceID string, realizedType string, relatedType string, relatedID string, attempts string, sleep string) *ServerBusy {
	m := fmt.Sprintf("%s ID %s was not realized to %s for %s %s after %s attempts with %s seconds sleep", resourceType, resourceID, realizedType, relatedType, relatedID, attempts, sleep)
	err := &ServerBusy{msg: m}
	return err
}

func (e *ServerBusy) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *ServerBusy) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type TooManyRequests struct {
	msg    string `parent:"ServerBusy"`
	detail *ErrorDetail
}

func createTooManyRequests(resourceType string, resourceID string, realizedType string, relatedType string, relatedID string, attempts string, sleep string) *TooManyRequests {
	m := fmt.Sprintf("%s ID %s was not realized to %s for %s %s after %s attempts with %s seconds sleep", resourceType, resourceID, realizedType, relatedType, relatedID, attempts, sleep)
	err := &TooManyRequests{msg: m}
	return err
}

func (e *TooManyRequests) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *TooManyRequests) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type ServiceUnavailable struct {
	msg    string `parent:"ServerBusy"`
	detail *ErrorDetail
}

func createServiceUnavailable(resourceType string, resourceID string, realizedType string, relatedType string, relatedID string, attempts string, sleep string) *ServiceUnavailable {
	m := fmt.Sprintf("%s ID %s was not realized to %s for %s %s after %s attempts with %s seconds sleep", resourceType, resourceID, realizedType, relatedType, relatedID, attempts, sleep)
	err := &ServiceUnavailable{msg: m}
	return err
}

func (e *ServiceUnavailable) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *ServiceUnavailable) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type ClientCertificateNotTrusted struct {
	msg    string `parent:"ManagerError"`
	detail *ErrorDetail
}

func createClientCertificateNotTrusted() *ClientCertificateNotTrusted {
	err := &ClientCertificateNotTrusted{msg: "Certificate not trusted"}
	return err
}

func (e *ClientCertificateNotTrusted) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *ClientCertificateNotTrusted) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type BadXSRFToken struct {
	msg    string `parent:"ManagerError"`
	detail *ErrorDetail
}

func createBadXSRFToken() *BadXSRFToken {
	err := &BadXSRFToken{msg: "Bad or expired XSRF token"}
	return err
}

func (e *BadXSRFToken) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *BadXSRFToken) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type InvalidCredentials struct {
	msg    string `parent:"ManagerError"`
	detail *ErrorDetail
}

func createInvalidCredentials(msg string) *InvalidCredentials {
	m := fmt.Sprintf("Failed to authenticate with NSX: %s", msg)
	err := &InvalidCredentials{msg: m}
	return err
}

func (e *InvalidCredentials) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *InvalidCredentials) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type InvalidLicense struct {
	msg    string `parent:"ManagerError"`
	detail *ErrorDetail
}

func createInvalidLicense(msg string) *InvalidLicense {
	m := fmt.Sprintf("No valid License to configure NSX resources: %s", msg)
	err := &InvalidLicense{msg: m}
	return err
}

func (e *InvalidLicense) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *InvalidLicense) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type BadJSONWebTokenProviderRequest struct {
	msg    string `parent:"NsxLibException"`
	detail *ErrorDetail
}

func createBadJSONWebTokenProviderRequest(msg string) *BadJSONWebTokenProviderRequest {
	m := fmt.Sprintf("Bad or expired JSON web token request from provider: %s", msg)
	err := &BadJSONWebTokenProviderRequest{msg: m}
	return err
}

func (e *BadJSONWebTokenProviderRequest) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *BadJSONWebTokenProviderRequest) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type ServiceClusterUnavailable struct {
	msg    string `parent:"ManagerError"`
	detail *ErrorDetail
}

func CreateServiceClusterUnavailable(clusterID string) *ServiceClusterUnavailable {
	m := fmt.Sprintf("Service cluster: '%s' is unavailable. Please, check NSX setup and/or configuration", clusterID)
	err := &ServiceClusterUnavailable{msg: m}
	return err
}

func (e *ServiceClusterUnavailable) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *ServiceClusterUnavailable) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type NSGroupMemberNotFound struct {
	msg    string `parent:"ManagerError"`
	detail *ErrorDetail
}

func createNSGroupMemberNotFound(nsgroupID string, memberID string) *NSGroupMemberNotFound {
	m := fmt.Sprintf("Could not find NSGroup %s member %s for removal.", nsgroupID, memberID)
	err := &NSGroupMemberNotFound{msg: m}
	return err
}

func (e *NSGroupMemberNotFound) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *NSGroupMemberNotFound) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type NSGroupIsFull struct {
	msg    string `parent:"ManagerError"`
	detail *ErrorDetail
}

func createNSGroupIsFull(nsgroupID string) *NSGroupIsFull {
	m := fmt.Sprintf("NSGroup %s contains has reached its maximum capacity, unable to add additional members.", nsgroupID)
	err := &NSGroupIsFull{msg: m}
	return err
}

func (e *NSGroupIsFull) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *NSGroupIsFull) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type SecurityGroupMaximumCapacityReached struct {
	msg    string `parent:"ManagerError"`
	detail *ErrorDetail
}

func createSecurityGroupMaximumCapacityReached(sgID string) *SecurityGroupMaximumCapacityReached {
	m := fmt.Sprintf("Security Group %s has reached its maximum capacity, no more ports can be associated with this security-group.", sgID)
	err := &SecurityGroupMaximumCapacityReached{msg: m}
	return err
}

func (e *SecurityGroupMaximumCapacityReached) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *SecurityGroupMaximumCapacityReached) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type NsxSearchInvalidQuery struct {
	msg    string `parent:"NsxLibException"`
	detail *ErrorDetail
}

func createNsxSearchInvalidQuery(reason string) *NsxSearchInvalidQuery {
	m := fmt.Sprintf("Invalid input for NSX search query. Reason: %s", reason)
	err := &NsxSearchInvalidQuery{msg: m}
	return err
}

func (e *NsxSearchInvalidQuery) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *NsxSearchInvalidQuery) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type NsxSearchError struct {
	msg    string `parent:"NsxLibException"`
	detail *ErrorDetail
}

func createNsxSearchError() *NsxSearchError {
	err := &NsxSearchError{msg: "Search failed due to error"}
	return err
}

func (e *NsxSearchError) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *NsxSearchError) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type NsxIndexingInProgress struct {
	msg    string `parent:"NsxSearchError"`
	detail *ErrorDetail
}

func createNsxIndexingInProgress() *NsxIndexingInProgress {
	err := &NsxIndexingInProgress{msg: "Bad Request due to indexing is in progress, please retry after sometime"}
	return err
}

func (e *NsxIndexingInProgress) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *NsxIndexingInProgress) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type NsxSearchTimeout struct {
	msg    string `parent:"NsxSearchError"`
	detail *ErrorDetail
}

func createNsxSearchTimeout() *NsxSearchTimeout {
	err := &NsxSearchTimeout{msg: "Request timed out. This may occur when system is under load or running low on resources"}
	return err
}

func (e *NsxSearchTimeout) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *NsxSearchTimeout) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type NsxSearchOutOfSync struct {
	msg    string `parent:"NsxSearchError"`
	detail *ErrorDetail
}

func createNsxSearchOutOfSync() *NsxSearchOutOfSync {
	err := &NsxSearchOutOfSync{msg: "Index is currently out of sync"}
	return err
}

func (e *NsxSearchOutOfSync) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *NsxSearchOutOfSync) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type NsxPendingDelete struct {
	msg    string `parent:"NsxLibException"`
	detail *ErrorDetail
}

func createNsxPendingDelete() *NsxPendingDelete {
	err := &NsxPendingDelete{msg: "An object with the same name is marked for deletion. Either use another path or wait for the purge cycle to permanently remove the deleted object"}
	return err
}

func (e *NsxPendingDelete) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *NsxPendingDelete) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type NsxSegemntWithVM struct {
	msg    string `parent:"ManagerError"`
	detail *ErrorDetail
}

func createNsxSegemntWithVM() *NsxSegemntWithVM {
	err := &NsxSegemntWithVM{msg: "Cannot delete segment as it still has VMs or VIFs attached"}
	return err
}

func (e *NsxSegemntWithVM) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *NsxSegemntWithVM) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type NsxOverlapAddresses struct {
	msg    string `parent:"NsxLibInvalidInput"`
	detail *ErrorDetail
}

func createNsxOverlapAddresses(details string) *NsxOverlapAddresses {
	m := fmt.Sprintf("Overlapping addresses found: %s", details)
	err := &NsxOverlapAddresses{msg: m}
	return err
}

func (e *NsxOverlapAddresses) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *NsxOverlapAddresses) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type NsxOverlapVlan struct {
	msg    string `parent:"NsxLibInvalidInput"`
	detail *ErrorDetail
}

func createNsxOverlapVlan() *NsxOverlapVlan {
	err := &NsxOverlapVlan{msg: "Duplicate logical-switch vlan-ids sharing same underlying physical devices resulting in a conflict"}
	return err
}

func (e *NsxOverlapVlan) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *NsxOverlapVlan) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type APITransactionAborted struct {
	msg    string `parent:"ManagerError"`
	detail *ErrorDetail
}

func createAPITransactionAborted() *APITransactionAborted {
	err := &APITransactionAborted{msg: "API transaction aborted as MP cluster is reconfiguring"}
	return err
}

func (e *APITransactionAborted) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *APITransactionAborted) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type CannotConnectToServer struct {
	msg    string `parent:"ManagerError"`
	detail *ErrorDetail
}

func createCannotConnectToServer() *CannotConnectToServer {
	err := &CannotConnectToServer{msg: "Cannot connect to server"}
	return err
}

func (e *CannotConnectToServer) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *CannotConnectToServer) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type ResourceInUse struct {
	msg    string `parent:"ManagerError"`
	detail *ErrorDetail
}

func createResourceInUse() *ResourceInUse {
	err := &ResourceInUse{msg: "The object cannot be deleted as either it has children or it is being referenced by other objects"}
	return err
}

func (e *ResourceInUse) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *ResourceInUse) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type Timeout struct {
	msg    string `parent:"NsxLibException"`
	detail *ErrorDetail
}

func CreateTimeout(host string) *Timeout {
	m := fmt.Sprintf("Connect to %s timeout", host)
	err := &Timeout{msg: m}
	return err
}

func (e *Timeout) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *Timeout) setDetail(detail *ErrorDetail) {
	e.detail = detail
}

type ConnectionError struct {
	msg    string `parent:"NsxLibException"`
	detail *ErrorDetail
}

func CreateConnectionError(host string) *ConnectionError {
	m := fmt.Sprintf("Connect to %s error", host)
	err := &ConnectionError{msg: m}
	return err
}

func (e *ConnectionError) Error() string {
	if e.detail != nil {
		e.msg += e.detail.Error()
	}
	return e.msg
}

func (e *ConnectionError) setDetail(detail *ErrorDetail) {
	e.detail = detail
}
