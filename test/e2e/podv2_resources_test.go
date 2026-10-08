// Copyright © 2026 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0
package e2e

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	common "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	p2 "github.com/vmware-tanzu/nsx-operator/test/e2e/podv2"
)

func (s *p2Suite) podObject(name string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: s.journal.Run + "-" + name, Namespace: s.namespace(), Labels: s.labels()}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "workload", Image: *p2Image, Command: []string{"sh", "-c", "mkdir -p /tmp/www; echo podv2-e2e > /tmp/www/index.html; httpd -f -p 8080 -h /tmp/www"}}}, TerminationGracePeriodSeconds: ptr.To[int64](1)}}
}
func (s *p2Suite) pod(t *testing.T, name string) *corev1.Pod {
	t.Helper()
	s.step(t, "create Pod "+name)
	p, e := testData.clientset.CoreV1().Pods(s.namespace()).Create(s.ctx, s.podObject(name), metav1.CreateOptions{})
	require.NoError(t, e)
	require.NoError(t, s.rememberPod(p))
	return s.scheduled(t, p.Name)
}
func (s *p2Suite) scheduled(t *testing.T, name string) *corev1.Pod {
	t.Helper()
	var p *corev1.Pod
	s.wait(t, "Pod scheduled: "+name, func(ctx context.Context) (bool, string, error) {
		var e error
		p, e = testData.clientset.CoreV1().Pods(s.namespace()).Get(ctx, name, metav1.GetOptions{})
		if e != nil {
			return false, name, e
		}
		return p.Spec.NodeName != "", fmt.Sprintf("node=%q phase=%s", p.Spec.NodeName, p.Status.Phase), nil
	})
	require.NoError(t, s.rememberPod(p))
	return p
}
func ownedByPod(c api.SubnetPort, uid types.UID) bool {
	for _, o := range c.OwnerReferences {
		if o.Kind == "Pod" && o.UID == uid {
			return true
		}
	}
	return false
}
func (s *p2Suite) podCRs(ctx context.Context, p *corev1.Pod) ([]api.SubnetPort, error) {
	cs, e := testData.crdClientset.CrdV1alpha1().SubnetPorts(p.Namespace).List(ctx, metav1.ListOptions{})
	if e != nil {
		return nil, e
	}
	var out []api.SubnetPort
	for _, c := range cs.Items {
		if ownedByPod(c, p.UID) {
			out = append(out, c)
		}
	}
	return out, nil
}
func ready(cs []api.Condition, want corev1.ConditionStatus) bool {
	for _, c := range cs {
		if c.Type == api.Ready {
			return c.Status == want
		}
	}
	return false
}
func (s *p2Suite) cr(t *testing.T, p *corev1.Pod) *api.SubnetPort {
	t.Helper()
	var result *api.SubnetPort
	s.wait(t, "exactly one Ready CR for Pod UID "+string(p.UID), func(ctx context.Context) (bool, string, error) {
		cs, e := s.podCRs(ctx, p)
		if e != nil {
			return false, "list CRs", e
		}
		if len(cs) != 1 {
			return false, fmt.Sprintf("matching CRs=%d", len(cs)), nil
		}
		result = cs[0].DeepCopy()
		return ready(result.Status.Conditions, corev1.ConditionTrue), fmt.Sprintf("CR=%s conditions=%+v", result.Name, result.Status.Conditions), nil
	})
	return result
}
func tags(p model.VpcSubnetPort, scope string) []string {
	var values []string
	for _, t := range p.Tags {
		if ptr.Deref(t.Scope, "") == scope {
			values = append(values, ptr.Deref(t.Tag, ""))
		}
	}
	return values
}
func (s *p2Suite) port(t *testing.T, p *corev1.Pod) *model.VpcSubnetPort {
	t.Helper()
	var found *model.VpcSubnetPort
	s.wait(t, "exactly one NSX port for Pod UID "+string(p.UID), func(ctx context.Context) (bool, string, error) {
		ps, e := s.ports(common.TagScopePodUID, string(p.UID))
		if e != nil {
			return false, "query NSX", e
		}
		if len(ps) != 1 {
			return false, fmt.Sprintf("matching NSX ports=%d", len(ps)), nil
		}
		found = &ps[0]
		return found.Path != nil, fmt.Sprintf("path=%v", found.Path), nil
	})
	if !contains(s.journal.PortPaths, *found.Path) {
		s.journal.PortPaths = append(s.journal.PortPaths, *found.Path)
		require.NoError(t, s.save())
	}
	return found
}
func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
func (s *p2Suite) verify(t *testing.T, p *corev1.Pod, c *api.SubnetPort, port *model.VpcSubnetPort) {
	t.Helper()
	parts, e := p2.PortPath(ptr.Deref(port.Path, ""))
	require.NoError(t, e)
	s.wait(t, "CR identity and network status match NSX realization", func(ctx context.Context) (bool, string, error) {
		// Search locates the port; direct GET avoids stale search-index fields.
		fresh, e := testData.nsxClient.PortClient.Get(parts[0], parts[1], parts[2], parts[3], parts[4])
		if e != nil {
			return false, "GET port", e
		}
		for scope, want := range map[string]string{common.TagScopePodUID: string(p.UID), common.TagScopeSubnetPortCRUID: string(c.UID), common.TagScopeNamespace: s.namespace(), common.TagScopeNamespaceUID: string(s.namespaceUID())} {
			got := tags(fresh, scope)
			if len(got) != 1 || got[0] != want {
				return false, fmt.Sprintf("tag %s expected exactly [%s], got %v", scope, want, got), nil
			}
		}
		if fresh.Attachment == nil {
			return false, "NSX attachment absent", nil
		}
		if ptr.Deref(fresh.Attachment.AppId, "") != string(p.UID) || ptr.Deref(fresh.Attachment.ContextId, "") == "" || ptr.Deref(fresh.Attachment.Id, "") != c.Status.Attachment.ID {
			return false, fmt.Sprintf("NSX attachment %+v; expected app_id=%s attachment_id=%s and nonempty context", fresh.Attachment, p.UID, c.Status.Attachment.ID), nil
		}
		state, e := testData.nsxClient.PortStateClient.Get(parts[0], parts[1], parts[2], parts[3], parts[4], nil, nil)
		if e != nil {
			return false, "GET realized state", e
		}
		if state.Attachment == nil || ptr.Deref(state.Attachment.Id, "") != c.Status.Attachment.ID {
			return false, "realized attachment does not match CR", nil
		}
		var backendIPs, backendMACs []string
		for _, b := range state.RealizedBindings {
			if b.Binding != nil {
				backendIPs = append(backendIPs, ptr.Deref(b.Binding.IpAddress, ""))
				backendMACs = append(backendMACs, strings.ToLower(ptr.Deref(b.Binding.MacAddress, "")))
			}
		}
		ips, e := p2.IPs(crIPs(c))
		if e != nil {
			return false, "invalid CR IPs", e
		}
		nsxIPs, e := p2.IPs(backendIPs)
		if e != nil {
			return false, "invalid NSX IPs", e
		}
		for _, ip := range ips {
			if !contains(nsxIPs, ip) {
				return false, fmt.Sprintf("CR IPs=%v NSX IPs=%v", ips, nsxIPs), nil
			}
		}
		if len(ips) > 0 && !contains(backendMACs, strings.ToLower(c.Status.NetworkInterfaceConfig.MACAddress)) {
			return false, fmt.Sprintf("CR MAC=%s NSX MACs=%v", c.Status.NetworkInterfaceConfig.MACAddress, backendMACs), nil
		}
		if strings.TrimSuffix(*port.Path, "/ports/"+parts[4]) != c.Status.NetworkInterfaceConfig.SubnetID {
			return false, "CR SubnetID differs from NSX parent", nil
		}
		return true, "identity, attachment, IP and MAC agree", nil
	})
}
func (s *p2Suite) removePod(t *testing.T, p *corev1.Pod) {
	t.Helper()
	s.step(t, "delete Pod "+p.Name)
	e := testData.clientset.CoreV1().Pods(p.Namespace).Delete(s.ctx, p.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &p.UID}})
	require.NoError(t, e)
	s.wait(t, "Pod and CR deleted", func(ctx context.Context) (bool, string, error) {
		_, e := testData.clientset.CoreV1().Pods(p.Namespace).Get(ctx, p.Name, metav1.GetOptions{})
		if !apierrors.IsNotFound(e) {
			return false, "Pod still present", e
		}
		cs, e := s.podCRs(ctx, p)
		return len(cs) == 0, fmt.Sprintf("remaining CRs=%d", len(cs)), e
	})
}
func (s *p2Suite) noPort(t *testing.T, uid string) {
	t.Helper()
	s.wait(t, "NSX port cleanup for "+uid, func(ctx context.Context) (bool, string, error) {
		ps, e := s.ports(common.TagScopePodUID, uid)
		return len(ps) == 0, fmt.Sprintf("ports=%d", len(ps)), e
	})
}
func (s *p2Suite) updatePod(t *testing.T, p *corev1.Pod, f func(*corev1.Pod)) {
	t.Helper()
	require.NoError(t, retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		latest, e := testData.clientset.CoreV1().Pods(p.Namespace).Get(s.ctx, p.Name, metav1.GetOptions{})
		if e != nil {
			return e
		}
		if latest.UID != p.UID {
			return fmt.Errorf("Pod replaced")
		}
		f(latest)
		_, e = testData.clientset.CoreV1().Pods(p.Namespace).Update(s.ctx, latest, metav1.UpdateOptions{})
		return e
	}))
}
func (s *p2Suite) hostAnnotation(t *testing.T, c *api.SubnetPort, value string) {
	t.Helper()
	require.NoError(t, retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		latest, e := testData.crdClientset.CrdV1alpha1().SubnetPorts(c.Namespace).Get(s.ctx, c.Name, metav1.GetOptions{})
		if e != nil {
			return e
		}
		if latest.UID != c.UID {
			return fmt.Errorf("CR replaced")
		}
		if latest.Annotations == nil {
			latest.Annotations = map[string]string{}
		}
		if value == "" {
			delete(latest.Annotations, common.AnnotationESXHostName)
		} else {
			latest.Annotations[common.AnnotationESXHostName] = value
		}
		_, e = testData.crdClientset.CrdV1alpha1().SubnetPorts(c.Namespace).Update(s.ctx, latest, metav1.UpdateOptions{})
		return e
	}))
}
func (s *p2Suite) node(t *testing.T, name string) model.HostTransportNode {
	t.Helper()
	res, e := testData.nsxClient.HostTransPortNodesClient.List("default", "default", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	require.NoError(t, e)
	for _, n := range res.Results {
		if n.NodeDeploymentInfo != nil && strings.EqualFold(ptr.Deref(n.NodeDeploymentInfo.Fqdn, ""), name) {
			require.NotEmpty(t, ptr.Deref(n.UniqueId, ""))
			return n
		}
	}
	t.Fatalf("PRECONDITION: Node %q has no NSX transport node with matching FQDN", name)
	return model.HostTransportNode{}
}
func (s *p2Suite) defaultLabel(ctx context.Context, name string, on bool) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		c, e := s.operatorCRD.CrdV1alpha1().SubnetSets(s.namespace()).Get(ctx, name, metav1.GetOptions{})
		if e != nil {
			return e
		}
		if c.Labels == nil {
			c.Labels = map[string]string{}
		}
		if on {
			c.Labels[common.LabelDefaultNetwork] = common.DefaultPodNetwork
		} else {
			delete(c.Labels, common.LabelDefaultNetwork)
		}
		_, e = s.operatorCRD.CrdV1alpha1().SubnetSets(c.Namespace).Update(ctx, c, metav1.UpdateOptions{})
		return e
	})
}
func (s *p2Suite) staticDefault(t *testing.T) {
	t.Helper()
	name := s.journal.Run + "-static"
	_, e := testData.crdClientset.CrdV1alpha1().SubnetSets(s.namespace()).Get(s.ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(e) {
		spec := api.SubnetSetSpec{IPAddressType: s.journal.DefaultSet.Spec.IPAddressType, AccessMode: api.AccessMode(api.AccessModePrivate), SubnetDHCPConfig: api.SubnetDHCPConfig{Mode: api.DHCPConfigMode(api.DHCPConfigModeDeactivated)}}
		if spec.IPAddressType != api.IPAddressTypeIPv6 {
			spec.IPv4SubnetSize = 32
		}
		if spec.IPAddressType != api.IPAddressTypeIPv4 {
			spec.IPv6PrefixLength = 64
		}
		var lastErr error
		pollErr := wait.PollUntilContextTimeout(s.ctx, 2*time.Second, 60*time.Second, true, func(ctx context.Context) (bool, error) {
			_, e = testData.crdClientset.CrdV1alpha1().SubnetSets(s.namespace()).Create(ctx, &api.SubnetSet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.namespace(), Labels: s.labels()}, Spec: spec}, metav1.CreateOptions{})
			if e == nil || apierrors.IsAlreadyExists(e) {
				return true, nil
			}
			lastErr = e
			if strings.Contains(e.Error(), "failed calling webhook") || strings.Contains(e.Error(), "connection refused") {
				return false, nil
			}
			return false, e
		})
		require.NoError(t, pollErr, "failed creating static default SubnetSet: %v", lastErr)
	} else {
		require.NoError(t, e)
	}
	require.NoError(t, s.defaultLabel(s.ctx, s.journal.DefaultSet.Name, false))
	sets, e := testData.crdClientset.CrdV1alpha1().SubnetSets(s.namespace()).List(s.ctx, s.options())
	require.NoError(t, e)
	for _, set := range sets.Items {
		if set.Name != name {
			require.NoError(t, s.defaultLabel(s.ctx, set.Name, false))
		}
	}
	require.NoError(t, s.defaultLabel(s.ctx, name, true))
}
func (s *p2Suite) restoreDefault(ctx context.Context) error {
	sets, e := testData.crdClientset.CrdV1alpha1().SubnetSets(s.namespace()).List(ctx, s.options())
	if e != nil {
		return e
	}
	for _, set := range sets.Items {
		if e = s.defaultLabel(ctx, set.Name, false); e != nil {
			return e
		}
	}
	original, e := testData.crdClientset.CrdV1alpha1().SubnetSets(s.namespace()).Get(ctx, s.journal.DefaultSet.Name, metav1.GetOptions{})
	if e != nil {
		return e
	}
	if original.UID != s.journal.DefaultSet.UID {
		return fmt.Errorf("original default SubnetSet UID changed")
	}
	return s.defaultLabel(ctx, s.journal.DefaultSet.Name, true)
}

