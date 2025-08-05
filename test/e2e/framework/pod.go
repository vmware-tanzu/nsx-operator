// Copyright © 2019-2021 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package framework

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/utils/ptr"
)

// PodWaitFor polls the K8s apiServer until the specified Pod is found (in the test Namespace) and
// the condition predicate is met (or until the provided timeout expires).
func (data *TestData) PodWaitFor(timeout time.Duration, name string, namespace string, condition PodCondition) (*corev1.Pod, error) {
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		if pod, err := data.ClientSet.CoreV1().Pods(namespace).Get(context.TODO(), name, metav1.GetOptions{}); err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("error when getting Pod '%s': %v", name, err)
		} else {
			return condition(pod)
		}
	})
	if err != nil {
		return nil, err
	}
	return data.ClientSet.CoreV1().Pods(namespace).Get(context.TODO(), name, metav1.GetOptions{})
}

// PodWaitForIPs polls the K8s apiServer until the specified Pod is in the "running" state (or until
// the provided timeout expires). The function then returns the IP addresses assigned to the Pod. If the
// Pod is not using "hostNetwork", the function also checks that an IP address exists in each required
// Address Family in the cluster.
func (data *TestData) PodWaitForIPs(timeout time.Duration, name, namespace string) (*PodIPs, error) {
	pod, err := data.PodWaitFor(timeout, name, namespace, func(pod *corev1.Pod) (bool, error) {
		return pod.Status.Phase == corev1.PodRunning, nil
	})
	if err != nil {
		Log.Error(err, "Failed to wait for Pod becoming RUNNING phase", "Pod", name)
		return nil, err
	}
	// According to the K8s API documentation (https://godoc.org/k8s.io/api/core/v1#PodStatus),
	// the PodIP field should only be empty if the Pod has not yet been scheduled, and "running"
	// implies scheduled.
	if pod.Status.PodIP == "" {
		return nil, fmt.Errorf("pod is running but has no assigned IP, which should never happen")
	}
	podIPStrings := sets.New[string](pod.Status.PodIP)
	for _, podIP := range pod.Status.PodIPs {
		ipStr := strings.TrimSpace(podIP.IP)
		if ipStr != "" {
			podIPStrings.Insert(ipStr)
		}
	}
	ips, err := parsePodIPs(podIPStrings)
	if err != nil {
		Log.Error(err, "Failed to parse Pod's IP", "Pod", name)
		return nil, err
	}

	if !pod.Spec.HostNetwork {
		if ClusterInfoData.PodV4NetworkCIDR != "" && ips.IPv4 == nil {
			return nil, fmt.Errorf("no IPv4 address is assigned while cluster was configured with IPv4 Pod CIDR %s", ClusterInfoData.PodV4NetworkCIDR)
		}
		if ClusterInfoData.PodV6NetworkCIDR != "" && ips.IPv6 == nil {
			return nil, fmt.Errorf("no IPv6 address is assigned while cluster was configured with IPv6 Pod CIDR %s", ClusterInfoData.PodV6NetworkCIDR)
		}
		if ips.IPv4 == nil && ips.IPv6 == nil {
			// If e2e test runs in OpenShift, the cluster-cidr is not properly detected. For now just check Pod IPs.
			return nil, fmt.Errorf("no IPv4 or IPv6 address is assigned to Pod %s/%s", namespace, name)
		}
	}
	return ips, nil
}

// parsePodIPs parses a set of IP address strings into a PodIPs struct
func parsePodIPs(podIPStrings sets.Set[string]) (*PodIPs, error) {
	ips := new(PodIPs)
	for podIP := range podIPStrings {
		ip := net.ParseIP(podIP)
		if ip.To4() != nil {
			if ips.IPv4 != nil && podIP != ips.IPv4.String() {
				return nil, fmt.Errorf("pod is assigned multiple IPv4 addresses: %s and %s", ips.IPv4.String(), podIP)
			}
			if ips.IPv4 == nil {
				ips.IPv4 = &ip
				ips.IPStrings = append(ips.IPStrings, podIP)
			}
		} else {
			if ips.IPv6 != nil && podIP != ips.IPv6.String() {
				return nil, fmt.Errorf("pod is assigned multiple IPv6 addresses: %s and %s", ips.IPv6.String(), podIP)
			}
			if ips.IPv6 == nil {
				ips.IPv6 = &ip
				ips.IPStrings = append(ips.IPStrings, podIP)
			}
		}
	}
	if len(ips.IPStrings) == 0 {
		return nil, fmt.Errorf("pod is running but has no assigned IP, which should never happen")
	}
	return ips, nil
}

// RunCommandFromPod runs the provided command in the specified Container for the give Pod and returns the contents of
// stdout and stderr as strings. An error either indicates that the command couldn't be run or that
// the command returned a non-zero error code.
func (data *TestData) RunCommandFromPod(namespace string, podName string, containerName string, cmd []string) (stdout string, stderr string, err error) {
	Log.Info("Running command in Pod's container", "Namespace", namespace, "Pod", podName, "Container", containerName, "Command", cmd)
	request := data.ClientSet.CoreV1().RESTClient().Post().
		Namespace(namespace).
		Resource("pods").
		Name(podName).
		SubResource("exec").
		Param("container", containerName).
		VersionedParams(&corev1.PodExecOptions{
			Command: cmd,
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
			TTY:     false,
		}, scheme.ParameterCodec)
	exec2, err := remotecommand.NewSPDYExecutor(data.KubeConfig, "POST", request.URL())
	if err != nil {
		return "", "", err
	}
	var stdoutB, stderrB bytes.Buffer
	if err := exec2.StreamWithContext(context.Background(), remotecommand.StreamOptions{
		Stdout: &stdoutB,
		Stderr: &stderrB,
	}); err != nil {
		Log.Error(err, "Failed to run command in Pod's container", "Namespace", namespace, "Pod", podName, "Container", containerName, "Command", cmd)
		return stdoutB.String(), stderrB.String(), err
	}
	outStr, errStr := stdoutB.String(), stderrB.String()
	Log.Info("Successfully run command in Pod's container", "Namespace", namespace, "Pod", podName, "Container", containerName, "Command", cmd, "stdOut", outStr, "stdErr", errStr)
	return stdoutB.String(), stderrB.String(), nil
}

// CreatePod creates a pod with the given parameters
func (data *TestData) CreatePod(namespace, podName, containerName, image string, protocol corev1.Protocol, containerPort int32,
	mutators ...func(pod *corev1.Pod),
) (*corev1.Pod, error) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   namespace,
			Annotations: map[string]string{},
			Labels: map[string]string{
				"nsx-op-e2e": podName,
				"app":        podName,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:            containerName,
					Image:           image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Ports: []corev1.ContainerPort{
						{
							Protocol:      protocol,
							ContainerPort: containerPort,
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
			HostNetwork:   false,
			// Set it to 1s for immediate shutdown to reduce test run time and to avoid affecting subsequent tests.
			TerminationGracePeriodSeconds: ptr.To[int64](1),
		},
	}
	for _, mutator := range mutators {
		mutator(&pod)
	}
	return data.ClientSet.CoreV1().Pods(namespace).Create(context.TODO(), &pod, metav1.CreateOptions{})
}
