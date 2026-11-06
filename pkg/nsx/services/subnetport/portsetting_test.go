package subnetport

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const portSettingSubnetPath = "/orgs/default/projects/project/vpcs/vpc/subnets/source-subnet"

func newPortSettingFixture(t *testing.T, objects ...client.Object) (*SubnetPortService, *v1alpha1.SubnetPort, *model.VpcSubnet) {
	t.Helper()
	patches := gomonkey.ApplyFunc((*nsx.Client).NSXCheckVersion, func(*nsx.Client, int) bool { return true })
	t.Cleanup(patches.Reset)
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	objects = append(objects, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "workload", UID: "namespace-uid"}})
	service := &SubnetPortService{
		Service: common.Service{
			Client:    fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build(),
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "test"}},
		},
		SubnetPortStore: setupStore(),
	}
	sp := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{Name: "port", Namespace: "workload", UID: "aaaaaaaa-1234-1234-1234-123456789abc"},
		Spec: v1alpha1.SubnetPortSpec{
			Subnet: "selected-subnet", PortSettingName: "named-setting",
			StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
		},
	}
	return service, sp, &model.VpcSubnet{Path: common.String(portSettingSubnetPath)}
}

func portSettingMirror(name, sourceID string) *v1alpha1.SubnetPortSetting {
	return &v1alpha1.SubnetPortSetting{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "workload",
			Annotations: map[string]string{common.AnnotationAssociatedResource: "project:vpc:source-subnet:" + sourceID},
		},
		Spec: v1alpha1.SubnetPortSettingSpec{SubnetName: "selected-subnet"},
	}
}