func (s *p2Suite) cleanWorkloads(ctx context.Context) error {
	ns := s.namespace()
	actual, e := testData.clientset.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if e != nil {
		return e
	}
	if actual.UID != s.namespaceUID() {
		return fmt.Errorf("namespace UID changed; refusing cleanup")
	}
	sts, e := testData.clientset.AppsV1().StatefulSets(ns).List(ctx, s.options())
	if e != nil {
		return e
	}
	for _, x := range sts.Items {
		e = testData.clientset.AppsV1().StatefulSets(ns).Delete(ctx, x.Name, metav1.DeleteOptions{PropagationPolicy: ptr.To(metav1.DeletePropagationForeground), Preconditions: &metav1.Preconditions{UID: &x.UID}})
		if e != nil && !apierrors.IsNotFound(e) {
			return e
		}
	}
	pods, e := testData.clientset.CoreV1().Pods(ns).List(ctx, s.options())
	if e != nil {
		return e
	}
	for _, p := range pods.Items {
		if e = s.rememberPod(&p); e != nil {
			return e
		}
		e = testData.clientset.CoreV1().Pods(ns).Delete(ctx, p.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &p.UID}})
		if e != nil && !apierrors.IsNotFound(e) {
			return e
		}
	}
	// CRs may not carry Pod labels. Match only journaled owner UIDs.
	crs, e := testData.crdClientset.CrdV1alpha1().SubnetPorts(ns).List(ctx, metav1.ListOptions{})
	if e != nil {
		return e
	}
	for _, c := range crs.Items {
		owned := false
		for _, uid := range s.journal.PodUIDs {
			if ownedByPod(c, types.UID(uid)) {
				owned = true
			}
		}
		if owned {
			e = testData.crdClientset.CrdV1alpha1().SubnetPorts(ns).Delete(ctx, c.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &c.UID}})
			if e != nil && !apierrors.IsNotFound(e) {
				return e
			}
		}
	}
	svcs, e := testData.clientset.CoreV1().Services(ns).List(ctx, s.options())
	if e != nil {
		return e
	}
	for _, svc := range svcs.Items {
		if e = testData.clientset.CoreV1().Services(ns).Delete(ctx, svc.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &svc.UID}}); e != nil && !apierrors.IsNotFound(e) {
			return e
		}
	}
	e = s.poll(ctx, "test workloads removed", func(c context.Context) (bool, string, error) {
		ps, e := testData.clientset.CoreV1().Pods(ns).List(c, s.options())
		if e != nil {
			return false, "list Pods", e
		}
		cs, e := testData.crdClientset.CrdV1alpha1().SubnetPorts(ns).List(c, metav1.ListOptions{})
		if e != nil {
			return false, "list CRs", e
		}
		count := 0
		for _, x := range cs.Items {
			for _, uid := range s.journal.PodUIDs {
				if ownedByPod(x, types.UID(uid)) {
					count++
					break
				}
			}
		}
		st, e := testData.clientset.AppsV1().StatefulSets(ns).List(c, s.options())
		if e != nil {
			return false, "list StatefulSets", e
		}
		services, e := testData.clientset.CoreV1().Services(ns).List(c, s.options())
		if e != nil {
			return false, "list Services", e
		}
		return len(ps.Items) == 0 && count == 0 && len(st.Items) == 0 && len(services.Items) == 0, fmt.Sprintf("Pods=%d CRs=%d StatefulSets=%d Services=%d", len(ps.Items), count, len(st.Items), len(services.Items)), nil
	})
	if e != nil {
		return e
	}
	// A failed product cleanup is reported by the case. Recovery can remove
	// remaining *owned* ports to leave the testbed usable; it never masks a failure.
	ps, e := s.ports(common.TagScopeNamespaceUID, string(s.namespaceUID()))
	if e != nil {
		return e
	}
	for _, p := range ps {
		// A StatefulSet Pod can disappear before the runner records its UID. The
		// run tag plus reserved namespace UID supplies independent ownership proof.
		if contains(tags(p, p2Label), s.journal.Run) && contains(tags(p, common.TagScopeNamespaceUID), string(s.namespaceUID())) {
			for _, uid := range tags(p, common.TagScopePodUID) {
				if uid != "" && !contains(s.journal.PodUIDs, uid) {
					s.journal.PodUIDs = append(s.journal.PodUIDs, uid)
					if e = s.save(); e != nil {
						return e
					}
				}
			}
		}
		if e = s.deletePort(ctx, p); e != nil {
			return e
		}
	}
	return s.poll(ctx, "no NSX ports in reserved test namespace", func(c context.Context) (bool, string, error) {
		ps, e := s.ports(common.TagScopeNamespaceUID, string(s.namespaceUID()))
		return len(ps) == 0, fmt.Sprintf("ports=%d", len(ps)), e
	})
}
func (s *p2Suite) cleanSubnetFixtures(ctx context.Context) error {
	ns := s.namespace()
	sets, e := testData.crdClientset.CrdV1alpha1().SubnetSets(ns).List(ctx, s.options())
	if e != nil {
		return e
	}
	var identities [][2]string
	for _, x := range sets.Items {
		identities = append(identities, [2]string{common.TagScopeSubnetSetCRUID, string(x.UID)})
		if !contains(s.journal.SubnetUIDs, string(x.UID)) {
			s.journal.SubnetUIDs = append(s.journal.SubnetUIDs, string(x.UID))
		}
		if e = s.save(); e != nil {
			return e
		}
		e = testData.crdClientset.CrdV1alpha1().SubnetSets(ns).Delete(ctx, x.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &x.UID}})
		if e != nil && !apierrors.IsNotFound(e) {
			return e
		}
	}
	subnets, e := testData.crdClientset.CrdV1alpha1().Subnets(ns).List(ctx, s.options())
	if e != nil {
		return e
	}
	for _, x := range subnets.Items {
		identities = append(identities, [2]string{common.TagScopeSubnetCRUID, string(x.UID)})
		if !contains(s.journal.SubnetUIDs, string(x.UID)) {
			s.journal.SubnetUIDs = append(s.journal.SubnetUIDs, string(x.UID))
		}
		if e = s.save(); e != nil {
			return e
		}
		e = testData.crdClientset.CrdV1alpha1().Subnets(ns).Delete(ctx, x.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &x.UID}})
		if e != nil && !apierrors.IsNotFound(e) {
			return e
		}
	}
	// Both scopes are checked for journaled UIDs after an interrupted deletion.
	for _, uid := range s.journal.SubnetUIDs {
		identities = append(identities, [2]string{common.TagScopeSubnetSetCRUID, uid}, [2]string{common.TagScopeSubnetCRUID, uid})
	}
	return s.poll(ctx, "test SubnetSets, Subnets and NSX backing subnets deleted", func(c context.Context) (bool, string, error) {
		ss, e := testData.crdClientset.CrdV1alpha1().SubnetSets(ns).List(c, s.options())
		if e != nil {
			return false, "list SubnetSets", e
		}
		sn, e := testData.crdClientset.CrdV1alpha1().Subnets(ns).List(c, s.options())
		if e != nil {
			return false, "list Subnets", e
		}
		if len(ss.Items)+len(sn.Items) > 0 {
			return false, fmt.Sprintf("SubnetSets=%d Subnets=%d", len(ss.Items), len(sn.Items)), nil
		}
		for _, id := range identities {
			res, e := testData.queryResource(common.ResourceTypeSubnet, []string{id[0], id[1]})
			if e != nil {
				return false, "NSX subnet query", e
			}
			if len(res.Results) > 0 {
				return false, fmt.Sprintf("backing subnets for UID %s: %d", id[1], len(res.Results)), nil
			}
		}
		return true, "no backing subnets", nil
	})
}
func (s *p2Suite) sameIdentity(t *testing.T, p *corev1.Pod, c *api.SubnetPort, port *model.VpcSubnetPort) {
	t.Helper()
	now := s.cr(t, p)
	np := s.port(t, p)
	require.Equal(t, c.UID, now.UID)
	require.Equal(t, port.Path, np.Path)
	s.verify(t, p, now, np)
}
func crIPs(c *api.SubnetPort) []string {
	var out []string
	for _, x := range c.Status.NetworkInterfaceConfig.IPAddresses {
		out = append(out, x.IPAddress)
	}
	return out
}
func normalized(t *testing.T, ips []string) []string {
	t.Helper()
	out, e := p2.IPs(ips)
	require.NoError(t, e)
	return out
}
func (s *p2Suite) matchIPs(t *testing.T, want []string, c *api.SubnetPort) {
	t.Helper()
	require.Equal(t, normalized(t, want), normalized(t, crIPs(c)))
}
func hostURL(ip string) string { return "http://" + net.JoinHostPort(ip, "8080") + "/" }
