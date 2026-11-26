/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transition

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	sdkerrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func TestIsRetriableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "explicit marker", err: MarkRetriable(errors.New("retry")), want: true},
		{name: "deadline", err: context.DeadlineExceeded, want: true},
		{name: "canceled context", err: context.Canceled, want: true},
		{name: "network operation", err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, want: true},
		{name: "kubernetes timeout", err: apierrors.NewTimeoutError("timeout", 1), want: true},
		{name: "kubernetes server timeout", err: apierrors.NewServerTimeout(schema.GroupResource{Resource: "pods"}, "list", 1), want: true},
		{name: "kubernetes rate limit", err: apierrors.NewTooManyRequests("throttled", 1), want: true},
		{name: "kubernetes unavailable", err: apierrors.NewServiceUnavailable("unavailable"), want: true},
		{name: "kubernetes internal error", err: apierrors.NewInternalError(errors.New("internal")), want: true},
		{name: "kubernetes conflict", err: apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, "object", errors.New("conflict")), want: true},
		{name: "SDK timeout", err: sdkerrors.NewTimedOut(), want: true},
		{name: "SDK unavailable", err: sdkerrors.NewServiceUnavailable(), want: true},
		{name: "SDK internal error", err: sdkerrors.NewInternalServerError(), want: true},
		{name: "SDK concurrent change", err: sdkerrors.NewConcurrentChange(), want: true},
		{name: "translated NSX service unavailable", err: nsxutil.NewNSXApiError(&model.ApiError{}, sdkerrors.ErrorType_SERVICE_UNAVAILABLE), want: true},
		{name: "invalid NSX request", err: sdkerrors.NewInvalidRequest(), want: false},
		{name: "terminal NSX failure", err: sdkerrors.NewNotFound(), want: false},
		{name: "unknown error", err: errors.New("bad input"), want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, IsRetriableError(tc.err))
		})
	}
}
