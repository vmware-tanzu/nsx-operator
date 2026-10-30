package framework

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"syscall"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
)

// IsTransientAPIError reports whether a failed API read can be retried. API
// status errors take precedence over underlying error text or transport errors.
// A caller's cancellation is never a reason to start another attempt.
func IsTransientAPIError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		return apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) ||
			apierrors.IsTooManyRequests(err) || apierrors.IsServiceUnavailable(err)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	for _, cause := range []error{
		syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.EHOSTUNREACH,
		syscall.ENETUNREACH, syscall.ETIMEDOUT,
	} {
		if errors.Is(err, cause) {
			return true
		}
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

// Poll checks condition immediately, then at interval until it succeeds or ctx
// expires. Temporary API errors are reported through onRetry and retried within
// the original deadline. A permanent error stops polling immediately. If the
// deadline expires without a subsequent successful API read, the returned error
// retains both the context error and the last temporary error.
func Poll(ctx context.Context, interval time.Duration, condition wait.ConditionWithContextFunc, onRetry func(error)) error {
	var lastError error
	err := wait.PollUntilContextCancel(ctx, interval, true, func(ctx context.Context) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		done, err := condition(ctx)
		if err == nil {
			lastError = nil
		}
		if contextError := ctx.Err(); contextError != nil {
			return false, contextError
		}
		if err == nil || !IsTransientAPIError(err) {
			return done, err
		}
		lastError = err
		if onRetry != nil {
			onRetry(err)
		}
		return false, nil
	})
	if err != nil && lastError != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return errors.Join(err, fmt.Errorf("last temporary API error: %w", lastError))
	}
	return err
}

var kubectlRequestError = regexp.MustCompile(`(?:^|[\n:])\s*(?:get|post|put|patch|delete|head) "https?://[^"\n]+":`)

// IsTransientKubectlError recognizes client-side failures reaching the API
// server. It deliberately excludes webhook, authorization, validation and other
// server-reported errors, even when those messages mention a connection failure.
// Callers must additionally ensure that repeating their command is safe.
func IsTransientKubectlError(output string) bool {
	message := strings.ToLower(strings.TrimSpace(output))
	for _, permanent := range []string{
		"error from server", "webhook", "forbidden", "unauthorized",
		"badrequest", "bad request", "invalid", "conflict", "alreadyexists",
		"already exists", "not found", "x509:", "certificate has expired",
	} {
		if strings.Contains(message, permanent) {
			return false
		}
	}
	if strings.Contains(message, "the connection to the server ") && strings.Contains(message, " was refused") {
		return true
	}
	clientFailure := strings.Contains(message, "unable to connect to the server:") ||
		strings.Contains(message, "failed to download openapi:") ||
		kubectlRequestError.MatchString(message)
	if !clientFailure {
		return false
	}
	for _, temporary := range []string{
		"connection refused", "connection reset by peer", "no route to host",
		"network is unreachable", "i/o timeout", "tls handshake timeout",
		"client.timeout exceeded", "context deadline exceeded",
	} {
		if strings.Contains(message, temporary) {
			return true
		}
	}
	return strings.HasSuffix(message, ": eof") || strings.HasSuffix(message, ": unexpected eof")
}
