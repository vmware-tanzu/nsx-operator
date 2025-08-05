// Copyright © 2019-2021 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package framework

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	stderror "errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/third_party/retry"
)

// UseWCPSetup returns whether the test is using WCP setup
func (data *TestData) UseWCPSetup() bool {
	return data.VCClient != nil
}

// VMWaitFor waits for a VM to be ready
func (data *TestData) VMWaitFor(timeout time.Duration, namespace, vmName string) (string, error) {
	var primaryIP4 string
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		cmd := exec.Command("kubectl", "get", "vm", vmName, "-n", namespace, "-o", "jsonpath={.status.network.primaryIP4}")
		output, err := cmd.Output()
		if err != nil {
			var exitError *exec.ExitError
			if stderror.As(err, &exitError) {
				if exitError.ExitCode() == 1 {
					return false, nil
				}
			}
			return false, fmt.Errorf("error when getting VirtualMachine '%s': %v", vmName, err)
		}

		primaryIP4 = strings.TrimSpace(string(output))
		if primaryIP4 == "" {
			return false, nil
		}

		return true, nil
	})
	return primaryIP4, err
}

// CheckTrafficByCurl checks traffic between pods using curl
func CheckTrafficByCurl(ns, podname, containername, ip string, port int32, interval, timeout time.Duration) error {
	// Test traffic from client Pod to server Pod
	url := fmt.Sprintf("http://%s:%d", ip, port)
	cmd := []string{
		`/bin/sh`, "-c", fmt.Sprintf(`curl -s -o /dev/null -w %%{http_code} %s`, url),
	}
	trafficErr := wait.PollUntilContextTimeout(context.TODO(), interval, timeout, true, func(ctx context.Context) (bool, error) {
		stdOut, _, err := Data.RunCommandFromPod(ns, podname, containername, cmd)
		if err != nil {
			return false, nil
		}
		statusCode := strings.Trim(stdOut, `"`)
		if statusCode != "200" {
			Log.Info("Failed to access ip", "ip", ip, "statusCode", statusCode)
			return false, nil
		}
		return true, nil
	})
	return trafficErr
}

// TestSSHConnection tests SSH connection to a host
func TestSSHConnection(host, username, password string, port int, timeout time.Duration, attempts uint, delay time.Duration) error {
	if host == "" || username == "" {
		return fmt.Errorf("host and username are required")
	}

	cfg := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // #nosec G106
		Timeout:         timeout,
	}

	address := net.JoinHostPort(host, strconv.Itoa(port))

	return retry.Do(
		func() error {
			conn, err := ssh.Dial("tcp", address, cfg)
			if err != nil {
				return fmt.Errorf("failed to establish SSH connection to %s: %w", address, err)
			}
			defer func() {
				if closeErr := conn.Close(); closeErr != nil {
					Log.Error(closeErr, "Failed to close SSH connection")
				}
			}()

			session, err := conn.NewSession()
			if err != nil {
				return fmt.Errorf("failed to create SSH session: %w", err)
			}
			defer func() {
				if closeErr := session.Close(); closeErr != nil {
					Log.Error(closeErr, "Failed to close SSH session")
				}
			}()

			return nil
		},
		retry.Attempts(attempts),
		retry.Delay(delay),
		retry.OnRetry(func(n uint, err error) {
			Log.Info("Retrying SSH connection", "attempt", n+1, "total_attempts", attempts, "error", err)
		}),
		retry.LastErrorOnly(true),
	)
}

// GetRandomString generates a random string by hashing the current timestamp
// and taking the first 8 characters of the hex-encoded hash.
func GetRandomString() string {
	timestamp := time.Now().UnixNano()
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d", timestamp)))
	return hex.EncodeToString(hash[:])[:8]
}
