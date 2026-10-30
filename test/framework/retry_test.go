package framework

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"syscall"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestIsTransientAPIError(t *testing.T) {
	resource := schema.GroupResource{Resource: "pods"}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "success"},
		{name: "end of stream", err: io.EOF, want: true},
		{name: "truncated stream", err: fmt.Errorf("read: %w", io.ErrUnexpectedEOF), want: true},
		{name: "refused", err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, want: true},
		{name: "reset", err: syscall.ECONNRESET, want: true},
		{name: "host unreachable", err: syscall.EHOSTUNREACH, want: true},
		{name: "network unreachable", err: syscall.ENETUNREACH, want: true},
		{name: "request timeout", err: &url.Error{Op: "Get", URL: "https://192.0.2.1", Err: syscall.ETIMEDOUT}, want: true},
		{name: "API timeout", err: apierrors.NewTimeoutError("request timed out", 1), want: true},
		{name: "API server timeout", err: apierrors.NewServerTimeout(resource, "get", 1), want: true},
		{name: "API throttling", err: apierrors.NewTooManyRequests("busy", 1), want: true},
		{name: "API unavailable", err: fmt.Errorf("get: %w", apierrors.NewServiceUnavailable("unavailable")), want: true},
		{name: "forbidden", err: apierrors.NewForbidden(resource, "pod", syscall.ECONNREFUSED)},
		{name: "unauthorized", err: apierrors.NewUnauthorized("connection refused")},
		{name: "invalid request", err: apierrors.NewBadRequest("connection refused")},
		{name: "conflict", err: apierrors.NewConflict(resource, "pod", syscall.ECONNRESET)},
		{name: "not found", err: apierrors.NewNotFound(resource, "pod")},
		{name: "internal error", err: apierrors.NewInternalError(syscall.ECONNREFUSED)},
		{name: "canceled", err: fmt.Errorf("read: %w", context.Canceled)},
		{name: "invalid certificate", err: x509.UnknownAuthorityError{}},
		{name: "unknown DNS name", err: &net.DNSError{Err: "no such host", Name: "missing.example", IsNotFound: true}},
		{name: "unclassified", err: errors.New("failed to validate resource")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsTransientAPIError(test.err); got != test.want {
				t.Fatalf("IsTransientAPIError(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

func TestPollRecoversWithinContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	attempts, retries := 0, 0
	err := Poll(ctx, time.Millisecond, func(received context.Context) (bool, error) {
		if received != ctx {
			t.Fatal("condition did not receive caller context")
		}
		attempts++
		if attempts == 1 {
			return false, syscall.ECONNREFUSED
		}
		return true, nil
	}, func(err error) {
		retries++
		if !errors.Is(err, syscall.ECONNREFUSED) {
			t.Fatalf("unexpected retry error: %v", err)
		}
	})
	if err != nil || attempts != 2 || retries != 1 {
		t.Fatalf("Poll: error=%v, attempts=%d, retries=%d", err, attempts, retries)
	}
}

func TestPollStopsOnPermanentError(t *testing.T) {
	want := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "pod", errors.New("denied"))
	attempts := 0
	err := Poll(context.Background(), time.Hour, func(context.Context) (bool, error) {
		attempts++
		return false, want
	}, func(error) {
		t.Fatal("permanent error retried")
	})
	if err != want || attempts != 1 {
		t.Fatalf("Poll: error=%v, attempts=%d", err, attempts)
	}
}

func TestPollRetainsLastFailureOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := 0
	err := Poll(ctx, time.Hour, func(context.Context) (bool, error) {
		attempts++
		return false, syscall.EHOSTUNREACH
	}, func(error) {
		cancel()
	})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, syscall.EHOSTUNREACH) || attempts != 1 {
		t.Fatalf("Poll lost context or temporary cause: error=%v, attempts=%d", err, attempts)
	}
}

func TestPollClearsRecoveredTransportFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := 0
	err := Poll(ctx, time.Millisecond, func(context.Context) (bool, error) {
		attempts++
		if attempts == 1 {
			return false, syscall.ECONNREFUSED
		}
		cancel()
		return false, nil
	}, nil)
	if !errors.Is(err, context.Canceled) || errors.Is(err, syscall.ECONNREFUSED) || attempts != 2 {
		t.Fatalf("Poll retained stale failure: error=%v, attempts=%d", err, attempts)
	}
}

func TestPollDoesNotRunAfterDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	err := Poll(ctx, time.Millisecond, func(context.Context) (bool, error) {
		t.Fatal("condition ran after caller deadline")
		return true, nil
	}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Poll returned %v, want deadline exceeded", err)
	}
}

func TestIsTransientKubectlError(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{name: "no route", output: "Unable to connect to the server: dial tcp 192.0.2.1:6443: connect: no route to host", want: true},
		{name: "refused", output: "The connection to the server 192.0.2.1:6443 was refused - did you specify the right host or port?", want: true},
		{name: "OpenAPI unavailable", output: `error: error validating "manifest.yaml": error validating data: failed to download openapi: Get "https://192.0.2.1:6443/openapi/v2?timeout=32s": dial tcp 192.0.2.1:6443: connect: no route to host`, want: true},
		{name: "patch transport", output: `error when applying patch: Patch "https://192.0.2.1:6443/api/v1/namespaces/test/pods/pod": read tcp: connection reset by peer`, want: true},
		{name: "TLS timeout", output: "Unable to connect to the server: net/http: TLS handshake timeout", want: true},
		{name: "EOF", output: `Unable to connect to the server: EOF`, want: true},
		{name: "unknown certificate", output: "Unable to connect to the server: tls: failed to verify certificate: x509: certificate signed by unknown authority"},
		{name: "webhook failure", output: `Error from server (InternalError): failed calling webhook: Post "https://webhook.example/validate": dial tcp 192.0.2.2:443: connect: connection refused`},
		{name: "conversion webhook", output: `error: conversion webhook failed: Get "https://webhook.example/convert": dial tcp: connection refused`},
		{name: "forbidden", output: "Error from server (Forbidden): pods is forbidden"},
		{name: "validation error", output: "The Pod is invalid: spec.containers is required"},
		{name: "conflict", output: "Error from server (Conflict): resource version is too old"},
		{name: "unknown DNS name", output: "Unable to connect to the server: dial tcp: lookup missing.example: no such host"},
		{name: "command not found", output: "kubectl: command not found"},
		{name: "unattributed network error", output: "dial tcp 192.0.2.2:443: connection refused"},
		{name: "mixed validation and transport", output: "Error from server (BadRequest): invalid manifest\nUnable to connect to the server: dial tcp: connection refused"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsTransientKubectlError(test.output); got != test.want {
				t.Fatalf("IsTransientKubectlError(%q) = %v, want %v", test.output, got, test.want)
			}
		})
	}
}
