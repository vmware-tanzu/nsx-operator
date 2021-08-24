/* Copyright © 2021 VMware, Inc. All Rights Reserved.

   SPDX-License-Identifier: Apache-2.0 */

package locerrors

import "time"

const (
	defaultLinearRetryAttempts = 1
)
const (
	ERR_NSX_OPERATOR_INVALID_CONFIG  = "NSXOP00001"
	ERR_NSX_OPERATOR_INIT_FAILED     = "NSXOP00002"
	ERR_NSX_OPERATOR_INVALID_STATE   = "NSXOP00003"
	ERR_NSX_OPERATOR_CERT_NOT_FOUND  = "NSXOP00005"
	ERR_NSX_OPERATOR_TOKEN_NOT_FOUND = "NSXOP00006"
)

type Error interface {
	error
	GetMessage() string
	GetErrorCode() string
}

type baseError struct {
	Msg       string
	ErrorCode string
}

func (e *baseError) Error() string {
	return e.ErrorCode + ": " + e.Msg
}

func (e *baseError) GetMessage() string {
	return e.Msg
}

func (e *baseError) GetErrorCode() string {
	return e.ErrorCode
}

func newBaseError(msg string, errorCode string) baseError {
	return baseError{
		Msg:       msg,
		ErrorCode: errorCode,
	}
}

type KubernetesClientInitFail struct {
	baseError
}

func NewKubernetesClientInitFailError(msg, errCode string) Error {
	return &KubernetesClientInitFail{newBaseError(msg, errCode)}
}

type RetryableError struct {
	baseError
	MinRetryInterval    time.Duration
	MaxRetryInterval    time.Duration
	LinearRetryAttempts int
	MaxRetryAttempts    int
}

func NewRetryableError(msg, errCode string, minRetryInterval, maxRetryInterval time.Duration, linearRetryAttempts, maxRetryAttempts int) Error {
	return &RetryableError{
		baseError:           newBaseError(msg, errCode),
		MinRetryInterval:    minRetryInterval,
		MaxRetryInterval:    maxRetryInterval,
		LinearRetryAttempts: linearRetryAttempts,
		MaxRetryAttempts:    maxRetryAttempts}
}

type InfiniteRetryError struct {
	RetryableError
}

func NewInfiniteRetryError(msg, errCode string, minRetryInterval, maxRetryInterval time.Duration, linearRetryAttempts int) Error {
	return &InfiniteRetryError{
		RetryableError: RetryableError{
			baseError:           newBaseError(msg, errCode),
			MinRetryInterval:    minRetryInterval,
			MaxRetryInterval:    maxRetryInterval,
			LinearRetryAttempts: linearRetryAttempts,
			MaxRetryAttempts:    0,
		},
	}
}
