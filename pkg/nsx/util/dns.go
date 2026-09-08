/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"errors"
	"fmt"
	"strings"

	apierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
)

const (
	// ConditionTypeDNSRecordReady is the condition type for DNS auto-record status.
	ConditionTypeDNSRecordReady = "DNSRecordReady"

	// Condition reasons for DNSRecordReady
	ConditionReasonDNSSuccess          = "Success"
	ConditionReasonDNSInvalidLength    = "InvalidDNSNameLength"
	ConditionReasonDNSInvalidName      = "InvalidDNSName"
	ConditionReasonDNSConflict         = "DNSNameConflict"
	ConditionReasonDNSNSXApiError      = "NSXApiError"
	ConditionReasonVPCDNSNotConfigured = "VPCDNSNotConfigured"

	// Event reasons for DNS auto-record events
	EventReasonDNSRecordCreated     = "DNSRecordCreated"
	EventReasonDNSConfigFailed      = "DNSConfigFailed"
	EventReasonDNSConflict          = "DNSConflict"
	EventReasonDNSNSXApiError       = "NSXApiError"
	EventReasonVPCDNSNotConfigured  = "VPCDNSNotConfigured"
	EventReasonVPCResolverIPMissing = "VPCResolverIPMissing"
)

// ValidateDNSHostname checks that hostname adheres to DNS length limits:
// 1. Total length must not be 0 and must not exceed 255 characters.
// 2. Each dot-separated label must not exceed 63 characters and must not be empty.
func ValidateDNSHostname(hostname string) error {
	trimmed := strings.TrimSuffix(hostname, ".")
	if len(trimmed) == 0 {
		return fmt.Errorf("DNS hostname must not be empty")
	}
	if len(hostname) > 255 {
		return fmt.Errorf("DNS hostname length %d exceeds maximum allowed length of 255", len(hostname))
	}
	labels := strings.Split(trimmed, ".")
	for _, label := range labels {
		if len(label) == 0 {
			return fmt.Errorf("DNS label in %q must not be empty", hostname)
		}
		if len(label) > 63 {
			return fmt.Errorf("DNS label %q length %d exceeds maximum allowed length of 63", label, len(label))
		}
	}
	return nil
}

// BuildDNSHostname returns the normalized (lowercased) DNS hostname for an object: <name>.<namespace>.
func BuildDNSHostname(name, namespace string) string {
	return strings.ToLower(fmt.Sprintf("%s.%s", name, namespace))
}

// IsNSXConflictError reports whether err represents an NSX HTTP 409 Conflict error for DNS uniqueness.
func IsNSXConflictError(err error) bool {
	if err == nil {
		return false
	}
	var nsxErr *NSXApiError
	if errors.As(err, &nsxErr) {
		if nsxErr.Type() == apierrors.ErrorType_ALREADY_EXISTS ||
			nsxErr.Type() == apierrors.ErrorTypeEnum("ALREADY_EXISTS") {
			return true
		}
		if nsxErr.ErrorCode != nil && (*nsxErr.ErrorCode == 409 || *nsxErr.ErrorCode == 500087) {
			return true
		}
		errStr := strings.ToLower(nsxErr.Error())
		if strings.Contains(errStr, "409") || strings.Contains(errStr, "conflict") || strings.Contains(errStr, "already exists") {
			// Do not treat concurrent change as a permanent conflict
			if strings.Contains(errStr, "concurrent_change") || strings.Contains(errStr, "precondition failed") {
				return false
			}
			return true
		}
	}
	var alreadyExists apierrors.AlreadyExists
	if errors.As(err, &alreadyExists) {
		return true
	}
	var objExists *ObjectAlreadyExists
	if errors.As(err, &objExists) {
		return true
	}
	errStr := strings.ToLower(err.Error())
	// Do not treat concurrent change as a permanent conflict
	if strings.Contains(errStr, "concurrent_change") || strings.Contains(errStr, "precondition failed") {
		return false
	}
	return strings.Contains(errStr, "409") || strings.Contains(errStr, "conflict") || strings.Contains(errStr, "already exists")
}

// IsNSXBadRequestError reports whether err represents an NSX HTTP 400 Bad Request error.
func IsNSXBadRequestError(err error) bool {
	if err == nil {
		return false
	}
	var nsxErr *NSXApiError
	if errors.As(err, &nsxErr) {
		if nsxErr.Type() == apierrors.ErrorType_INVALID_REQUEST ||
			nsxErr.Type() == apierrors.ErrorType_INVALID_ARGUMENT ||
			nsxErr.Type() == apierrors.ErrorTypeEnum("INVALID_REQUEST") ||
			nsxErr.Type() == apierrors.ErrorTypeEnum("INVALID_ARGUMENT") {
			return true
		}
		if nsxErr.ErrorCode != nil && *nsxErr.ErrorCode == 400 {
			return true
		}
		errStr := strings.ToLower(nsxErr.Error())
		if strings.Contains(errStr, "400") || strings.Contains(errStr, "bad request") || strings.Contains(errStr, "invalid request") {
			return true
		}
	}
	var invalidRequest apierrors.InvalidRequest
	if errors.As(err, &invalidRequest) {
		return true
	}
	var invalidArgument apierrors.InvalidArgument
	if errors.As(err, &invalidArgument) {
		return true
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "400") || strings.Contains(errStr, "bad request")
}

// DNSReconcileOutcome describes the condition and event parameters for a DNS auto-record outcome.
type DNSReconcileOutcome struct {
	ConditionReason string
	EventReason     string
	EventType       string
	IsPermanent     bool
	Message         string
}

// ClassifyDNSError maps an error to its corresponding DNSReconcileOutcome per the DNS error retry matrix.
func ClassifyDNSError(err error) DNSReconcileOutcome {
	if err == nil {
		return DNSReconcileOutcome{
			ConditionReason: ConditionReasonDNSSuccess,
			EventReason:     EventReasonDNSRecordCreated,
			EventType:       "Normal",
			IsPermanent:     false,
			Message:         "DNS auto-record successfully created",
		}
	}
	if IsNSXConflictError(err) {
		return DNSReconcileOutcome{
			ConditionReason: ConditionReasonDNSConflict,
			EventReason:     EventReasonDNSConflict,
			EventType:       "Warning",
			IsPermanent:     true,
			Message:         fmt.Sprintf("DNS name conflict in NSX: %v", err),
		}
	}
	if IsNSXBadRequestError(err) {
		return DNSReconcileOutcome{
			ConditionReason: ConditionReasonDNSInvalidName,
			EventReason:     EventReasonDNSConfigFailed,
			EventType:       "Warning",
			IsPermanent:     true,
			Message:         fmt.Sprintf("NSX rejected DNS name: %v", err),
		}
	}
	return DNSReconcileOutcome{
		ConditionReason: ConditionReasonDNSNSXApiError,
		EventReason:     EventReasonDNSNSXApiError,
		EventType:       "Warning",
		IsPermanent:     false,
		Message:         fmt.Sprintf("NSX API error: %v", err),
	}
}
