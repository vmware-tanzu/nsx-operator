package framework

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"

	utilexec "k8s.io/client-go/util/exec"
)

func TestCurlMatchesExpectation(t *testing.T) {
	timeout := utilexec.CodeExitError{Err: errors.New("curl timed out"), Code: 28}
	tests := []struct {
		name       string
		stdout     string
		err        error
		shouldPass bool
		want       bool
	}{
		{name: "allowed", stdout: "200", shouldPass: true, want: true},
		{name: "allowed with whitespace", stdout: "\n200\n", shouldPass: true, want: true},
		{name: "allowed quoted", stdout: `"200"`, shouldPass: true, want: true},
		{name: "allow HTTP failure", stdout: "500", shouldPass: true},
		{name: "allow exec failure", stdout: "200", err: syscall.ECONNREFUSED, shouldPass: true},
		{name: "drop timeout", stdout: "000", err: timeout, want: true},
		{name: "wrapped remote timeout", stdout: "000\n", err: fmt.Errorf("exec: %w", timeout), want: true},
		{name: "drop requires remote exit", stdout: "000"},
		{name: "drop requires no HTTP response", stdout: "200", err: timeout},
		{name: "drop HTTP not found", stdout: "404"},
		{name: "drop HTTP server error", stdout: "500"},
		{name: "SPDY request failed", stdout: "000", err: errors.New("unable to upgrade connection")},
		{name: "API connection refused", stdout: "000", err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}},
		{name: "exec context timeout", stdout: "000", err: context.DeadlineExceeded},
		{name: "exec canceled", stdout: "000", err: context.Canceled},
		{name: "canceled remote result", stdout: "000", err: errors.Join(timeout, context.Canceled)},
		{name: "curl unavailable", stdout: "000", err: utilexec.CodeExitError{Err: errors.New("curl not found"), Code: 127}},
		{name: "connection refused by destination", stdout: "000", err: utilexec.CodeExitError{Err: errors.New("curl could not connect"), Code: 7}},
		{name: "no remote output", err: timeout},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CurlMatchesExpectation(test.stdout, test.err, test.shouldPass); got != test.want {
				t.Fatalf("CurlMatchesExpectation(%q, %v, %v) = %v, want %v", test.stdout, test.err, test.shouldPass, got, test.want)
			}
		})
	}
}
