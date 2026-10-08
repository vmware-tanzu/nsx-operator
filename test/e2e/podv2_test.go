// Copyright © 2026 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0
package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	common "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	util "github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func TestPodV2(t *testing.T) {
	if !*podV2Only {
		t.Skip("Pod v2 suite runs only in its isolated entry point")
	}
	TrackTest(t)
	s := podV2Suite
	require.NotNil(t, s)
	t.Cleanup(func() {
		if t.Failed() && s.secret != nil {
			s.diagnostics(t)
		}
		require.NoError(t, s.cleanup(), "CLEANUP FAILED: recovery journal retained; run -podv2-cleanup")
	})
	s.preflight(t)
	// A suite-owned default prevents auto-created NSX subnets from remaining on
	// the pre-existing SubnetSet after testing. Its original spec is never edited.
	s.mode(t, true, false)
	s.staticDefault(t)
	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{"TB01_Legacy", s.legacy},
		{"TB02_StaticLifecycle", s.staticLifecycle},
		{"TB03_PrecreatedDHCP", s.dhcp},
		{"TB04_Labels", s.labelCase},
		{"TB05_HostRetry", s.hostCase},
		{"TB06_StatefulSet", s.stateful},
		{"TB07_RestartAndReplacement", s.restartCase},
		{"TB08_RestoreExistingCR", func(t *testing.T) { s.restoreVariants(t, false, false) }},
		{"TB09_RestoreLegacyMissingCR", func(t *testing.T) { s.restoreVariants(t, true, false) }},
		{"TB10_RestoreRetry", func(t *testing.T) { s.restoreVariants(t, false, true) }},
	}
	for _, c := range cases {
		ok := RunSubtest(t, c.name, func(t *testing.T) {
			t.Cleanup(func() {
				if t.Failed() {
					s.diagnostics(t)
				}
			})
			c.run(t)
			// This assertion is part of PASS, not best-effort teardown.
			s.wait(t, "case has no residual workloads or ports", func(ctx context.Context) (bool, string, error) {
				ps, e := testData.clientset.CoreV1().Pods(s.namespace()).List(ctx, s.options())
				if e != nil {
					return false, "list Pods", e
				}
				cs, e := testData.crdClientset.CrdV1alpha1().SubnetPorts(s.namespace()).List(ctx, metav1.ListOptions{})
				if e != nil {
					return false, "list CRs", e
				}
				ports, e := s.ports(common.TagScopeNamespaceUID, string(s.namespaceUID()))
				return len(ps.Items) == 0 && len(cs.Items) == 0 && len(ports) == 0, fmt.Sprintf("Pods=%d CRs=%d ports=%d", len(ps.Items), len(cs.Items), len(ports)), e
			})
		})
		if !ok {
			t.Log("Stopping remaining cases after a failure; suite rollback follows.")
			break
		}
	}
}

