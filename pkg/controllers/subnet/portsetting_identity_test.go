package subnet

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	subnetservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
)

func TestGenerateValidPortSettingName(t *testing.T) {
	for _, tc := range []struct {
		name, id  string
		uniqueID  *string
		want, err string
	}{
		{name: "valid ID", id: "setting.example-1", want: "setting.example-1"},
		{name: "valid ID wins", id: "setting", uniqueID: ptr.To("BAD_UID"), want: "setting"},
		{name: "invalid ID", id: "Invalid_ID", uniqueID: ptr.To("f090a67a-1c26-4f6a-ae31-12b36d800885"), want: "f090a67a-1c26-4f6a-ae31-12b36d800885"},
		{name: "long ID", id: strings.Repeat("a", 254), uniqueID: ptr.To("stable-id"), want: "stable-id"},
		{name: "missing unique ID", id: "Invalid_ID", err: "missing unique_id"},
		{name: "empty unique ID", id: "Invalid_ID", uniqueID: ptr.To(""), err: "missing unique_id"},
		{name: "invalid unique ID", id: "Invalid_ID", uniqueID: ptr.To("Bad_UID"), err: "unusable unique_id"},
		{name: "long unique ID", id: "Invalid_ID", uniqueID: ptr.To(strings.Repeat("a", 254)), err: "unusable unique_id"},
		{name: "missing backend ID", uniqueID: ptr.To("stable-id"), err: "missing its NSX ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, err := generateValidPortSettingName(model.VpcSubnetPortSetting{Id: ptr.To(tc.id), UniqueId: tc.uniqueID})
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				assert.Empty(t, name)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, name)
			}
		})
	}
}

func inventoryParent(namespace string) *v1alpha1.Subnet {
	return &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{
		Name: "shared", Namespace: namespace, UID: types.UID(namespace + "-parent"),
		Annotations: map[string]string{common.AnnotationAssociatedResource: "project:vpc:subnet"},
	}}
}

func inventoryMirror(parent *v1alpha1.Subnet, name, id string) *v1alpha1.SubnetPortSetting {
	return &v1alpha1.SubnetPortSetting{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: parent.Namespace,
			OwnerReferences: portConfigTestOwnerReferences(parent),
			Annotations:     map[string]string{common.AnnotationAssociatedResource: parent.Annotations[common.AnnotationAssociatedResource] + ":" + id}},
		Spec: v1alpha1.SubnetPortSettingSpec{SubnetName: parent.Name},
	}
}

func TestInventoryIdentityThroughCacheAndNamespaces(t *testing.T) {
	ctx := context.Background()
	first, second := inventoryParent("first"), inventoryParent("second")
	legacy := inventoryMirror(first, "port-setting-oldhash", "Invalid_ID:with:colons")
	orphan := inventoryMirror(first, "orphan", "deleted")
	oldDefault := inventoryMirror(first, "old-default", "system-setting")
	r := createFakeSubnetReconciler([]client.Object{first, second, legacy, orphan, oldDefault})
	r.SubnetService.NSXConfig.NsxConfig.VpcWcpEnhance = ptr.To(true)
	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool { return true })
	defer patches.Reset()
	calls := 0
	patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListPortSettingsByAssociatedResource",
		func(_ *subnetservice.SubnetService, _ string) ([]model.VpcSubnetPortSetting, error) {
			calls++
			return []model.VpcSubnetPortSetting{
				{Id: ptr.To("valid-setting"), UniqueId: ptr.To("unused-id")},
				{Id: ptr.To("Invalid_ID:with:colons"), UniqueId: ptr.To("stable-unique-id")},
				{Id: ptr.To("system-setting"), IsDefault: ptr.To(true)},
				{Id: ptr.To("valid-setting"), UniqueId: ptr.To("unused-id")},
			}, nil
		})
	for _, parent := range []*v1alpha1.Subnet{first, second} {
		require.NoError(t, r.syncPortConfigsOnDemand(ctx, parent, "project:vpc:subnet"))
	}
	assert.Equal(t, 1, calls, "both namespaces must receive unique_id from the same cached enumeration")
	names := sets.New(client.ObjectKeyFromObject(first), client.ObjectKeyFromObject(second))
	r.pollAndSyncPortConfigs(ctx, "project:vpc:subnet", names)
	assert.Equal(t, 2, calls, "polling must refresh the complete identity snapshot")
	for _, parent := range []*v1alpha1.Subnet{first, second} {
		list := &v1alpha1.SubnetPortSettingList{}
		require.NoError(t, r.APIReader.List(ctx, list, client.InNamespace(parent.Namespace)))
		require.Len(t, list.Items, 2)
		for _, setting := range list.Items {
			id := "valid-setting"
			if setting.Name == "stable-unique-id" {
				id = "Invalid_ID:with:colons"
			} else {
				assert.Equal(t, "valid-setting", setting.Name)
			}
			assert.Equal(t, "project:vpc:subnet:"+id, setting.Annotations[common.AnnotationAssociatedResource])
			assert.Equal(t, id, getPortSettingIDFromCR(&setting))
			assert.Equal(t, parent.Name, setting.Spec.SubnetName)
			assert.Equal(t, portConfigTestOwnerReferences(parent), setting.OwnerReferences)
			assert.Empty(t, setting.GenerateName)
			require.Len(t, setting.Status.Conditions, 1)
			assert.Equal(t, corev1.ConditionTrue, setting.Status.Conditions[0].Status)
		}
	}
}

