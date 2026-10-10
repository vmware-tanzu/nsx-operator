package subnetport

import (
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func TestBuildPodSubnetPortValidatesOwnerBeforeStatefulSetReuse(t *testing.T) {
	patches := gomonkey.ApplyFunc((*nsx.Client).NSXCheckVersion, func(*nsx.Client, int) bool { return true })
	defer patches.Reset()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "web-0", Namespace: "ns", UID: "new-pod-uid",
		OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "web", UID: "sts-uid", Controller: common.Bool(true)}},
	}}
	api := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns", UID: "namespace-uid"}}).Build()
	svc := &SubnetPortService{Service: common.Service{
		Client: api, NSXClient: &nsx.Client{},
		NSXConfig: &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{VpcWcpEnhance: common.Bool(true)}, CoeConfig: &config.CoeConfig{Cluster: "test"}},
	}, SubnetPortStore: setupStore()}
	path := "/orgs/default/projects/default/vpcs/vpc/subnets/subnet"
	current := &model.VpcSubnetPort{Id: common.String("reused-port"), DisplayName: common.String("web-0"), ParentPath: &path, Tags: []model.Tag{
		{Scope: common.String(common.TagScopeStatefulSetUID), Tag: common.String("sts-uid")},
		{Scope: common.String(common.TagScopePodName), Tag: common.String("web-0")},
		{Scope: common.String(common.TagScopePodUID), Tag: common.String("new-pod-uid")},
	}}
	require.NoError(t, svc.SubnetPortStore.Apply(current))
	sp := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{
		Name: "port", Namespace: "ns", UID: "aaaaaaaa-1234-1234-1234-123456789abc",
		OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: pod.Name, UID: "old-pod-uid", Controller: common.Bool(true)}},
	}, Spec: v1alpha1.SubnetPortSpec{StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone}}
	// A delayed reconcile of the old CR must not rebind the replacement Pod's port.
	built, err := svc.buildSubnetPort(sp, &model.VpcSubnet{Path: &path}, "esx-id", nil, false, false, v1alpha1.IPAddressTypeIPv4)
	require.ErrorContains(t, err, "UID changed from old-pod-uid to new-pod-uid")
	require.Nil(t, built)

	// The current owner's CR can reuse the port and receives each identity tag once.
	sp.OwnerReferences[0].UID = pod.UID
	built, err = svc.buildSubnetPort(sp, &model.VpcSubnet{Path: &path}, "esx-id", nil, false, false, v1alpha1.IPAddressTypeIPv4)
	require.NoError(t, err)
	require.Equal(t, *current.Id, *built.Id)
	require.Equal(t, string(pod.UID), *built.Attachment.AppId)
	for scope, value := range map[string]string{
		common.TagScopeNamespace: "ns", common.TagScopeNamespaceUID: "namespace-uid",
		common.TagScopePodName: pod.Name, common.TagScopePodUID: string(pod.UID),
		common.TagScopeSubnetPortCRUID: string(sp.UID), common.TagScopeStatefulSetUID: "sts-uid",
	} {
		require.Equal(t, value, nsxutil.FindTag(built.Tags, scope))
		count := 0
		for _, tag := range built.Tags {
			if *tag.Scope == scope {
				count++
			}
		}
		require.Equal(t, 1, count, "duplicate tag scope %s", scope)
	}
	require.Empty(t, nsxutil.FindTag(built.Tags, common.TagScopeVMNamespace))
	require.Empty(t, nsxutil.FindTag(built.Tags, common.TagScopeVMNamespaceUID))
}
