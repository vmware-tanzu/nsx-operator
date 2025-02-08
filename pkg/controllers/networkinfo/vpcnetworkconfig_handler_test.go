package networkinfo

import (
	"context"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	types "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/ipblocksinfo"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

func createVPCNetworkConfigurationHandler(objs []client.Object) *VPCNetworkConfigurationHandler {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(objs...).Build()

	vpcService := &vpc.VPCService{
		Service: types.Service{
			Client: fakeClient,
			NSXClient: &nsx.Client{
				VPCConnectivityProfilesClient: &fakeVPCConnectivityProfilesClient{},
				VpcAttachmentClient:           fakeAttachmentClient,
			},

			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint:   "vmc-enforcementpoint",
					UseAVILoadBalancer: false,
				},
			},
		},
	}

	ipBlocksInfoService := &ipblocksinfo.IPBlocksInfoService{
		Service: types.Service{
			Client: fakeClient,
			NSXClient: &nsx.Client{
				VPCConnectivityProfilesClient: &fakeVPCConnectivityProfilesClient{},
				VpcAttachmentClient:           fakeAttachmentClient,
			},

			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint:   "vmc-enforcementpoint",
					UseAVILoadBalancer: false,
				},
			},
		},
		SyncTask: nil,
	}

	return &VPCNetworkConfigurationHandler{
		Client:              fakeClient,
		vpcService:          vpcService,
		ipBlocksInfoService: ipBlocksInfoService,
	}
}

func TestVPCNetworkConfigurationHandler_Create(t *testing.T) {
	testCases := []struct {
		name             string
		vpcNetworkConfig *v1alpha1.VPCNetworkConfiguration
	}{
		{
			name: "Create with invalid NSX project path",
			vpcNetworkConfig: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{NSXProject: ""},
			},
		},
		{
			name: "Create with valid NSX project path",
			vpcNetworkConfig: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{NSXProject: "/orgs/default/projects/nsx_operator_e2e_test"},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			queue := workqueue.NewTypedRateLimitingQueue(
				workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
			handler := createVPCNetworkConfigurationHandler(nil)
			handler.Create(context.TODO(), event.CreateEvent{Object: tc.vpcNetworkConfig}, queue)
		})
	}
}

func TestVPCNetworkConfigurationHandler_Delete(t *testing.T) {
	testCases := []struct {
		name             string
		vpcNetworkConfig *v1alpha1.VPCNetworkConfiguration
	}{
		{
			name: "Delete VPCNetworkConfiguration",
			vpcNetworkConfig: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{NSXProject: "/orgs/default/projects/nsx_operator_e2e_test"},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			queue := workqueue.NewTypedRateLimitingQueue(
				workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
			handler := createVPCNetworkConfigurationHandler(nil)
			handler.Delete(context.TODO(), event.DeleteEvent{Object: tc.vpcNetworkConfig}, queue)
		})
	}
}

func TestVPCNetworkConfigurationHandler_Update(t *testing.T) {
	testCases := []struct {
		name                string
		vpcNetworkConfigOld *v1alpha1.VPCNetworkConfiguration
		vpcNetworkConfigNew *v1alpha1.VPCNetworkConfiguration
		existingCR          []client.Object
	}{
		{
			name: "Update VPCNetworkConfiguration with same Spec",
			vpcNetworkConfigOld: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{NSXProject: "/orgs/default/projects/nsx_operator_e2e_test"},
			},
			vpcNetworkConfigNew: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{NSXProject: "/orgs/default/projects/nsx_operator_e2e_test"},
			},
		},
		{
			name: "Update VPCNetworkConfiguration with diff Spec, and the new NSXProject is invalid",
			vpcNetworkConfigOld: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{NSXProject: "/orgs/default/projects/nsx_operator_e2e_test"},
			},
			vpcNetworkConfigNew: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{NSXProject: ""},
			},
		},
		{
			name: "Update VPCNetworkConfiguration with diff Spec",
			vpcNetworkConfigOld: &v1alpha1.VPCNetworkConfiguration{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "testVPCNetworkConfig"},
				Spec:       v1alpha1.VPCNetworkConfigurationSpec{NSXProject: "/orgs/default/projects/nsx_operator_e2e_test"},
				Status:     v1alpha1.VPCNetworkConfigurationStatus{},
			},
			vpcNetworkConfigNew: &v1alpha1.VPCNetworkConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "testVPCNetworkConfig"},
				Spec:       v1alpha1.VPCNetworkConfigurationSpec{NSXProject: "/orgs/default/projects/nsx_operator_e2e_test", PrivateIPs: []string{"1.1.1.1"}},
			},
			existingCR: []client.Object{
				&v1alpha1.NetworkInfo{
					TypeMeta:   metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{Name: "testNetworkInfo", Namespace: "testNamespace"},
					VPCs:       nil,
				},
				&v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "testNamespace",
						Annotations: map[string]string{
							types.AnnotationVPCNetworkConfig: "testVPCNetworkConfig",
						},
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			queue := workqueue.NewTypedRateLimitingQueue(
				workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
			var objs []client.Object
			if tc.existingCR != nil {
				objs = append(objs, tc.existingCR...)
			}
			handler := createVPCNetworkConfigurationHandler(objs)

			handler.Update(context.TODO(), event.UpdateEvent{ObjectOld: tc.vpcNetworkConfigOld, ObjectNew: tc.vpcNetworkConfigNew}, queue)
		})
	}
}

func TestVPCNetworkConfigurationHandler_Generic(t *testing.T) {
	testCases := []struct {
		name             string
		vpcNetworkConfig *v1alpha1.VPCNetworkConfiguration
	}{
		{
			name: "Delete VPCNetworkConfiguration",
			vpcNetworkConfig: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{NSXProject: "/orgs/default/projects/nsx_operator_e2e_test"},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			queue := workqueue.NewTypedRateLimitingQueue(
				workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
			handler := createVPCNetworkConfigurationHandler(nil)
			handler.Generic(context.TODO(), event.GenericEvent{Object: tc.vpcNetworkConfig}, queue)
		})
	}
}
