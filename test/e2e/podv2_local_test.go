// Copyright © 2026 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0
package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	crdfake "github.com/vmware-tanzu/nsx-operator/pkg/client/clientset/versioned/fake"
	common "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	restoreutil "github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func localSuite(t *testing.T, ncp *unstructured.Unstructured) *p2Suite {
	t.Helper()
	if os.Getenv("PODV2_LOCAL_TESTS") != "true" {
		t.Skip("local fake-client tests have a separate entry point")
	}
	oldData, oldNS, oldDeployment := testData, *p2OperatorNS, *p2Deployment
	*p2OperatorNS = "operator-system"
	*p2Deployment = "operator"
	t.Cleanup(func() { testData = oldData; *p2OperatorNS = oldNS; *p2Deployment = oldDeployment })
	deployment := appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: *p2Deployment, Namespace: *p2OperatorNS, UID: "operator-uid"}, Spec: appsv1.DeploymentSpec{Replicas: ptr.To[int32](2), Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "operator"}}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "operator"}, Annotations: map[string]string{"keep": "original"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "manager", Image: "original", VolumeMounts: []corev1.VolumeMount{{Name: "original", MountPath: "/etc/ncp.ini"}, {Name: "cert", MountPath: "/certs"}}}}, Volumes: []corev1.Volume{{Name: "original"}, {Name: "cert"}}}}}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: *p2Deployment + "-podv2-e2e-state", Namespace: *p2OperatorNS, UID: "journal-uid", Labels: map[string]string{p2Label: "run"}}, Data: map[string][]byte{"original.ini": []byte("[nsx_v3]\npod_v2=false\n[k8s]\ncluster=keep\n")}}
	clients := kubefake.NewClientset(&deployment, secret)
	crds := crdfake.NewSimpleClientset() //nolint:staticcheck // Generated client has no apply configurations / NewClientset.
	testData = &TestData{clientset: clients, crdClientset: crds}
	var objects []runtime.Object
	if ncp != nil {
		objects = append(objects, ncp)
	}
	dyn := fake.NewSimpleDynamicClient(runtime.NewScheme(), objects...)
	return &p2Suite{ctx: context.Background(), dynamic: dyn, secret: secret, operatorCRD: crds, journal: p2Journal{Run: "run", Namespace: "test", NamespaceUID: "namespace-uid", Deployment: deployment, Container: "manager", ConfigPath: "/etc/ncp.ini"}}
}
func localNCP() *unstructured.Unstructured {
	n := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "nsx.vmware.com/v1", "kind": "NCPConfig", "metadata": map[string]interface{}{"name": restoreutil.NSXRestoreStatus, "uid": "ncp-uid"}}}
	n.SetAnnotations(map[string]string{restoreutil.AnnotationForceRestore: "true", restoreutil.AnnotationRestoreEndTime: "123", "another-controller": "keep"})
	return n
}
func TestPodV2LocalRestoreAnnotations(t *testing.T) {
	s := localSuite(t, localNCP())
	s.journal.NCPExists = true
	s.journal.NCPUID = "ncp-uid"
	s.journal.NCPAnnotations = map[string]string{restoreutil.AnnotationRestoreEndTime: "7"}
	require.NoError(t, s.restoreNCP(s.ctx))
	n, e := s.dynamic.Resource(p2NCP).Get(s.ctx, restoreutil.NSXRestoreStatus, metav1.GetOptions{})
	require.NoError(t, e)
	require.Equal(t, map[string]string{restoreutil.AnnotationRestoreEndTime: "7", "another-controller": "keep"}, n.GetAnnotations())
	require.NoError(t, s.restoreNCP(s.ctx), "rollback is idempotent")
}
func TestPodV2LocalRefuseForeignNCPDeletion(t *testing.T) {
	s := localSuite(t, localNCP())
	require.ErrorContains(t, s.restoreNCP(s.ctx), "not owned")
	_, e := s.dynamic.Resource(p2NCP).Get(s.ctx, restoreutil.NSXRestoreStatus, metav1.GetOptions{})
	require.NoError(t, e)
	s.journal.NCPExists = true
	s.journal.NCPUID = "other-uid"
	require.ErrorContains(t, s.restoreNCP(s.ctx), "UID changed")
}
func TestPodV2LocalDeleteOnlyOwnedNewNCP(t *testing.T) {
	n := localNCP()
	n.SetLabels(map[string]string{p2Label: "run"})
	s := localSuite(t, n)
	require.NoError(t, s.restoreNCP(s.ctx))
	_, e := s.dynamic.Resource(p2NCP).Get(s.ctx, restoreutil.NSXRestoreStatus, metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(e))
	require.NoError(t, s.restoreNCP(s.ctx))
}
func TestPodV2LocalTemporaryConfigAndTemplate(t *testing.T) {
	s := localSuite(t, nil)
	original := s.journal.Deployment.DeepCopy()
	require.NoError(t, s.configure(s.ctx, true, true, true, false))
	d, e := testData.clientset.AppsV1().Deployments(*p2OperatorNS).Get(s.ctx, *p2Deployment, metav1.GetOptions{})
	require.NoError(t, e)
	require.Equal(t, int32(0), *d.Spec.Replicas)
	require.Len(t, d.Spec.Template.Spec.Volumes, 3)
	require.Len(t, d.Spec.Template.Spec.Containers[0].VolumeMounts, 3)
	require.Equal(t, p2Volume, d.Spec.Template.Spec.Containers[0].VolumeMounts[2].Name)
	require.NoError(t, s.configure(s.ctx, false, false, false, false))
	d, e = testData.clientset.AppsV1().Deployments(*p2OperatorNS).Get(s.ctx, *p2Deployment, metav1.GetOptions{})
	require.NoError(t, e)
	require.Len(t, d.Spec.Template.Spec.Volumes, 3, "must not accumulate mounts")
	require.NoError(t, s.updateDeployment(s.ctx, func(d *appsv1.Deployment) {
		d.Spec.Template = *original.Spec.Template.DeepCopy()
		d.Spec.Replicas = ptr.To(*original.Spec.Replicas)
	}))
	d, e = testData.clientset.AppsV1().Deployments(*p2OperatorNS).Get(s.ctx, *p2Deployment, metav1.GetOptions{})
	require.NoError(t, e)
	require.Equal(t, original.Spec, d.Spec)
	require.Equal(t, original.Spec, s.journal.Deployment.Spec, "rollback snapshot must never be mutated")
}
func TestPodV2LocalRefuseReplacedDeployment(t *testing.T) {
	s := localSuite(t, nil)
	s.journal.Deployment.UID = "replacement"
	require.ErrorContains(t, s.stop(s.ctx), "UID changed")
	d, e := testData.clientset.AppsV1().Deployments(*p2OperatorNS).Get(s.ctx, *p2Deployment, metav1.GetOptions{})
	require.NoError(t, e)
	require.Equal(t, int32(2), *d.Spec.Replicas)
}
func TestPodV2LocalJournalConflictAndUID(t *testing.T) {
	s := localSuite(t, nil)
	client := testData.clientset.(*kubefake.Clientset)
	attempts := 0
	client.PrependReactor("update", "secrets", func(action ktesting.Action) (bool, runtime.Object, error) {
		attempts++
		if attempts == 1 {
			return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, s.journalName(), errors.New("concurrent update"))
		}
		return false, nil, nil
	})
	s.journal.PodUIDs = []string{"pod-uid"}
	require.NoError(t, s.save())
	require.Equal(t, 2, attempts)
	sec, e := client.CoreV1().Secrets(*p2OperatorNS).Get(s.ctx, s.journalName(), metav1.GetOptions{})
	require.NoError(t, e)
	var j p2Journal
	require.NoError(t, json.Unmarshal(sec.Data["journal.json"], &j))
	require.Equal(t, []string{"pod-uid"}, j.PodUIDs)
	sec.UID = "replaced"
	_, e = client.CoreV1().Secrets(*p2OperatorNS).Update(s.ctx, sec, metav1.UpdateOptions{})
	require.NoError(t, e)
	require.ErrorContains(t, s.save(), "UID changed")
}
func TestPodV2LocalDefaultLabelPreservesSpec(t *testing.T) {
	s := localSuite(t, nil)
	original := &api.SubnetSet{ObjectMeta: metav1.ObjectMeta{Name: "pod-default", Namespace: "test", UID: "set-uid", Labels: map[string]string{common.LabelDefaultNetwork: common.DefaultPodNetwork, "another-label": "keep"}}, Spec: api.SubnetSetSpec{IPAddressType: api.IPAddressTypeIPv4, IPv4SubnetSize: 32}}
	_, e := s.operatorCRD.CrdV1alpha1().SubnetSets("test").Create(s.ctx, original, metav1.CreateOptions{})
	require.NoError(t, e)
	require.NoError(t, s.defaultLabel(s.ctx, original.Name, false))
	require.NoError(t, s.defaultLabel(s.ctx, original.Name, true))
	current, e := s.operatorCRD.CrdV1alpha1().SubnetSets("test").Get(s.ctx, original.Name, metav1.GetOptions{})
	require.NoError(t, e)
	require.Equal(t, original.Spec, current.Spec)
	require.Equal(t, original.Labels, current.Labels)
}

