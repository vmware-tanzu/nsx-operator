/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package e2e

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/client/clientset/versioned"
	typedvpc "github.com/vmware-tanzu/nsx-operator/pkg/client/clientset/versioned/typed/vpc/v1alpha1"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const (
	portSettingOperatorIdentity = "system:serviceaccount:vmware-system-nsx:ncp-svc-account"
	portSettingTestFinalizer    = "example.com/test"
)

// These tests exercise the installed webhook through the API server, including
// real Kubernetes lifecycle controllers. Fixtures do not require NSX port
// settings or an enabled synchronization feature gate. The runner credentials
// must allow impersonation and creation of Namespaces and namespaced RBAC.
func TestSubnetPortSettingWebhook(t *testing.T) {
	TrackTest(t)
	RunSubtest(t, "UserAndOperatorWrites", testPortSettingWebhookWrites)
	for _, policy := range []metav1.DeletionPropagation{
		metav1.DeletePropagationBackground, metav1.DeletePropagationForeground, metav1.DeletePropagationOrphan,
	} {
		RunSubtest(t, "GarbageCollection/"+string(policy), func(t *testing.T) {
			testPortSettingWebhookGC(t, policy)
		})
	}
	RunSubtest(t, "ForegroundMirrorDeletion", testPortSettingWebhookForegroundDeletion)
	RunSubtest(t, "NamespaceCleanup", testPortSettingWebhookNamespaceCleanup)
}

type portSettingWebhookFixture struct {
	namespace *corev1.Namespace
	operator  typedvpc.SubnetPortSettingInterface
	user      typedvpc.SubnetPortSettingInterface
}

func newPortSettingWebhookFixture(t *testing.T, ctx context.Context) *portSettingWebhookFixture {
	t.Helper()
	operatorCRDs, operatorKube := portSettingWebhookClients(t, portSettingOperatorIdentity)
	namespace, err := testData.clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "e2e-setting-webhook-"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	f := &portSettingWebhookFixture{namespace: namespace, operator: operatorCRDs.CrdV1alpha1().SubnetPortSettings(namespace.Name)}
	// Cleanup must remain usable after an assertion or the test context fails.
	t.Cleanup(func() { f.cleanup(t) })
	account, err := testData.clientset.CoreV1().ServiceAccounts(namespace.Name).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "setting-user"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	role, err := testData.clientset.RbacV1().Roles(namespace.Name).Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "setting-writer"},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{v1alpha1.GroupVersion.Group},
			Resources: []string{"subnetportsettings", "subnetportsettings/status"},
			Verbs:     []string{"create", "get", "list", "update", "patch", "delete", "deletecollection"},
		}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = testData.clientset.RbacV1().RoleBindings(namespace.Name).Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "setting-writers"},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: role.Name},
		Subjects: []rbacv1.Subject{
			{Kind: "ServiceAccount", Name: account.Name, Namespace: namespace.Name},
			{Kind: "User", APIGroup: rbacv1.GroupName, Name: portSettingOperatorIdentity},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	userCRDs, userKube := portSettingWebhookClients(t, fmt.Sprintf("system:serviceaccount:%s:%s", namespace.Name, account.Name))
	f.user = userCRDs.CrdV1alpha1().SubnetPortSettings(namespace.Name)
	// RBAC denial must never be mistaken for admission protection. Wait for the
	// scoped grants to propagate, then require the webhook's own denial below.
	for _, client := range []kubernetes.Interface{userKube, operatorKube} {
		err = wait.PollUntilContextTimeout(ctx, time.Second, defaultTimeout, true, func(ctx context.Context) (bool, error) {
			for _, resource := range []string{"subnetportsettings", "subnetportsettings/status"} {
				verbs := []string{"get", "create", "update", "patch", "delete", "deletecollection"}
				if resource == "subnetportsettings/status" {
					verbs = []string{"update", "patch"}
				}
				for _, verb := range verbs {
					attributes := &authorizationv1.ResourceAttributes{
						Namespace: namespace.Name, Group: v1alpha1.GroupVersion.Group, Verb: verb, Resource: "subnetportsettings",
					}
					if resource == "subnetportsettings/status" {
						attributes.Subresource = "status"
					}
					review, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{
						Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: attributes},
					}, metav1.CreateOptions{})
					if err != nil {
						return false, err
					}
					if !review.Status.Allowed {
						return false, nil
					}
				}
			}
			return true, nil
		})
		require.NoError(t, err, "impersonated clients must have write permission before testing admission")
	}
	return f
}