func (s *p2Suite) legacy(t *testing.T) {
	s.mode(t, false, false)
	server := s.pod(t, "legacy-server")
	client := s.pod(t, "legacy-client")
	for _, p := range []*corev1.Pod{server, client} {
		s.wait(t, "legacy Pod Running with network annotations", func(ctx context.Context) (bool, string, error) {
			latest, e := testData.clientset.CoreV1().Pods(p.Namespace).Get(ctx, p.Name, metav1.GetOptions{})
			if e != nil {
				return false, p.Name, e
			}
			*p = *latest
			return p.Status.Phase == corev1.PodRunning && p.Status.PodIP != "" && p.Annotations[common.AnnotationPodMAC] != "" && p.Annotations[common.AnnotationAttachment] != "", fmt.Sprintf("%s phase=%s ip=%s mac=%s", p.Name, p.Status.Phase, p.Status.PodIP, p.Annotations[common.AnnotationPodMAC]), nil
		})
		cs, e := s.podCRs(s.ctx, p)
		require.NoError(t, e)
		require.Empty(t, cs)
		port := s.port(t, p)
		require.Empty(t, tags(*port, common.TagScopeSubnetPortCRUID))
	}
	// A dedicated service selector must not match the client.
	s.updatePod(t, server, func(p *corev1.Pod) { p.Labels["podv2-e2e-role"] = "server" })
	svc, e := testData.clientset.CoreV1().Services(s.namespace()).Create(s.ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: s.journal.Run + "-http", Labels: s.labels()}, Spec: corev1.ServiceSpec{Selector: map[string]string{p2Label: s.journal.Run, "podv2-e2e-role": "server"}, Ports: []corev1.ServicePort{{Port: 8080, TargetPort: intstr.FromInt32(8080)}}}}, metav1.CreateOptions{})
	require.NoError(t, e)
	fqdn := fmt.Sprintf("%s.%s.svc.cluster.local", svc.Name, s.namespace())
	for _, command := range [][]string{{"wget", "-T", "10", "-qO-", hostURL(server.Status.PodIP)}, {"wget", "-T", "10", "-qO-", fmt.Sprintf("http://%s:8080/", fqdn)}} {
		s.wait(t, "legacy connectivity: "+strings.Join(command, " "), func(ctx context.Context) (bool, string, error) {
			c, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			out, e := s.exec(c, client.Namespace, client.Name, "workload", command)
			return e == nil, strings.TrimSpace(out), e
		})
	}
	require.NoError(t, testData.clientset.CoreV1().Services(s.namespace()).Delete(s.ctx, svc.Name, metav1.DeleteOptions{}))
	for _, p := range []*corev1.Pod{server, client} {
		s.removePod(t, p)
		s.noPort(t, string(p.UID))
	}
}
func (s *p2Suite) staticLifecycle(t *testing.T) {
	s.mode(t, true, false)
	p := s.pod(t, "static")
	c := s.cr(t, p)
	port := s.port(t, p)
	require.Equal(t, s.journal.Run+"-static", c.Spec.SubnetSet)
	require.Equal(t, s.journal.DefaultSet.Spec.IPAddressType, c.Spec.InterfaceIPType)
	require.Equal(t, api.StaticIPAllocationType(c.Spec.InterfaceIPType), c.Spec.StaticIPAllocationType)
	require.NotEmpty(t, c.Status.NetworkInterfaceConfig.MACAddress)
	require.NotEmpty(t, normalized(t, crIPs(c)))
	s.verify(t, p, c, port)
	n := s.node(t, p.Spec.NodeName)
	require.Equal(t, ptr.Deref(n.UniqueId, ""), ptr.Deref(port.Attachment.ContextId, ""))
	s.removePod(t, p)
	s.noPort(t, string(p.UID))
}
func (s *p2Suite) dhcp(t *testing.T) {
	if s.journal.DHCPNamespace == "" {
		t.Skip("Skipping TB03: -podv2-dhcp-namespace not provided with a provisioned pre-created DHCP default Pod SubnetSet; see PODV2.md")
		return
	}
	s.activeNamespace = s.journal.DHCPNamespace
	defer func() {
		if t.Failed() {
			s.diagnostics(t)
		}
		s.activeNamespace = ""
	}()
	s.mode(t, true, true)
	sets, e := testData.crdClientset.CrdV1alpha1().SubnetSets(s.namespace()).List(s.ctx, metav1.ListOptions{LabelSelector: common.LabelDefaultNetwork + "=" + common.DefaultPodNetwork})
	require.NoError(t, e)
	require.Len(t, sets.Items, 1)
	set := sets.Items[0]
	require.NotNil(t, set.Spec.SubnetNames, "TB03 requires pre-created subnets, not an auto-created DHCP SubnetSet")
	require.NotEmpty(t, *set.Spec.SubnetNames)
	p := s.pod(t, "dhcp")
	c := s.cr(t, p)
	port := s.port(t, p)
	require.Equal(t, set.Name, c.Spec.SubnetSet)
	require.Equal(t, api.StaticIPAllocationTypeNone, c.Spec.StaticIPAllocationType)
	require.NotNil(t, port.Attachment)
	require.NotEqual(t, "BOTH", ptr.Deref(port.Attachment.AllocateAddresses, ""))
	require.NotEqual(t, "IP_POOL", ptr.Deref(port.Attachment.AllocateAddresses, ""))
	var backend *model.VpcSubnet
	for _, name := range *set.Spec.SubnetNames {
		subnet, e := testData.crdClientset.CrdV1alpha1().Subnets(s.namespace()).Get(s.ctx, name, metav1.GetOptions{})
		require.NoError(t, e)
		if path := subnet.Annotations[common.AnnotationAssociatedResource]; path != "" {
			if path != c.Status.NetworkInterfaceConfig.SubnetID {
				continue
			}
			parts := strings.Split(strings.Trim(path, "/"), "/")
			require.Len(t, parts, 8)
			found, e := testData.nsxClient.SubnetsClient.Get(parts[1], parts[3], parts[5], parts[7])
			require.NoError(t, e)
			backend = &found
		} else {
			res, e := testData.queryResource(common.ResourceTypeSubnet, []string{common.TagScopeSubnetCRUID, string(subnet.UID)})
			require.NoError(t, e)
			for _, raw := range res.Results {
				v, errs := common.NewConverter().ConvertToGolang(raw, model.VpcSubnetBindingType())
				require.Empty(t, errs)
				found, ok := v.(model.VpcSubnet)
				require.True(t, ok)
				if ptr.Deref(found.Path, "") == c.Status.NetworkInterfaceConfig.SubnetID {
					backend = &found
				}
			}
		}
	}
	require.NotNil(t, backend, "selected parent is outside the configured pre-created Subnets")
	require.False(t, util.NSXSubnetStaticIPAllocationEnabled(backend))
	if set.Spec.IPAddressType != api.IPAddressTypeIPv6 {
		require.NotNil(t, backend.SubnetDhcpConfig)
		require.Equal(t, "DHCP_SERVER", ptr.Deref(backend.SubnetDhcpConfig.Mode, ""))
	}
	if set.Spec.IPAddressType != api.IPAddressTypeIPv4 {
		require.NotNil(t, backend.SubnetDhcpv6Config)
		require.Equal(t, "DHCP_SERVER", ptr.Deref(backend.SubnetDhcpv6Config.Mode, ""))
	}
	require.NoError(t, s.stop(s.ctx))
	require.NoError(t, s.start(s.ctx))
	require.NoError(t, s.normal(s.ctx))
	s.sameIdentity(t, p, c, port)
	s.removePod(t, p)
	s.noPort(t, string(p.UID))
}
func (s *p2Suite) labelCase(t *testing.T) {
	s.mode(t, true, false)
	p := s.pod(t, "labels")
	c := s.cr(t, p)
	port := s.port(t, p)
	const key = "podv2-e2e-label"
	for _, value := range []string{"first", "second", ""} {
		s.updatePod(t, p, func(p *corev1.Pod) {
			if value == "" {
				delete(p.Labels, key)
			} else {
				p.Labels[key] = value
			}
		})
		s.wait(t, "NSX label equals "+value, func(ctx context.Context) (bool, string, error) {
			ps, e := s.ports(common.TagScopePodUID, string(p.UID))
			if e != nil || len(ps) != 1 {
				return false, fmt.Sprintf("ports=%d", len(ps)), e
			}
			got := tags(ps[0], key)
			if value == "" {
				return len(got) == 0, fmt.Sprintf("values=%v", got), nil
			}
			return len(got) == 1 && got[0] == value, fmt.Sprintf("values=%v", got), nil
		})
		s.sameIdentity(t, p, c, port)
	}
	s.removePod(t, p)
	s.noPort(t, string(p.UID))
}
func (s *p2Suite) hostCase(t *testing.T) {
	s.mode(t, true, false)
	p := s.pod(t, "host")
	c := s.cr(t, p)
	port := s.port(t, p)
	node := s.node(t, p.Spec.NodeName)
	require.Equal(t, ptr.Deref(node.UniqueId, ""), ptr.Deref(port.Attachment.ContextId, ""))
	s.hostAnnotation(t, c, p.Spec.NodeName)
	s.wait(t, "valid host annotation reconciled", func(ctx context.Context) (bool, string, error) {
		ps, e := s.ports(common.TagScopePodUID, string(p.UID))
		if e != nil || len(ps) != 1 {
			return false, "query port", e
		}
		return ps[0].Attachment != nil && ptr.Deref(ps[0].Attachment.ContextId, "") == ptr.Deref(node.UniqueId, ""), "context ID must match node UUID", nil
	})
	s.hostAnnotation(t, c, "podv2-test-nonexistent-host")
	s.wait(t, "invalid host produces Ready=False", func(ctx context.Context) (bool, string, error) {
		x, e := testData.crdClientset.CrdV1alpha1().SubnetPorts(c.Namespace).Get(ctx, c.Name, metav1.GetOptions{})
		if e != nil {
			return false, c.Name, e
		}
		return ready(x.Status.Conditions, corev1.ConditionFalse), fmt.Sprintf("conditions=%+v", x.Status.Conditions), nil
	})
	unchanged := s.port(t, p)
	require.Equal(t, port.Attachment.ContextId, unchanged.Attachment.ContextId)
	s.hostAnnotation(t, c, "")
	s.sameIdentity(t, p, c, port)
	s.removePod(t, p)
	s.noPort(t, string(p.UID))
}
func (s *p2Suite) stateful(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		if !RunSubtest(t, fmt.Sprintf("enhance_%t", enabled), func(t *testing.T) {
			if enabled && !testData.nsxClient.NSXCheckVersion(nsx.StatefulSetPod) {
				t.Skip("NSX does not support StatefulSet port reuse")
			}
			s.mode(t, true, enabled)
			name := s.journal.Run + "-sts"
			pod := s.podObject("template")
			svc, e := testData.clientset.CoreV1().Services(s.namespace()).Create(s.ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: s.labels()}, Spec: corev1.ServiceSpec{ClusterIP: "None", Selector: s.labels(), Ports: []corev1.ServicePort{{Port: 8080}}}}, metav1.CreateOptions{})
			require.NoError(t, e)
			sts, e := testData.clientset.AppsV1().StatefulSets(s.namespace()).Create(s.ctx, &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: s.labels()}, Spec: appsv1.StatefulSetSpec{Replicas: ptr.To[int32](2), ServiceName: name, PodManagementPolicy: appsv1.ParallelPodManagement, Selector: &metav1.LabelSelector{MatchLabels: s.labels()}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: s.labels()}, Spec: pod.Spec}}}, metav1.CreateOptions{})
			require.NoError(t, e)
			p0 := s.scheduled(t, name+"-0")
			p1 := s.scheduled(t, name+"-1")
			c0 := s.cr(t, p0)
			_ = s.cr(t, p1)
			oldPort := s.port(t, p0)
			_ = s.port(t, p1)
			require.NoError(t, testData.clientset.CoreV1().Pods(p0.Namespace).Delete(s.ctx, p0.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &p0.UID}}))
			s.wait(t, "StatefulSet replacement UID", func(ctx context.Context) (bool, string, error) {
				p, e := testData.clientset.CoreV1().Pods(p0.Namespace).Get(ctx, p0.Name, metav1.GetOptions{})
				if e != nil {
					return false, "waiting for replacement", e
				}
				return p.UID != p0.UID && p.DeletionTimestamp == nil, fmt.Sprintf("uid=%s old=%s", p.UID, p0.UID), nil
			})
			replacement := s.scheduled(t, p0.Name)
			c := s.cr(t, replacement)
			newPort := s.port(t, replacement)
			require.NotEqual(t, c0.UID, c.UID)
			s.verify(t, replacement, c, newPort)
			if enabled {
				require.Equal(t, oldPort.Path, newPort.Path)
				require.Equal(t, c0.Status.NetworkInterfaceConfig.SubnetID, c.Status.NetworkInterfaceConfig.SubnetID)
				require.Equal(t, []string{string(sts.UID)}, tags(*newPort, common.TagScopeStatefulSetUID))
			} else {
				require.NotEqual(t, oldPort.Path, newPort.Path)
			}
			s.noPort(t, string(p0.UID))
			require.NoError(t, retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				x, e := testData.clientset.AppsV1().StatefulSets(sts.Namespace).Get(s.ctx, sts.Name, metav1.GetOptions{})
				if e != nil {
					return e
				}
				x.Spec.Replicas = ptr.To[int32](1)
				_, e = testData.clientset.AppsV1().StatefulSets(sts.Namespace).Update(s.ctx, x, metav1.UpdateOptions{})
				return e
			}))
			s.noPort(t, string(p1.UID))
			s.sameIdentity(t, replacement, c, newPort)
			require.NoError(t, testData.clientset.AppsV1().StatefulSets(sts.Namespace).Delete(s.ctx, sts.Name, metav1.DeleteOptions{PropagationPolicy: ptr.To(metav1.DeletePropagationForeground)}))
			s.noPort(t, string(replacement.UID))
			s.wait(t, "StatefulSet deleted", func(ctx context.Context) (bool, string, error) {
				_, e := testData.clientset.AppsV1().StatefulSets(sts.Namespace).Get(ctx, sts.Name, metav1.GetOptions{})
				return apierrors.IsNotFound(e), "waiting for StatefulSet garbage collection", nil
			})
			require.NoError(t, testData.clientset.CoreV1().Services(s.namespace()).Delete(s.ctx, svc.Name, metav1.DeleteOptions{}))
		}) {
			return
		}
	}
}
func (s *p2Suite) restartCase(t *testing.T) {
	s.mode(t, true, false)
	var pods []*corev1.Pod
	var crs []*api.SubnetPort
	var ports []*model.VpcSubnetPort
	for i := 0; i < 3; i++ {
		p := s.pod(t, fmt.Sprintf("repeat-%d", i))
		pods = append(pods, p)
		for j := 0; j < 3; j++ {
			s.updatePod(t, p, func(p *corev1.Pod) { p.Labels["podv2-e2e-update"] = fmt.Sprint(j) })
		}
	}
	require.NoError(t, s.stop(s.ctx))
	require.NoError(t, s.start(s.ctx))
	require.NoError(t, s.normal(s.ctx))
	for _, p := range pods {
		crs = append(crs, s.cr(t, p))
		ports = append(ports, s.port(t, p))
	}
	require.NoError(t, s.stop(s.ctx))
	require.NoError(t, s.start(s.ctx))
	require.NoError(t, s.normal(s.ctx))
	for i, p := range pods {
		s.sameIdentity(t, p, crs[i], ports[i])
	}
	old := pods[0]
	s.removePod(t, old)
	s.noPort(t, string(old.UID))
	replacement := s.pod(t, "repeat-0")
	require.NotEqual(t, old.UID, replacement.UID)
	newCR := s.cr(t, replacement)
	require.NotEqual(t, crs[0].UID, newCR.UID)
	s.verify(t, replacement, newCR, s.port(t, replacement))
	pods[0] = replacement
	for _, p := range pods {
		s.removePod(t, p)
		s.noPort(t, string(p.UID))
	}
}