func TestPodV2LocalForeignPortGuard(t *testing.T) {
	s := localSuite(t, nil)
	s.journal.PodUIDs = []string{"owned-pod"}
	port := model.VpcSubnetPort{Path: ptr.To("/orgs/o/projects/p/vpcs/v/subnets/s/ports/port"), Tags: []model.Tag{{Scope: ptr.To(common.TagScopeNamespaceUID), Tag: ptr.To("namespace-uid")}, {Scope: ptr.To(common.TagScopePodUID), Tag: ptr.To("foreign-pod")}}}
	require.ErrorContains(t, s.deletePort(s.ctx, port), "ownership mismatch")
	port.Tags[1].Tag = ptr.To("owned-pod")
	port.Tags[0].Tag = ptr.To("foreign-namespace")
	require.ErrorContains(t, s.deletePort(s.ctx, port), "ownership mismatch")
}
func TestPodV2LocalFailedCleanupKeepsJournal(t *testing.T) {
	s := localSuite(t, nil)
	s.journal.Deployment.UID = "unexpected-uid"
	require.ErrorContains(t, s.cleanup(), "journal retained")
	require.NotNil(t, s.secret)
	_, e := testData.clientset.CoreV1().Secrets(*p2OperatorNS).Get(s.ctx, s.journalName(), metav1.GetOptions{})
	require.NoError(t, e)
}
func TestPodV2LocalReplacedNamespaceGuard(t *testing.T) {
	s := localSuite(t, nil)
	_, e := testData.clientset.CoreV1().Namespaces().Create(s.ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test", UID: "new-namespace"}}, metav1.CreateOptions{})
	require.NoError(t, e)
	require.ErrorContains(t, s.cleanWorkloads(s.ctx), "namespace UID changed")
	require.ErrorContains(t, s.checkNamespace(s.ctx), "namespace UID changed")
}

func TestPodV2LocalReplicaSetCleanupOwnership(t *testing.T) {
	s := localSuite(t, nil)
	for _, name := range []string{"original", "test"} {
		template := s.journal.Deployment.Spec.Template.DeepCopy()
		if name == "test" {
			template.Annotations[p2Label] = s.journal.Run
		}
		_, e := testData.clientset.AppsV1().ReplicaSets(*p2OperatorNS).Create(s.ctx, &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name), OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: *p2Deployment, UID: s.journal.Deployment.UID, Controller: ptr.To(true)}}}, Spec: appsv1.ReplicaSetSpec{Replicas: ptr.To[int32](0), Template: *template}}, metav1.CreateOptions{})
		require.NoError(t, e)
	}
	require.NoError(t, s.cleanReplicaSets(s.ctx))
	_, e := testData.clientset.AppsV1().ReplicaSets(*p2OperatorNS).Get(s.ctx, "test", metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(e))
	_, e = testData.clientset.AppsV1().ReplicaSets(*p2OperatorNS).Get(s.ctx, "original", metav1.GetOptions{})
	require.NoError(t, e)
}