func portSettingWebhookClients(t *testing.T, username string) (versioned.Interface, kubernetes.Interface) {
	t.Helper()
	config := rest.CopyConfig(testData.kubeConfig)
	config.Impersonate = rest.ImpersonationConfig{UserName: username, Groups: []string{"system:authenticated"}}
	crds, err := versioned.NewForConfig(config)
	require.NoError(t, err)
	kube, err := kubernetes.NewForConfig(config)
	require.NoError(t, err)
	return crds, kube
}

func (f *portSettingWebhookFixture) cleanup(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*defaultTimeout)
	defer cancel()
	settings, err := testData.crdClientset.CrdV1alpha1().SubnetPortSettings(f.namespace.Name).List(ctx, metav1.ListOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		t.Errorf("list webhook fixtures for cleanup: %v", err)
	}
	if err == nil {
		for _, setting := range settings.Items {
			// Failed admission or foreground-GC assertions may leave these
			// test-introduced finalizers behind. Remove them only in cleanup,
			// after the assertion has already recorded the failure.
			err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				current, err := f.operator.Get(ctx, setting.Name, metav1.GetOptions{})
				if err != nil {
					return ignorePortSettingNotFound(err)
				}
				if current.UID != setting.UID {
					return fmt.Errorf("webhook fixture %s UID changed during cleanup", setting.Name)
				}
				count := len(current.Finalizers)
				current.Finalizers = slices.DeleteFunc(current.Finalizers, func(finalizer string) bool {
					return finalizer == portSettingTestFinalizer || finalizer == metav1.FinalizerDeleteDependents
				})
				if len(current.Finalizers) == count {
					return nil
				}
				_, err = f.operator.Update(ctx, current, metav1.UpdateOptions{})
				return ignorePortSettingNotFound(err)
			})
			if err != nil {
				t.Errorf("remove test finalizers from webhook fixture %s: %v", setting.Name, err)
			}
			uid := setting.UID
			if err := f.operator.Delete(ctx, setting.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
				t.Errorf("delete webhook fixture %s: %v", setting.Name, err)
			}
		}
	}
	uid := f.namespace.UID
	err = testData.clientset.CoreV1().Namespaces().Delete(ctx, f.namespace.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
	if err != nil && !apierrors.IsNotFound(err) {
		t.Errorf("delete webhook test Namespace: %v", err)
		return
	}
	err = wait.PollUntilContextCancel(ctx, time.Second, true, func(ctx context.Context) (bool, error) {
		_, err := testData.clientset.CoreV1().Namespaces().Get(ctx, f.namespace.Name, metav1.GetOptions{})
		return apierrors.IsNotFound(err), ignorePortSettingNotFound(err)
	})
	if err != nil {
		t.Errorf("wait for webhook test Namespace cleanup: %v", err)
	}
}

func portSettingWebhookObject(name string) *v1alpha1.SubnetPortSetting {
	return &v1alpha1.SubnetPortSetting{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.SubnetPortSettingSpec{SubnetName: "webhook-fixture"},
	}
}

func requirePortSettingWebhookDenied(t *testing.T, err error) {
	t.Helper()
	require.ErrorContains(t, err, `admission webhook "subnetportsetting.validating.crd.nsx.vmware.com" denied the request`)
	require.ErrorContains(t, err, "managed by NSX Operator")
}