func TestInventoryUnusableIdentityRetainsExistingMirror(t *testing.T) {
	ctx := context.Background()
	parent := inventoryParent("test")
	legacy := inventoryMirror(parent, "port-setting-oldhash", "Invalid_ID")
	r := createFakeSubnetReconciler([]client.Object{parent, legacy})
	settings := []model.VpcSubnetPortSetting{{Id: ptr.To("Invalid_ID")}, {Id: ptr.To("healthy")}}
	err := r.syncPortConfigsForNamespaceSubnet(ctx, parent, settings, "project:vpc:subnet")
	require.ErrorContains(t, err, "missing unique_id")
	list := &v1alpha1.SubnetPortSettingList{}
	require.NoError(t, r.APIReader.List(ctx, list))
	require.Len(t, list.Items, 2, "an unusable identity must not hide other non-default settings")
	retained := &v1alpha1.SubnetPortSetting{}
	require.NoError(t, r.APIReader.Get(ctx, client.ObjectKeyFromObject(legacy), retained))
	assert.Equal(t, corev1.ConditionFalse, retained.Status.Conditions[0].Status)
	assert.Equal(t, "PortSettingIdentityError", retained.Status.Conditions[0].Reason)
	settings[0].UniqueId = ptr.To("recovered-identity")
	require.NoError(t, r.syncPortConfigsForNamespaceSubnet(ctx, parent, settings, "project:vpc:subnet"))
	assert.True(t, apierrors.IsNotFound(r.APIReader.Get(ctx, client.ObjectKeyFromObject(legacy), retained)))
}

func TestInventoryIncompleteIdentitySnapshotDoesNotMutate(t *testing.T) {
	for _, settings := range [][]model.VpcSubnetPortSetting{
		{{Id: ptr.To("good")}, {}},
		{{Id: ptr.To("same"), UniqueId: ptr.To("one")}, {Id: ptr.To("same"), UniqueId: ptr.To("two")}},
		{{Id: ptr.To("same"), IsDefault: ptr.To(true)}, {Id: ptr.To("same")}},
	} {
		ctx := context.Background()
		parent := inventoryParent("test")
		existing := inventoryMirror(parent, "keep", "keep")
		r := createFakeSubnetReconciler([]client.Object{parent, existing})
		before, after := &v1alpha1.SubnetPortSettingList{}, &v1alpha1.SubnetPortSettingList{}
		require.NoError(t, r.APIReader.List(ctx, before))
		require.Error(t, r.syncPortConfigsForNamespaceSubnet(ctx, parent, settings, "project:vpc:subnet"))
		require.NoError(t, r.APIReader.List(ctx, after))
		assert.Equal(t, before.Items, after.Items)
	}
}

type testNetworkAssociations func(context.Context, string, string, string) ([]NetworkPortSettingAssociation, error)

func (f testNetworkAssociations) ListNetworkAssociations(ctx context.Context, namespace, source, id string) ([]NetworkPortSettingAssociation, error) {
	return f(ctx, namespace, source, id)
}

