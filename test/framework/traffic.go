package framework

import (
	"context"
	"errors"
	"strings"

	utilexec "k8s.io/client-go/util/exec"
)

// CurlMatchesExpectation distinguishes a remote curl result from a failed exec
// request. An API outage must never count as evidence that a policy drops
// traffic. For a drop expectation, curl must actually report a timeout (exit 28)
// without receiving an HTTP response.
func CurlMatchesExpectation(stdout string, err error, shouldPass bool) bool {
	statusCode := strings.TrimSpace(strings.Trim(strings.TrimSpace(stdout), `"`))
	if shouldPass {
		return err == nil && statusCode == "200"
	}
	if statusCode != "000" || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var exitError utilexec.ExitError
	return errors.As(err, &exitError) && exitError.Exited() && exitError.ExitStatus() == 28
}
