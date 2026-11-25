package e2e

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// assertLoadBalancerTraffic records backend and Service state before failing so
// the fixture cleanup cannot erase the evidence needed to diagnose a VIP timeout.
func assertLoadBalancerTraffic(t *testing.T, sourceNS, sourcePod string, backend backendSvc, vip string) {
	t.Helper()
	if checkTrafficByCurl(sourceNS, sourcePod, containerName, vip, backend.port, true) {
		return
	}
	diagnoseLoadBalancerTraffic(t, sourceNS, sourcePod, backend, vip)
	t.Fatalf("Service traffic should work: %s/%s VIP=%s port=%d", backend.namespace, backend.name, vip, backend.port)
}

func diagnoseLoadBalancerTraffic(t *testing.T, sourceNS, sourcePod string, backend backendSvc, vip string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	t.Logf("LB traffic diagnostics: source=%s/%s service=%s/%s VIP=%s port=%d", sourceNS, sourcePod, backend.namespace, backend.name, vip, backend.port)
	svc, err := testData.clientset.CoreV1().Services(backend.namespace).Get(ctx, backend.name, metav1.GetOptions{})
	if err != nil {
		t.Logf("get Service: %v", err)
	} else {
		t.Logf("Service: spec=%+v status=%+v", svc.Spec, svc.Status)
	}
	pod, err := testData.clientset.CoreV1().Pods(backend.namespace).Get(ctx, backend.podName, metav1.GetOptions{})
	if err != nil {
		t.Logf("get backend Pod: %v", err)
	} else {
		t.Logf("backend Pod: name=%s node=%s status=%+v", pod.Name, pod.Spec.NodeName, pod.Status)
	}
	source, err := testData.clientset.CoreV1().Pods(sourceNS).Get(ctx, sourcePod, metav1.GetOptions{})
	if err != nil {
		t.Logf("get source Pod: %v", err)
	} else {
		t.Logf("source Pod: name=%s node=%s status=%+v", source.Name, source.Spec.NodeName, source.Status)
	}
	slices, err := testData.clientset.DiscoveryV1().EndpointSlices(backend.namespace).List(ctx, metav1.ListOptions{LabelSelector: "kubernetes.io/service-name=" + backend.name})
	if err != nil {
		t.Logf("list EndpointSlices: %v", err)
	} else {
		for _, slice := range slices.Items {
			t.Logf("EndpointSlice %s: addressType=%s ports=%+v endpoints=%+v", slice.Name, slice.AddressType, slice.Ports, slice.Endpoints)
		}
		if len(slices.Items) == 0 {
			t.Log("no EndpointSlices found")
		}
	}
	for _, ns := range []string{backend.namespace, sourceNS} {
		events, err := testData.clientset.CoreV1().Events(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			t.Logf("list events in %s: %v", ns, err)
			continue
		}
		for _, event := range events.Items {
			if event.InvolvedObject.Name == backend.name || event.InvolvedObject.Name == backend.podName || event.InvolvedObject.Name == sourcePod {
				t.Logf("event %s/%s: %s %s: %s", ns, event.InvolvedObject.Name, event.Type, event.Reason, event.Message)
			}
		}
	}
	// Probe each path once; these diagnostics do not affect the failed assertion.
	kubeconfig, err := provider.GetKubeconfigPath()
	if err != nil {
		t.Logf("get kubeconfig for traffic probes: %v", err)
		return
	}
	for _, probe := range []struct {
		label, ip string
		port      int32
	}{
		{label: "backend Pod", ip: podIP(pod), port: backend.targetPort},
		{label: "ClusterIP", ip: clusterIP(svc), port: backend.port},
	} {
		if net.ParseIP(probe.ip) == nil {
			t.Logf("%s probe skipped: no valid IP", probe.label)
			continue
		}
		probeCtx, probeCancel := context.WithTimeout(context.Background(), 8*time.Second)
		args := []string{"--kubeconfig", kubeconfig, "exec", "-n", sourceNS, sourcePod, "-c", containerName, "--", "curl", "-sS", "-o", "/dev/null", "-w", "%{http_code}", "--connect-timeout", "2", "--max-time", "5", "http://" + net.JoinHostPort(probe.ip, fmt.Sprint(probe.port))}
		output, probeErr := exec.CommandContext(probeCtx, "kubectl", args...).CombinedOutput()
		probeCancel()
		t.Logf("%s probe ip=%s port=%d: output=%q error=%v", probe.label, probe.ip, probe.port, strings.TrimSpace(string(output)), probeErr)
	}

}

func podIP(pod *corev1.Pod) string {
	if pod == nil {
		return ""
	}
	return pod.Status.PodIP
}
func clusterIP(svc *corev1.Service) string {
	if svc == nil {
		return ""
	}
	return svc.Spec.ClusterIP
}