func TestExplicitNetworkAssociations(t *testing.T) {
	ctx := context.Background()
	parent := inventoryParent("test")
	ps := model.VpcSubnetPortSetting{Id: ptr.To("backend-id"), DisplayName: ptr.To("do-not-infer")}
	r := createFakeSubnetReconciler([]client.Object{parent})
	name, err := r.portSettingName(ctx, parent.Namespace, "project:vpc:subnet", ps)
	require.NoError(t, err)
	assert.Equal(t, "backend-id", name, "a missing source leaves Network-derived naming disabled")
	for _, names := range [][]string{{"z-network", "a-network", "a-network"}, {"a-network", "z-network"}} {
		r.NetworkAssociations = testNetworkAssociations(func(_ context.Context, namespace, source, id string) ([]NetworkPortSettingAssociation, error) {
			assert.Equal(t, parent.Namespace, namespace)
			assert.Equal(t, "project:vpc:subnet", source)
			assert.Equal(t, "backend-id", id)
			var associations []NetworkPortSettingAssociation
			for _, name := range names {
				associations = append(associations, NetworkPortSettingAssociation{Network: types.NamespacedName{Namespace: namespace, Name: name}, VDNName: "vdn", DVPGID: "dvpg"})
			}
			return associations, nil
		})
		require.NoError(t, r.syncPortConfigsForNamespaceSubnet(ctx, parent, []model.VpcSubnetPortSetting{ps, ps}, "project:vpc:subnet"))
		list := &v1alpha1.SubnetPortSettingList{}
		require.NoError(t, r.APIReader.List(ctx, list))
		require.Len(t, list.Items, 1)
		assert.Equal(t, "a-network", list.Items[0].Name)
		assert.Equal(t, "project:vpc:subnet:backend-id", list.Items[0].Annotations[common.AnnotationAssociatedResource])
	}
	r.NetworkAssociations = testNetworkAssociations(func(context.Context, string, string, string) ([]NetworkPortSettingAssociation, error) {
		return nil, nil
	})
	name, err = r.portSettingName(ctx, parent.Namespace, "project:vpc:subnet", ps)
	require.NoError(t, err)
	assert.Equal(t, "backend-id", name, "unassociated settings remain in inventory")
	r.NetworkAssociations = testNetworkAssociations(func(context.Context, string, string, string) ([]NetworkPortSettingAssociation, error) {
		return nil, errors.New("source unavailable")
	})
	name, err = r.portSettingName(ctx, parent.Namespace, "project:vpc:subnet", ps)
	require.ErrorContains(t, err, "source unavailable")
	assert.Empty(t, name)
}

func TestInventoryNameConflictPreservesLegacyAndForeign(t *testing.T) {
	for _, networkName := range []bool{false, true} {
		t.Run(map[bool]string{false: "unique_id", true: "Network"}[networkName], func(t *testing.T) {
			ctx := context.Background()
			parent := inventoryParent("test")
			legacy := inventoryMirror(parent, "oldhash", "Invalid_ID")
			foreign := inventoryMirror(parent, "occupied-name", "different-backend")
			r := createFakeSubnetReconciler([]client.Object{parent, legacy, foreign})
			if networkName {
				r.NetworkAssociations = testNetworkAssociations(func(context.Context, string, string, string) ([]NetworkPortSettingAssociation, error) {
					return []NetworkPortSettingAssociation{{Network: types.NamespacedName{Namespace: parent.Namespace, Name: "occupied-name"}, VDNName: "vdn", DVPGID: "dvpg"}}, nil
				})
			}
			// Even when the conflicting backing is absent, orphan cleanup must
			// not silently free its name during a failed migration.
			settings := []model.VpcSubnetPortSetting{{Id: ptr.To("Invalid_ID"), UniqueId: ptr.To("occupied-name")}}
			before, after := &v1alpha1.SubnetPortSettingList{}, &v1alpha1.SubnetPortSettingList{}
			require.NoError(t, r.APIReader.List(ctx, before))
			err := r.syncPortConfigsForNamespaceSubnet(ctx, parent, settings, "project:vpc:subnet")
			require.ErrorContains(t, err, "conflicts")
			require.NoError(t, r.APIReader.List(ctx, after))
			assert.Equal(t, before.Items, after.Items, "a conflict must neither overwrite a CR nor retire the legacy mirror")
		})
	}
}

type inventoryStatusFailureClient struct{ client.Client }

