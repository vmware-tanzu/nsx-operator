/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"fmt"
)

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
		impl.ErrorDetail.RelatedErrorCodes = []int{}
		for index := range detail.RelatedErrorCodes {
			impl.ErrorDetail.RelatedErrorCodes = append(impl.ErrorDetail.RelatedErrorCodes, detail.RelatedErrorCodes[index])
		}
	}
	if len(detail.RelatedStatusCodes) > 0 {
		impl.ErrorDetail.RelatedStatusCodes = []string{}
		for index := range detail.RelatedStatusCodes {
			impl.ErrorDetail.RelatedStatusCodes = append(impl.ErrorDetail.RelatedStatusCodes, detail.RelatedStatusCodes[index])
		}
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
	msg    string `parent:"RealizationError"`
	detail *ErrorDetail
}

func CreateRealizationErrorStateError(resourceType string, resourceID string, error string) *RealizationErrorStateError {
	m := fmt.Sprintf("%s ID %s is in ERROR state: %s", resourceType, resourceID, error)
	nsxErr := &RealizationErrorStateError{msg: m}
	return nsxErr
}

type RealizationTimeoutError struct {
	msg    string `parent:"RealizationError"`
	detail *ErrorDetail
}

func CreateRealizationTimeoutError(resourceType string, resourceID string, attempts string, sleep string) *RealizationTimeoutError {
	m := fmt.Sprintf("%s ID %s was not realized after %s attempts with %s seconds sleep", resourceType, resourceID, attempts, sleep)
	nsxErr := &RealizationTimeoutError{msg: m}
	return nsxErr
}

type DetailedRealizationTimeoutError struct {
	msg    string `parent:"RealizationError"`
	detail *ErrorDetail
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
	msg    string
	detail *ErrorDetail
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

type NsxPendingDelete struct {
	nsxErrorImpl
}

func CreateNsxPendingDelete() *NsxPendingDelete {
	nsxErr := &NsxPendingDelete{}
	nsxErr.msg = "An object with the same name is marked for deletion. Either use another path or wait for the purge cycle to permanently remove the deleted object"
	return nsxErr
}

type NsxSegemntWithVM struct {
	managerErrorImpl
}

func CreateNsxSegemntWithVM() *NsxSegemntWithVM {
	nsxErr := &NsxSegemntWithVM{}
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

type NoFilteredPod struct {
	Desc string
}

func (err NoFilteredPod) Error() string {
	return err.Desc
}