func TestBuildSubnetPortPortSetting(t *testing.T) {
	tests := []struct {
		name         string
		prepare      func(*v1alpha1.SubnetPort, *v1alpha1.SubnetPortSetting, *model.VpcSubnet)
		wantPath     *string
		wantError    string
		isNotFound   bool
		deleteMirror bool
	}{
		{name: "named", wantPath: common.String(portSettingSubnetPath + "/port-settings/named-setting")},
		{
			name: "mirrored name differs from NSX ID",
			prepare: func(_ *v1alpha1.SubnetPort, setting *v1alpha1.SubnetPortSetting, _ *model.VpcSubnet) {
				setting.Annotations[common.AnnotationAssociatedResource] = "project:vpc:source-subnet:Legacy_Port-Setting:42"
			},
			wantPath: common.String(portSettingSubnetPath + "/port-settings/Legacy_Port-Setting:42"),
		},
		{
			name: "absent ignores invalid unused mirror",
			prepare: func(sp *v1alpha1.SubnetPort, setting *v1alpha1.SubnetPortSetting, _ *model.VpcSubnet) {
				sp.Spec.PortSettingName = ""
				setting.Annotations = nil
			},
		},
		{
			name: "missing",
			prepare: func(sp *v1alpha1.SubnetPort, _ *v1alpha1.SubnetPortSetting, _ *model.VpcSubnet) {
				sp.Spec.PortSettingName = "missing"
			},
			wantError: "failed to get SubnetPortSetting workload/missing", isNotFound: true,
		},
		{
			name: "setting in another namespace",
			prepare: func(_ *v1alpha1.SubnetPort, setting *v1alpha1.SubnetPortSetting, _ *model.VpcSubnet) {
				setting.Namespace = "another-workload"
			},
			wantError: "failed to get SubnetPortSetting workload/named-setting", isNotFound: true,
		},
		{
			name: "wrong Subnet CR",
			prepare: func(_ *v1alpha1.SubnetPort, setting *v1alpha1.SubnetPortSetting, _ *model.VpcSubnet) {
				setting.Spec.SubnetName = "other-subnet"
			},
			wantError: "belongs to Subnet",
		},
		{
			name: "SubnetSet does not select a Subnet mirror",
			prepare: func(sp *v1alpha1.SubnetPort, _ *v1alpha1.SubnetPortSetting, _ *model.VpcSubnet) {
				sp.Spec.Subnet, sp.Spec.SubnetSet = "", "subnetset"
			},
			wantError: "belongs to Subnet",
		},
		{
			name: "wrong realized Subnet",
			prepare: func(_ *v1alpha1.SubnetPort, _ *v1alpha1.SubnetPortSetting, subnet *model.VpcSubnet) {
				subnet.Path = common.String("/orgs/default/projects/project/vpcs/vpc/subnets/other-subnet")
			},
			wantError: "no source identity for selected Subnet",
		},
		{
			name: "same Subnet ID in another VPC",
			prepare: func(_ *v1alpha1.SubnetPort, setting *v1alpha1.SubnetPortSetting, _ *model.VpcSubnet) {
				setting.Annotations[common.AnnotationAssociatedResource] = "project:other-vpc:source-subnet:named-setting"
			},
			wantError: "no source identity for selected Subnet",
		},
		{
			name: "missing source annotation",
			prepare: func(_ *v1alpha1.SubnetPort, setting *v1alpha1.SubnetPortSetting, _ *model.VpcSubnet) {
				setting.Annotations = nil
			},
			wantError: "no source identity for selected Subnet",
		},
		{
			name: "empty source ID",
			prepare: func(_ *v1alpha1.SubnetPort, setting *v1alpha1.SubnetPortSetting, _ *model.VpcSubnet) {
				setting.Annotations[common.AnnotationAssociatedResource] = "project:vpc:source-subnet:"
			},
			wantError: "no source identity for selected Subnet",
		},
		{
			name: "source ID escapes selected Subnet",
			prepare: func(_ *v1alpha1.SubnetPort, setting *v1alpha1.SubnetPortSetting, _ *model.VpcSubnet) {
				setting.Annotations[common.AnnotationAssociatedResource] = "project:vpc:source-subnet:../../other"
			},
			wantError: "invalid source port setting ID",
		},
		{
			name: "deleting mirror",
			prepare: func(_ *v1alpha1.SubnetPort, setting *v1alpha1.SubnetPortSetting, _ *model.VpcSubnet) {
				setting.Finalizers = []string{"test-finalizer"}
			},
			wantError: "is being deleted", deleteMirror: true,
		},
		{
			name: "missing realized Subnet path",
			prepare: func(_ *v1alpha1.SubnetPort, _ *v1alpha1.SubnetPortSetting, subnet *model.VpcSubnet) {
				subnet.Path = nil
			},
			wantError: "without a realized Subnet path",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setting := portSettingMirror("named-setting", "named-setting")
			service, sp, subnet := newPortSettingFixture(t)
			if tt.prepare != nil {
				tt.prepare(sp, setting, subnet)
			}
			require.NoError(t, service.Client.Create(context.Background(), setting))
			if tt.deleteMirror {
				require.NoError(t, service.Client.Delete(context.Background(), setting))
			}
			port, err := service.buildSubnetPort(sp, subnet, "", nil, true, false, v1alpha1.IPAddressTypeIPv4)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				require.Nil(t, port)
				if tt.isNotFound {
					require.True(t, apierrors.IsNotFound(err))
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantPath, port.PortSettingPath)
		})
	}
}

func TestBuildSubnetPortPortSettingLeavesPodUnchanged(t *testing.T) {
	service, _, subnet := newPortSettingFixture(t)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "pod", Namespace: "workload", UID: "bbbbbbbb-1234-1234-1234-123456789abc",
	}}
	port, err := service.buildSubnetPort(pod, subnet, "node", nil, false, false, v1alpha1.IPAddressTypeIPv4)
	require.NoError(t, err)
	require.Nil(t, port.PortSettingPath)
	require.Equal(t, common.String(string(pod.UID)), port.Attachment.AppId)
	require.Equal(t, common.String("node"), port.Attachment.ContextId)
}

type recordingPortSettingClient struct {
	fakePortClient
	ports []model.VpcSubnetPort
}

func (c *recordingPortSettingClient) Patch(_, _, _, _, _ string, port model.VpcSubnetPort) error {
	c.ports = append(c.ports, port)
	return nil
}

func (c *recordingPortSettingClient) Get(_, _, _, _, _ string) (model.VpcSubnetPort, error) {
	port := c.ports[len(c.ports)-1]
	// NSX reads back an omitted reference as the subnet's default setting.
	if port.PortSettingPath == nil {
		port.PortSettingPath = common.String(*port.ParentPath + "/port-settings/default")
	}
	return port, nil
}