func (s *p2Suite) restoreVariants(t *testing.T, legacy, badHost bool) {
	for _, vif := range []bool{true, false} {
		if !RunSubtest(t, fmt.Sprintf("restore_vif_%t", vif), func(t *testing.T) {
			t.Cleanup(func() {
				if t.Failed() {
					s.diagnostics(t)
				}
			})
			if vif && !testData.nsxClient.NSXCheckVersion(nsx.RestoreVIF) {
				t.Skip("NSX does not support restore_vif=true")
			}
			s.restoreCase(t, legacy, badHost, vif)
		}) {
			return
		}
	}
}
func (s *p2Suite) restoreCase(t *testing.T, legacy, badHost, vif bool) {
	s.mode(t, !legacy, false)
	p := s.pod(t, "restore")
	var saved *api.SubnetPort
	var ips []string
	var mac, attachment, subnet string
	if legacy {
		s.wait(t, "realized legacy Pod restore data", func(ctx context.Context) (bool, string, error) {
			x, e := testData.clientset.CoreV1().Pods(p.Namespace).Get(ctx, p.Name, metav1.GetOptions{})
			if e != nil {
				return false, p.Name, e
			}
			p = x
			return x.Status.Phase == corev1.PodRunning && len(x.Status.PodIPs) > 0 && x.Annotations[common.AnnotationPodMAC] != "" && x.Annotations[common.AnnotationAttachment] != "", fmt.Sprintf("phase=%s IPs=%v annotations present=%t", x.Status.Phase, x.Status.PodIPs, x.Annotations[common.AnnotationPodMAC] != ""), nil
		})
		for _, ip := range p.Status.PodIPs {
			ips = append(ips, ip.IP)
		}
		mac = p.Annotations[common.AnnotationPodMAC]
		attachment = p.Annotations[common.AnnotationAttachment]
		cs, e := s.podCRs(s.ctx, p)
		require.NoError(t, e)
		require.Empty(t, cs)
	} else {
		saved = s.cr(t, p)
		ips = crIPs(saved)
		mac = saved.Status.NetworkInterfaceConfig.MACAddress
		attachment = saved.Status.Attachment.ID
	}
	require.NotEmpty(t, normalized(t, ips))
	require.NotEmpty(t, mac)
	require.NotEmpty(t, attachment)
	oldPort := s.port(t, p)
	require.NotNil(t, oldPort.Path)
	subnet = strings.Split(*oldPort.Path, "/ports/")[0]
	// A fresh operator process must see the absence. Deleting while the operator
	// runs could be repaired in normal mode and would not exercise restoration.
	s.step(t, "stop operator; prepare missing NSX port")
	require.NoError(t, s.configure(s.ctx, true, false, true, vif))
	if badHost {
		s.hostAnnotation(t, saved, "podv2-test-nonexistent-host")
	}
	require.NoError(t, s.deletePort(s.ctx, *oldPort))
	require.NoError(t, s.force(s.ctx, true))
	stamp, e := s.restoreStamp(s.ctx)
	require.NoError(t, e)
	require.NoError(t, s.start(s.ctx))
	s.wait(t, "restore mode entered", func(ctx context.Context) (bool, string, error) {
		current, e := s.operatorLogs(ctx, false)
		previous, _ := s.operatorLogs(ctx, true)
		all := current + previous
		return strings.Contains(all, "Enter restore mode"), "waiting for restore startup log", e
	})
	if badHost {
		s.wait(t, "restore fails on invalid host", func(ctx context.Context) (bool, string, error) {
			current, e := s.operatorLogs(ctx, false)
			previous, _ := s.operatorLogs(ctx, true)
			all := current + previous
			return strings.Contains(all, "podv2-test-nonexistent-host") && (strings.Contains(all, "Failed") || strings.Contains(all, "failed")), "waiting for host lookup failure", e
		})
		after, e := s.restoreStamp(s.ctx)
		require.NoError(t, e)
		require.Equal(t, stamp, after, "failed restore advanced end timestamp")
		ps, e := s.ports(common.TagScopePodUID, string(p.UID))
		require.NoError(t, e)
		require.Empty(t, ps, "invalid host must not create a port")
		s.step(t, "repair host annotation; retry forced restore")
		s.hostAnnotation(t, saved, "")
		// Restart explicitly: do not depend on an unbounded CrashLoopBackOff delay.
		require.NoError(t, s.stop(s.ctx))
		require.NoError(t, s.start(s.ctx))
	}
	s.wait(t, "restore completion timestamp advanced", func(ctx context.Context) (bool, string, error) {
		now, e := s.restoreStamp(ctx)
		return now != "" && now != "-1" && now != stamp, fmt.Sprintf("before=%s after=%s", stamp, now), e
	})
	// The force annotation persists. Stop immediately, inspect durable state,
	// then clear it before a fresh normal-mode process can start.
	require.NoError(t, s.stop(s.ctx))
	c := s.cr(t, p)
	port := s.port(t, p)
	if saved != nil {
		require.Equal(t, saved.UID, c.UID, "restore replaced existing CR")
	}
	s.matchIPs(t, ips, c)
	require.Equal(t, strings.ToLower(mac), strings.ToLower(c.Status.NetworkInterfaceConfig.MACAddress))
	require.Equal(t, subnet, c.Status.NetworkInterfaceConfig.SubnetID)
	if vif {
		require.Equal(t, attachment, c.Status.Attachment.ID)
	} else {
		require.NotEqual(t, attachment, c.Status.Attachment.ID)
		require.NotEmpty(t, c.Status.Attachment.ID)
	}
	s.verify(t, p, c, port)
	require.NoError(t, s.force(s.ctx, false))
	require.NoError(t, s.start(s.ctx))
	require.NoError(t, s.normal(s.ctx))
	s.sameIdentity(t, p, c, port)
	// Reconciliation after an additional restart must reuse both identities.
	require.NoError(t, s.stop(s.ctx))
	require.NoError(t, s.start(s.ctx))
	require.NoError(t, s.normal(s.ctx))
	s.sameIdentity(t, p, c, port)
	s.removePod(t, p)
	s.noPort(t, string(p.UID))
	t.Logf("RESTORE PASS legacy-source=%t retry=%t restore_vif=%t (CR/backend validation only)", legacy, badHost, vif)
}
