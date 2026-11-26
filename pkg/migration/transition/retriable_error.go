/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transition

import (
	"context"
	"errors"
	"net"

	sdkerrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

// RetriableError marks an operation error as transient. Engine preserves the
// Running checkpoint and returns the error so controller-runtime applies its
// rate-limited retry backoff.
type RetriableError struct {
	Err error
}

func (err *RetriableError) Error() string {
	if err == nil || err.Err == nil {
		return "retriable operation error"
	}
	return err.Err.Error()
}

func (err *RetriableError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

// MarkRetriable wraps err for cases where a caller has operation-specific
// evidence that retrying is safe, such as a workflow revision changing during
// a failed PUT.
func MarkRetriable(err error) error {
	if err == nil {
		return nil
	}
	return &RetriableError{Err: err}
}

// IsRetriableError identifies known transient NSX, Kubernetes, and network
// errors. Unknown errors are not classified here; callers may explicitly mark
// operation-specific transient failures with MarkRetriable.
func IsRetriableError(err error) bool {
	if err == nil {
		return false
	}
	var marked *RetriableError
	if errors.As(err, &marked) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return true
	}
	var operationError *net.OpError
	if errors.As(err, &operationError) {
		return true
	}
	if apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsTooManyRequests(err) ||
		apierrors.IsServiceUnavailable(err) || apierrors.IsInternalError(err) || apierrors.IsConflict(err) {
		return true
	}
	var timedOut *sdkerrors.TimedOut
	var unavailable *sdkerrors.ServiceUnavailable
	var internal *sdkerrors.InternalServerError
	var concurrentChange *sdkerrors.ConcurrentChange
	if errors.As(err, &timedOut) || errors.As(err, &unavailable) || errors.As(err, &internal) || errors.As(err, &concurrentChange) {
		return true
	}
	var nsxError *nsxutil.NSXApiError
	if errors.As(err, &nsxError) {
		switch nsxError.Type() {
		case sdkerrors.ErrorType_TIMED_OUT, sdkerrors.ErrorType_SERVICE_UNAVAILABLE,
			sdkerrors.ErrorType_INTERNAL_SERVER_ERROR, sdkerrors.ErrorType_CONCURRENT_CHANGE:
			return true
		}
	}
	return false
}