func TestCreateOrUpdateSubnetPortDefaultPortSettingConverges(t *testing.T) {
	for _, kind := range []string{"SubnetPort", "Pod"} {
		t.Run(kind, func(t *testing.T) {
			service, sp, subnet := newPortSettingFixture(t)
			sp.Spec.PortSettingName = ""
			var obj interface{} = sp
			if kind == "Pod" {
				obj = &corev1.Pod{ObjectMeta: sp.ObjectMeta}
			}
			ports := &recordingPortSettingClient{}
			service.NSXClient.PortClient = ports
			patches := gomonkey.ApplyFunc((*SubnetPortService).CheckSubnetPortState,
				func(*SubnetPortService, interface{}, string) (*model.SegmentPortState, error) {
					return &model.SegmentPortState{}, nil
				})
			defer patches.Reset()
			for i := 0; i < 3; i++ {
				_, err := service.CreateOrUpdateSubnetPort(obj, subnet, "", nil, kind == "SubnetPort", false, v1alpha1.IPAddressTypeIPv4)
				require.NoError(t, err)
				require.Len(t, ports.ports, 1, "reconcile %d must not rewrite the default setting", i+1)
				require.Nil(t, ports.ports[0].PortSettingPath, "request must leave the setting unset")
				stored := service.SubnetPortStore.GetByKey(*ports.ports[0].Id)
				require.NotNil(t, stored)
				require.Equal(t, common.String(portSettingSubnetPath+"/port-settings/default"), stored.PortSettingPath)
			}
		})
	}
}

func TestCreateOrUpdateSubnetPortPortSetting(t *testing.T) {
	foreignSetting := portSettingMirror("foreign-setting", "Foreign_Setting")
	foreignSetting.Spec.SubnetName = "another-subnet"
	service, sp, subnet := newPortSettingFixture(t,
		portSettingMirror("named-setting", "Original_Setting"),
		portSettingMirror("replacement", "Replacement_Setting"),
		foreignSetting,
	)
	ports := &recordingPortSettingClient{}
	service.NSXClient.PortClient = ports
	patches := gomonkey.ApplyFunc((*SubnetPortService).CheckSubnetPortState,
		func(*SubnetPortService, interface{}, string) (*model.SegmentPortState, error) {
			return &model.SegmentPortState{}, nil
		})
	defer patches.Reset()
	for _, step := range []struct {
		name         string
		setting      string
		wantPath     *string
		wantRequests int
		wantError    bool
	}{
		{"create", "named-setting", common.String(portSettingSubnetPath + "/port-settings/Original_Setting"), 1, false},
		{"unchanged", "named-setting", common.String(portSettingSubnetPath + "/port-settings/Original_Setting"), 1, false},
		{"change", "replacement", common.String(portSettingSubnetPath + "/port-settings/Replacement_Setting"), 2, false},
		{"missing preserves existing setting", "missing", common.String(portSettingSubnetPath + "/port-settings/Replacement_Setting"), 2, true},
		{"wrong Subnet preserves existing setting", "foreign-setting", common.String(portSettingSubnetPath + "/port-settings/Replacement_Setting"), 2, true},
		{"clear", "", nil, 3, false},
		{"default unchanged", "", nil, 3, false},
		{"set on existing default port", "named-setting", common.String(portSettingSubnetPath + "/port-settings/Original_Setting"), 4, false},
	} {
		t.Run(step.name, func(t *testing.T) {
			sp.Spec.PortSettingName = step.setting
			_, err := service.CreateOrUpdateSubnetPort(sp, subnet, "", nil, true, false, v1alpha1.IPAddressTypeIPv4)
			if step.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Len(t, ports.ports, step.wantRequests)
			latest := ports.ports[len(ports.ports)-1]
			require.Equal(t, step.wantPath, latest.PortSettingPath)
			// Only the requested setting changes across updates.
			first := ports.ports[0]
			latest.PortSettingPath = first.PortSettingPath
			require.Equal(t, first, latest)
		})
	}
}