func (c *inventoryStatusFailureClient) Status() client.StatusWriter {
	return &fakeStatusWriter{updateErr: errors.New("status update failed")}
}

func TestInventoryMigrationRetriesFailedWrites(t *testing.T) {
	for _, phase := range []string{"create", "status"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			parent := inventoryParent("test")
			legacy := inventoryMirror(parent, "legacy-hash", "Invalid_ID")
			r := createFakeSubnetReconciler([]client.Object{parent, legacy})
			baseClient := r.Client
			if phase == "create" {
				r.Client = &portConfigCreateFailureClient{Client: baseClient, err: errors.New("create failed")}
			} else {
				r.Client = &inventoryStatusFailureClient{Client: baseClient}
			}
			before, after := &v1alpha1.SubnetPortSetting{}, &v1alpha1.SubnetPortSetting{}
			require.NoError(t, r.APIReader.Get(ctx, client.ObjectKeyFromObject(legacy), before))
			settings := []model.VpcSubnetPortSetting{{Id: ptr.To("Invalid_ID"), UniqueId: ptr.To("canonical-id")}}
			require.Error(t, r.syncPortConfigsForNamespaceSubnet(ctx, parent, settings, "project:vpc:subnet"))
			require.NoError(t, r.APIReader.Get(ctx, client.ObjectKeyFromObject(legacy), after))
			assert.Equal(t, before, after, "failed replacement must leave the legacy mirror intact")
			r.Client = baseClient
			require.NoError(t, r.syncPortConfigsForNamespaceSubnet(ctx, parent, settings, "project:vpc:subnet"))
			list := &v1alpha1.SubnetPortSettingList{}
			require.NoError(t, r.APIReader.List(ctx, list))
			require.Len(t, list.Items, 1)
			assert.Equal(t, "canonical-id", list.Items[0].Name)
			assert.Equal(t, corev1.ConditionTrue, list.Items[0].Status.Conditions[0].Status)
		})
	}
}

func TestInventoryConflictProtectedRegardlessOfSnapshotOrder(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		ctx := context.Background()
		parent := inventoryParent("test")
		occupied := inventoryMirror(parent, "occupied-name", "First_ID")
		r := createFakeSubnetReconciler([]client.Object{parent, occupied})
		settings := []model.VpcSubnetPortSetting{
			{Id: ptr.To("First_ID"), UniqueId: ptr.To("first-canonical")},
			{Id: ptr.To("Second_ID"), UniqueId: ptr.To("occupied-name")},
		}
		if reverse {
			settings[0], settings[1] = settings[1], settings[0]
		}
		before, after := &v1alpha1.SubnetPortSetting{}, &v1alpha1.SubnetPortSetting{}
		require.NoError(t, r.APIReader.Get(ctx, client.ObjectKeyFromObject(occupied), before))
		require.ErrorContains(t, r.syncPortConfigsForNamespaceSubnet(ctx, parent, settings, "project:vpc:subnet"), "conflicts")
		require.NoError(t, r.APIReader.Get(ctx, client.ObjectKeyFromObject(occupied), after))
		assert.Equal(t, before, after)
	}
}

func TestNetworkAssociationRejectsIncompleteOrForeignChain(t *testing.T) {
	for _, association := range []NetworkPortSettingAssociation{
		{Network: types.NamespacedName{Namespace: "other", Name: "network"}, VDNName: "vdn", DVPGID: "dvpg"},
		{Network: types.NamespacedName{Namespace: "test", Name: "Invalid_Name"}, VDNName: "vdn", DVPGID: "dvpg"},
		{Network: types.NamespacedName{Namespace: "test", Name: "network"}, DVPGID: "dvpg"},
		{Network: types.NamespacedName{Namespace: "test", Name: "network"}, VDNName: "vdn"},
	} {
		r := &SubnetReconciler{NetworkAssociations: testNetworkAssociations(func(context.Context, string, string, string) ([]NetworkPortSettingAssociation, error) {
			return []NetworkPortSettingAssociation{association}, nil
		})}
		name, err := r.portSettingName(context.Background(), "test", "project:vpc:subnet", model.VpcSubnetPortSetting{Id: ptr.To("backend")})
		require.ErrorContains(t, err, "invalid Network-to-VDN-to-DVPG association")
		assert.Empty(t, name)
	}
}