func testPortSettingWebhookWrites(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*defaultTimeout)
	defer cancel()
	f := newPortSettingWebhookFixture(t, ctx)
	for _, forged := range []bool{false, true} {
		setting := portSettingWebhookObject(fmt.Sprintf("user-created-%t", forged))
		if forged {
			setting.Annotations = map[string]string{servicecommon.AnnotationAssociatedResource: "project-1:vpc-1:subnet-1:default"}
			setting.OwnerReferences = []metav1.OwnerReference{{
				APIVersion: v1alpha1.GroupVersion.String(), Kind: "Subnet", Name: "webhook-fixture", UID: "forged-parent-uid",
			}}
		}
		_, err := f.user.Create(ctx, setting, metav1.CreateOptions{})
		requirePortSettingWebhookDenied(t, err)
		_, err = f.operator.Get(ctx, setting.Name, metav1.GetOptions{})
		require.True(t, apierrors.IsNotFound(err), "denied CREATE must not persist the object: %v", err)
	}
	setting := portSettingWebhookObject("protected")
	setting.Annotations = map[string]string{servicecommon.AnnotationAssociatedResource: "project-1:vpc-1:subnet-1:default"}
	setting, err := f.operator.Create(ctx, setting, metav1.CreateOptions{})
	require.NoError(t, err, "operator CREATE must succeed")
	setting.Spec.SubnetName = "updated-fixture"
	setting, err = f.operator.Update(ctx, setting, metav1.UpdateOptions{})
	require.NoError(t, err, "operator UPDATE must succeed")
	setting.Status.Conditions = []v1alpha1.Condition{{
		Type: v1alpha1.Ready, Status: corev1.ConditionTrue, Reason: "WebhookFixture", LastTransitionTime: metav1.Now(),
	}}
	setting, err = f.operator.UpdateStatus(ctx, setting, metav1.UpdateOptions{})
	require.NoError(t, err, "operator status UPDATE must succeed")
	setting, err = f.operator.Get(ctx, setting.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "updated-fixture", setting.Spec.SubnetName)
	require.Len(t, setting.Status.Conditions, 1)
	require.Equal(t, corev1.ConditionTrue, setting.Status.Conditions[0].Status)
	patches := []struct {
		name string
		body string
		sub  []string
	}{
		{"spec", `{"spec":{"subnetName":"other-subnet"}}`, nil},
		{"source annotation", `{"metadata":{"annotations":{"nsx.vmware.com/associated-resource":"other-source"}}}`, nil},
		{"remove annotations", `{"metadata":{"annotations":null}}`, nil},
		{"labels", `{"metadata":{"labels":{"example.com/test":"changed"}}}`, nil},
		{"owner reference", `{"metadata":{"ownerReferences":[{"apiVersion":"v1","kind":"ConfigMap","name":"forged-parent","uid":"forged-parent-uid"}]}}`, nil},
		{"finalizers", fmt.Sprintf(`{"metadata":{"finalizers":[%q]}}`, portSettingTestFinalizer), nil},
		{"status", `{"status":{"conditions":[]}}`, []string{"status"}},
	}
	for _, patch := range patches {
		RunSubtest(t, "Patch/"+patch.name, func(t *testing.T) {
			_, err := f.user.Patch(ctx, setting.Name, types.MergePatchType, []byte(patch.body), metav1.PatchOptions{}, patch.sub...)
			requirePortSettingWebhookDenied(t, err)
			f.requireUnchanged(t, ctx, setting)
		})
	}
	RunSubtest(t, "Update", func(t *testing.T) {
		changed := setting.DeepCopy()
		changed.Spec.SubnetName = "other-subnet"
		_, err := f.user.Update(ctx, changed, metav1.UpdateOptions{})
		requirePortSettingWebhookDenied(t, err)
		f.requireUnchanged(t, ctx, setting)
	})
	RunSubtest(t, "UpdateStatus", func(t *testing.T) {
		changed := setting.DeepCopy()
		changed.Status.Conditions[0].Status = corev1.ConditionFalse
		_, err := f.user.UpdateStatus(ctx, changed, metav1.UpdateOptions{})
		requirePortSettingWebhookDenied(t, err)
		f.requireUnchanged(t, ctx, setting)
	})
	RunSubtest(t, "Delete", func(t *testing.T) {
		requirePortSettingWebhookDenied(t, f.user.Delete(ctx, setting.Name, metav1.DeleteOptions{}))
		f.requireUnchanged(t, ctx, setting)
	})
	RunSubtest(t, "DeleteCollection", func(t *testing.T) {
		requirePortSettingWebhookDenied(t, f.user.DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{}))
		f.requireUnchanged(t, ctx, setting)
	})
	uid := setting.UID
	require.NoError(t, f.operator.Delete(ctx, setting.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}))
	f.requireDeleted(t, ctx, setting.Name)
}

func (f *portSettingWebhookFixture) requireUnchanged(t *testing.T, ctx context.Context, expected *v1alpha1.SubnetPortSetting) {
	t.Helper()
	actual, err := f.operator.Get(ctx, expected.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, expected, actual, "denied write must leave the persisted object unchanged")
}

func (f *portSettingWebhookFixture) requireDeleted(t *testing.T, ctx context.Context, name string) {
	t.Helper()
	err := wait.PollUntilContextTimeout(ctx, time.Second, defaultTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := f.operator.Get(ctx, name, metav1.GetOptions{})
		return apierrors.IsNotFound(err), ignorePortSettingNotFound(err)
	})
	require.NoError(t, err, "wait for SubnetPortSetting %s deletion", name)
}

func ignorePortSettingNotFound(err error) error {
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func testPortSettingWebhookGC(t *testing.T, policy metav1.DeletionPropagation) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*defaultTimeout)
	defer cancel()
	f := newPortSettingWebhookFixture(t, ctx)
	// A ConfigMap owner isolates admission/GC behavior from NSX Subnet
	// provisioning and the independent shared Subnet admission rules.
	parent, err := testData.clientset.CoreV1().ConfigMaps(f.namespace.Name).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "parent"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	setting := portSettingWebhookObject("dependent")
	setting.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: parent.Name, UID: parent.UID}}
	setting, err = f.operator.Create(ctx, setting, metav1.CreateOptions{})
	require.NoError(t, err)
	requirePortSettingWebhookDenied(t, f.user.Delete(ctx, setting.Name, metav1.DeleteOptions{}))
	uid := parent.UID
	require.NoError(t, testData.clientset.CoreV1().ConfigMaps(f.namespace.Name).Delete(ctx, parent.Name, metav1.DeleteOptions{
		PropagationPolicy: &policy, Preconditions: &metav1.Preconditions{UID: &uid},
	}))
	err = wait.PollUntilContextTimeout(ctx, time.Second, defaultTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := testData.clientset.CoreV1().ConfigMaps(f.namespace.Name).Get(ctx, parent.Name, metav1.GetOptions{})
		return apierrors.IsNotFound(err), ignorePortSettingNotFound(err)
	})
	require.NoError(t, err, "wait for GC owner deletion")
	if policy != metav1.DeletePropagationOrphan {
		f.requireDeleted(t, ctx, setting.Name)
		return
	}
	var orphan *v1alpha1.SubnetPortSetting
	err = wait.PollUntilContextTimeout(ctx, time.Second, defaultTimeout, true, func(ctx context.Context) (bool, error) {
		orphan, err = f.operator.Get(ctx, setting.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		return len(orphan.OwnerReferences) == 0, nil
	})
	require.NoError(t, err, "GC must remove the owner reference without deleting the orphan")
	require.Equal(t, setting.UID, orphan.UID)
	require.Equal(t, setting.Spec, orphan.Spec)
	require.Nil(t, orphan.DeletionTimestamp)
	_, err = f.user.Patch(ctx, orphan.Name, types.MergePatchType, []byte(`{"spec":{"subnetName":"other-subnet"}}`), metav1.PatchOptions{})
	requirePortSettingWebhookDenied(t, err)
	requirePortSettingWebhookDenied(t, f.user.Delete(ctx, orphan.Name, metav1.DeleteOptions{}))
	f.requireUnchanged(t, ctx, orphan)
}

func testPortSettingWebhookForegroundDeletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*defaultTimeout)
	defer cancel()
	f := newPortSettingWebhookFixture(t, ctx)
	setting, err := f.operator.Create(ctx, portSettingWebhookObject("foreground-owner"), metav1.CreateOptions{})
	require.NoError(t, err)
	blockOwnerDeletion := true
	child, err := testData.clientset.CoreV1().ConfigMaps(f.namespace.Name).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "blocking-dependent",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: v1alpha1.GroupVersion.String(), Kind: "SubnetPortSetting", Name: setting.Name,
				UID: setting.UID, BlockOwnerDeletion: &blockOwnerDeletion,
			}},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	policy, uid := metav1.DeletePropagationForeground, setting.UID
	require.NoError(t, f.operator.Delete(ctx, setting.Name, metav1.DeleteOptions{
		PropagationPolicy: &policy, Preconditions: &metav1.Preconditions{UID: &uid},
	}))
	f.requireDeleted(t, ctx, setting.Name)
	// Completion requires the GC to delete the blocking dependent and remove
	// its foregroundDeletion finalizer from the protected mirror.
	_, err = testData.clientset.CoreV1().ConfigMaps(f.namespace.Name).Get(ctx, child.Name, metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(err), "foreground deletion must remove the blocking dependent: %v", err)
}

func testPortSettingWebhookNamespaceCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*defaultTimeout)
	defer cancel()
	f := newPortSettingWebhookFixture(t, ctx)
	for _, name := range []string{"first", "second"} {
		_, err := f.operator.Create(ctx, portSettingWebhookObject(name), metav1.CreateOptions{})
		require.NoError(t, err)
		requirePortSettingWebhookDenied(t, f.user.Delete(ctx, name, metav1.DeleteOptions{}))
	}
	// Do not remove the fixtures ourselves until this assertion completes:
	// Namespace cleanup must succeed through the actual controller identity.
	uid := f.namespace.UID
	require.NoError(t, testData.clientset.CoreV1().Namespaces().Delete(ctx, f.namespace.Name, metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{UID: &uid},
	}))
	err := wait.PollUntilContextTimeout(ctx, time.Second, 2*defaultTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := testData.clientset.CoreV1().Namespaces().Get(ctx, f.namespace.Name, metav1.GetOptions{})
		return apierrors.IsNotFound(err), ignorePortSettingNotFound(err)
	})
	require.NoError(t, err, "Namespace controller must be able to delete protected fixtures")
	for _, name := range []string{"first", "second"} {
		_, err := testData.crdClientset.CrdV1alpha1().SubnetPortSettings(f.namespace.Name).Get(ctx, name, metav1.GetOptions{})
		require.True(t, apierrors.IsNotFound(err), "Namespace cleanup must remove %s: %v", name, err)
	}
}
