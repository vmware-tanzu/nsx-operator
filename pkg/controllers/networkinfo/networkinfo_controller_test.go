/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkinfo

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stderrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/ipblocksinfo"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/test/unittests"
)

type fakeVPCConnectivityProfilesClient struct{}

func (c *fakeVPCConnectivityProfilesClient) Get(orgIdParam string, projectIdParam string, vpcConnectivityProfileIdParam string) (model.VpcConnectivityProfile, error) {
	return model.VpcConnectivityProfile{}, nil
}

func (c *fakeVPCConnectivityProfilesClient) Delete(orgIdParam string, projectIdParam string, vpcConnectivityProfileIdParam string) error {
	return nil
}

func (c *fakeVPCConnectivityProfilesClient) List(orgIdParam string, projectIdParam string, cursorParam *string, includeConflictsParam *bool, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.VpcConnectivityProfileListResult, error) {
	return model.VpcConnectivityProfileListResult{}, nil
}

func (c *fakeVPCConnectivityProfilesClient) Patch(orgIdParam string, projectIdParam string, vpcConnectivityProfileIdParam string, vpcConnectivityProfileParam model.VpcConnectivityProfile) error {
	return nil
}

func (c *fakeVPCConnectivityProfilesClient) Update(orgIdParam string, projectIdParam string, vpcConnectivityProfileIdParam string, vpcConnectivityProfileParam model.VpcConnectivityProfile) (model.VpcConnectivityProfile, error) {
	return model.VpcConnectivityProfile{}, nil
}

type fakeVpcAttachmentClient struct{}

func (c *fakeVpcAttachmentClient) List(orgIdParam string, projectIdParam string, vpcIdParam string, cursorParam *string, includeConflictsParam *bool, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.VpcAttachmentListResult, error) {
	return model.VpcAttachmentListResult{}, nil
}

func (c *fakeVpcAttachmentClient) Get(orgIdParam string, projectIdParam string, vpcIdParam string, vpcAttachmentIdParam string) (model.VpcAttachment, error) {
	return model.VpcAttachment{}, nil
}

func (c *fakeVpcAttachmentClient) Patch(orgIdParam string, projectIdParam string, vpcIdParam string, vpcAttachmentIdParam string, vpcAttachmentParam model.VpcAttachment) error {
	return nil
}

func (c *fakeVpcAttachmentClient) Update(orgIdParam string, projectIdParam string, vpcIdParam string, vpcAttachmentIdParam string, vpcAttachmentParam model.VpcAttachment) (model.VpcAttachment, error) {
	return model.VpcAttachment{}, nil
}

func (c *fakeVpcAttachmentClient) Delete(orgIdParam string, projectIdParam string, vpcIdParam string, vpcAttachmentIdParam string) error {
	return nil
}

var fakeAttachmentClient = &fakeVpcAttachmentClient{}

type fakeQueryClient struct{}

func (*fakeQueryClient) List(_ string, _ *string, _ *string, _ *int64, _ *bool, _ *string) (model.SearchResponse, error) {
	rc := int64(0)
	return model.SearchResponse{ResultCount: &rc}, nil
}

type fakeRecorder struct{}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
}

func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}

func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}

func createNetworkInfoReconciler(objs []client.Object) *NetworkInfoReconciler {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithStatusSubresource(&v1alpha1.VPCNetworkConfiguration{}).WithObjects(objs...).Build()

	vpcStore := &vpc.VPCStore{ResourceStore: servicecommon.ResourceStore{
		BindingType: model.VpcBindingType(),
	}}

	lbsStore := &vpc.LBSStore{ResourceStore: servicecommon.ResourceStore{
		Indexer: cache.NewIndexer(func(obj interface{}) (string, error) {
			lbs, ok := obj.(*model.LBService)
			if !ok || lbs.Id == nil {
				return "", fmt.Errorf("unexpected LBSStore object type %T", obj)
			}
			return *lbs.Id, nil
		}, cache.Indexers{}),
		BindingType: model.LBServiceBindingType(),
	}}

	service := &vpc.VPCService{
		VpcStore: vpcStore,
		LbsStore: lbsStore,
		Service: servicecommon.Service{
			Client: fakeClient,
			NSXClient: &nsx.Client{
				VPCConnectivityProfilesClient: &fakeVPCConnectivityProfilesClient{},
				VpcAttachmentClient:           fakeAttachmentClient,
				QueryClient:                   &fakeQueryClient{},
				NsxConfig: &config.NSXOperatorConfig{
					CoeConfig: &config.CoeConfig{Cluster: "unit-test"},
				},
			},

			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{Cluster: "unit-test"},
				NsxConfig: &config.NsxConfig{
					EnforcementPoint:   "vmc-enforcementpoint",
					UseAVILoadBalancer: false,
				},
				K8sConfig: &config.K8sConfig{
					IPFamily: "ipv4",
				},
			},
		},
	}

	dnsSvc := &mockDNSZoneSyncer{}

	r := &NetworkInfoReconciler{
		Client:           fakeClient,
		Scheme:           fake.NewClientBuilder().Build().Scheme(),
		Service:          service,
		DNSRecordService: dnsSvc,
		Recorder:         &fakeRecorder{},
	}
	r.StatusUpdater = common.NewStatusUpdater(r.Client, r.Service.NSXConfig, r.Recorder, MetricResType, "VPC", "NetworkInfo")
	return r
}

func TestReconcileVLANBackedAVIWithoutEdgeCluster(t *testing.T) {
	for _, tc := range []struct {
		name            string
		system          bool
		provider        vpc.LBProvider
		stack           v1alpha1.NetworkStackType
		distributed     bool
		gateway         bool
		preCreated      bool
		cachedCondition v1alpha1.ConditionType
		create          bool
	}{
		{"system AVI VLAN backed with distributed connection", true, vpc.AVILB, v1alpha1.VLANBackedVPC, true, false, false, "", true},
		{"namespace AVI VLAN backed with distributed connection", false, vpc.AVILB, v1alpha1.VLANBackedVPC, true, false, false, "", true},
		{"pre-created AVI VLAN backed with distributed connection", false, vpc.AVILB, v1alpha1.VLANBackedVPC, true, false, true, "", true},
		{"pre-created AVI VLAN backed without distributed connection", false, vpc.AVILB, v1alpha1.VLANBackedVPC, false, false, true, "", false},
		{"system AVI VLAN backed without distributed connection", true, vpc.AVILB, v1alpha1.VLANBackedVPC, false, false, false, "", true},
		{"system AVI VLAN backed with cached gateway but no distributed connection", true, vpc.AVILB, v1alpha1.VLANBackedVPC, false, false, false, v1alpha1.GatewayConnectionReady, true},
		{"namespace AVI VLAN backed without distributed connection", false, vpc.AVILB, v1alpha1.VLANBackedVPC, false, false, false, "", false},
		{"namespace AVI VLAN backed with cached gateway but no distributed connection", false, vpc.AVILB, v1alpha1.VLANBackedVPC, false, true, false, v1alpha1.GatewayConnectionReady, true},
		{"namespace AVI VLAN backed with cached gateway and distributed connection", false, vpc.AVILB, v1alpha1.VLANBackedVPC, true, false, false, v1alpha1.GatewayConnectionReady, true},
		{"namespace AVI VLAN backed with cached service cluster", false, vpc.AVILB, v1alpha1.VLANBackedVPC, false, false, false, v1alpha1.ServiceClusterReady, true},
		{"namespace AVI VLAN backed with gateway only", false, vpc.AVILB, v1alpha1.VLANBackedVPC, false, true, false, "", false},
		{"namespace NSX LB VLAN backed", false, vpc.NSXLB, v1alpha1.VLANBackedVPC, false, false, false, "", false},
		{"namespace AVI full stack", false, vpc.AVILB, v1alpha1.FullStackVPC, false, false, false, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			r := createNetworkInfoReconciler([]client.Object{&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns1"}}})
			v1alpha1.AddToScheme(r.Scheme)
			req := controllerruntime.Request{NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "name"}}
			require.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Namespace: req.Namespace, Name: req.Name}}))
			systemNC := &v1alpha1.VPCNetworkConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: servicecommon.SystemVPCNetworkConfigurationName},
				Spec: v1alpha1.VPCNetworkConfigurationSpec{
					NSXProject:             "/orgs/default/projects/project-quality",
					VPCConnectivityProfile: "/orgs/default/projects/project-quality/vpc-connectivity-profiles/system",
				},
			}
			if tc.cachedCondition != "" {
				systemNC.Status.Conditions = []v1alpha1.Condition{{Type: tc.cachedCondition, Status: corev1.ConditionTrue}}
			}
			require.NoError(t, r.Client.Create(ctx, systemNC))

			ncName := "non-system"
			if tc.system {
				ncName = servicecommon.SystemVPCNetworkConfigurationName
			}
			patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, _ context.Context, _ string) (string, error) {
				return ncName, nil
			})
			defer patches.Reset()
			patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
				profilePath := "/orgs/default/projects/project-quality/vpc-connectivity-profiles/default"
				vpcPath := ""
				if tc.preCreated {
					profilePath = ""
					vpcPath = "/orgs/default/projects/project-quality/vpcs/pre-created"
				}
				return &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: ncName},
					Spec: v1alpha1.VPCNetworkConfigurationSpec{
						NSXProject:             "/orgs/default/projects/project-quality",
						VPCConnectivityProfile: profilePath,
						VPC:                    vpcPath,
					},
				}, true, nil
			})
			patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
				return tc.provider, nil
			})
			patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
				return tc.stack, nil
			})
			validated := false
			patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateConnectionStatus", func(_ *vpc.VPCService, connectionNC *v1alpha1.VPCNetworkConfiguration, profilePath string, allow bool) (*servicecommon.VPCConnectionStatus, error) {
				validated = true
				if tc.provider == vpc.AVILB && tc.stack == v1alpha1.VLANBackedVPC {
					require.True(t, allow)
					require.Equal(t, servicecommon.SystemVPCNetworkConfigurationName, connectionNC.Name)
					require.Equal(t, "/orgs/default/projects/project-quality/vpc-connectivity-profiles/system", profilePath)
				}
				return &servicecommon.VPCConnectionStatus{DistributedConnectionReady: tc.distributed, GatewayConnectionReady: tc.gateway}, nil
			})
			patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVpcConnectivityProfile", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration, _ string) (*model.VpcConnectivityProfile, error) {
				return &model.VpcConnectivityProfile{ServiceGateway: &model.VpcServiceGatewayConfig{}}, nil
			})
			lbCapabilitySet := false
			patches.ApplyFunc(setVPCNetworkConfigurationStatusWithLBCapability, func(_ context.Context, _ client.Client, _ *v1alpha1.VPCNetworkConfiguration, capable bool) {
				lbCapabilitySet = capable
			})
			created := false
			createReached := errors.New("VPC creation reached")
			patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, _ context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
				created = true
				return nil, createReached
			})

			result, err := r.Reconcile(ctx, req)
			require.Equal(t, tc.provider == vpc.AVILB && tc.stack == v1alpha1.VLANBackedVPC && tc.cachedCondition == "", validated)
			require.Equal(t, tc.create, created)
			if tc.create {
				require.ErrorIs(t, err, createReached)
				if tc.system {
					require.True(t, lbCapabilitySet)
				}
			} else {
				require.NoError(t, err)
				require.Equal(t, common.ResultRequeueAfter60sec, result)
			}
		})
	}
}

func TestNetworkInfoReconciler_Reconcile(t *testing.T) {
	// These cases exercise VPC reconciliation with successful status persistence.
	// Persistence failures and recovery are covered by TestSystemAVIStatusWriteRetriesUntilRecovery.
	statusPatches := gomonkey.ApplyFunc(setVPCNetworkConfigurationStatusWithLBS,
		func(context.Context, client.Client, string, string, string, string, string) error { return nil })
	defer statusPatches.Reset()
	type args struct {
		req controllerruntime.Request
	}
	requestArgs := args{
		req: controllerruntime.Request{NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "name"}},
	}
	tests := []struct {
		name              string
		prepareFunc       func(*testing.T, *NetworkInfoReconciler, context.Context) *gomonkey.Patches
		verifyFunc        func(*testing.T, *NetworkInfoReconciler)
		customStatusWrite bool
		args              args
		want              controllerruntime.Result
		wantErr           bool
	}{
		{
			name: "Empty",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				patches = gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "deleteVPCsByNamespace", func(_ *NetworkInfoReconciler, _ context.Context, _ string) error {
					return nil
				})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultNormal,
			wantErr: false,
		},
		{
			name: "GatewayConnectionReadyInSystemVPC",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "system",
					},
				}))
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, _ context.Context, _ string) (string, error) {
					return servicecommon.SystemVPCNetworkConfigurationName, nil
				})
				patches.ApplyMethod(reflect.TypeOf(&vpc.VPCService{}), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						ObjectMeta: metav1.ObjectMeta{Name: servicecommon.SystemVPCNetworkConfigurationName},
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
							NSXProject:             "/orgs/default/projects/project-quality",
						},
					}, true, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateConnectionStatus", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration, _ string, _ bool) (*servicecommon.VPCConnectionStatus, error) {
					return &servicecommon.VPCConnectionStatus{
						GatewayConnectionReady: true,
						ServiceClusterReady:    false,
						ServiceClusterReason:   servicecommon.ReasonServiceClusterNotSet,
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (bool, error) {
					return false, nil
				})
				patches.ApplyMethodSeq(reflect.TypeOf(r.Service.Service.NSXClient.VPCConnectivityProfilesClient), "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.VpcConnectivityProfile{
						ExternalIpBlocks: []string{"fake-ip-block"},
						ServiceGateway: &model.VpcServiceGatewayConfig{
							EdgeClusterPaths: []string{"fake-edge-cluster-path"},
						},
					}, nil},
					Times: 2,
				}})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
					return vpc.NSXLB, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNSXLBSNATIP", func(_ *vpc.VPCService, _ model.Vpc, _ string) ([]string, error) {
					return []string{"100.64.0.3"}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultNSXLBSPathByVPC", func(_ *vpc.VPCService, _ string) string {
					return "lbs-path"
				})
				patches.ApplyFunc(r.StatusUpdater.UpdateSuccess,
					func(_ context.Context, _ client.Object, _ common.UpdateSuccessStatusFn, _ ...interface{}) {
					})
				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCAutoSNATDisabled.getNSNetworkCondition()))
					})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultRequeueAfter60sec,
			wantErr: false,
		},
		{
			name: "ServiceClusterReadyInSystemVPC",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "system",
					},
				}))
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, _ context.Context, _ string) (string, error) {
					return servicecommon.SystemVPCNetworkConfigurationName, nil
				})
				patches.ApplyMethod(reflect.TypeOf(&vpc.VPCService{}), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						ObjectMeta: metav1.ObjectMeta{Name: servicecommon.SystemVPCNetworkConfigurationName},
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
							NSXProject:             "/orgs/default/projects/project-quality",
						},
					}, true, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateConnectionStatus", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration, _ string, _ bool) (*servicecommon.VPCConnectionStatus, error) {
					return &servicecommon.VPCConnectionStatus{
						GatewayConnectionReady:  false,
						GatewayConnectionReason: servicecommon.ReasonGatewayConnectionNotSet,
						ServiceClusterReady:     true,
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (bool, error) {
					return false, nil
				})
				patches.ApplyMethodSeq(reflect.TypeOf(r.Service.Service.NSXClient.VPCConnectivityProfilesClient), "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.VpcConnectivityProfile{
						ExternalIpBlocks: []string{"fake-ip-block"},
						ServiceGateway: &model.VpcServiceGatewayConfig{
							EdgeClusterPaths: []string{"fake-edge-cluster-path"},
						},
					}, nil},
					Times: 2,
				}})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
					return vpc.NSXLB, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNSXLBSNATIP", func(_ *vpc.VPCService, _ model.Vpc, _ string) ([]string, error) {
					return []string{"100.64.0.3"}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultNSXLBSPathByVPC", func(_ *vpc.VPCService, _ string) string {
					return "lbs-path"
				})
				patches.ApplyFunc(r.StatusUpdater.UpdateSuccess,
					func(_ context.Context, _ client.Object, _ common.UpdateSuccessStatusFn, _ ...interface{}) {
					})
				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCAutoSNATDisabled.getNSNetworkCondition()))
					})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultRequeueAfter60sec,
			wantErr: false,
		},
		{
			name: "GatewayConnectionReadyInNonSystemVPC",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: "system"},
					Status: v1alpha1.VPCNetworkConfigurationStatus{
						Conditions: []v1alpha1.Condition{{
							Type:   v1alpha1.GatewayConnectionReady,
							Status: corev1.ConditionTrue,
						}},
					},
				}))
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, _ context.Context, _ string) (string, error) {
					return "non-system", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						ObjectMeta: metav1.ObjectMeta{Name: "non-system"},
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
							NSXProject:             "/orgs/default/projects/project-quality",
						},
					}, true, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateConnectionStatus", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration, _ string, _ bool) (*servicecommon.VPCConnectionStatus, error) {
					assert.FailNow(t, "should not be called")
					return &servicecommon.VPCConnectionStatus{
						GatewayConnectionReady: true,
						ServiceClusterReady:    false,
						ServiceClusterReason:   servicecommon.ReasonServiceClusterNotSet,
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (bool, error) {
					return false, nil
				})
				patches.ApplyMethodSeq(reflect.TypeOf(r.Service.Service.NSXClient.VPCConnectivityProfilesClient), "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.VpcConnectivityProfile{ExternalIpBlocks: []string{"fake-ip-block"}}, nil},
					Times:  1,
				}})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
					return vpc.NSXLB, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNSXLBSNATIP", func(_ *vpc.VPCService, _ model.Vpc, _ string) ([]string, error) {
					return []string{"100.64.0.3"}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultNSXLBSPathByVPC", func(_ *vpc.VPCService, _ string) string {
					return "lbs-path"
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
				})
				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCIsReady.getNSNetworkCondition()))
					})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultNormal,
			wantErr: false,
		},
		{
			name: "GatewayConnectionNotReadyInNonSystemVPC",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "system",
					},
				}))
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (string, error) {
					return "non-system", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVpcConnectivityProfilePathByVpcPath", func(_ *vpc.VPCService, _ string) (string, error) {
					return "connectivity_profile", nil
				})

				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						ObjectMeta: metav1.ObjectMeta{Name: "non-system"},
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
							NSXProject:             "/orgs/default/projects/project-quality",
						},
					}, true, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateConnectionStatus", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration, _ string, _ bool) (*servicecommon.VPCConnectionStatus, error) {
					assert.FailNow(t, "should not be called")
					return &servicecommon.VPCConnectionStatus{
						GatewayConnectionReady: true,
						ServiceClusterReady:    false,
						ServiceClusterReason:   servicecommon.ReasonServiceClusterNotSet,
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
					assert.FailNow(t, "should not be called")
					return &model.Vpc{}, nil
				})
				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCConnectionNotReady.getNSNetworkCondition()))
					})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultRequeueAfter60sec,
			wantErr: false,
		},
		{
			name: "AutoSnatEnabledInSystemVPC",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "system",
					},
				}))
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (string, error) {
					return servicecommon.SystemVPCNetworkConfigurationName, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						ObjectMeta: metav1.ObjectMeta{Name: servicecommon.SystemVPCNetworkConfigurationName},
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
							NSXProject:             "/orgs/default/projects/project-quality",
						},
					}, true, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateConnectionStatus", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration, _ string, _ bool) (*servicecommon.VPCConnectionStatus, error) {
					return &servicecommon.VPCConnectionStatus{
						GatewayConnectionReady: true,
						ServiceClusterReady:    false,
						ServiceClusterReason:   servicecommon.ReasonServiceClusterNotSet,
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
					return vpc.NSXLB, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNSXLBSNATIP", func(_ *vpc.VPCService, _ model.Vpc, _ string) ([]string, error) {
					return []string{"100.64.0.3"}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (bool, error) {
					return false, nil
				})
				patches.ApplyMethodSeq(reflect.TypeOf(r.Service.Service.NSXClient.VPCConnectivityProfilesClient), "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.VpcConnectivityProfile{
						ExternalIpBlocks: []string{"fake-ip-block"},
						ServiceGateway: &model.VpcServiceGatewayConfig{
							Enable:           servicecommon.Bool(true),
							EdgeClusterPaths: []string{"fake-edge-cluster-path"},
							NatConfig: &model.VpcNatConfig{
								EnableDefaultSnat: servicecommon.Bool(true),
							},
						},
					}, nil},
					Times: 2,
				}})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultNSXLBSPathByVPC", func(_ *vpc.VPCService, _ string) string {
					return "lbs-path"
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
					return vpc.NSXLB, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNSXLBSNATIP", func(_ *vpc.VPCService, _ model.Vpc, _ string) ([]string, error) {
					return []string{"100.64.0.3"}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultSNATIP", func(_ *vpc.VPCService, _ model.Vpc) (string, error) {
					return "snat-ip", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
				})
				patches.ApplyFunc(setVPCNetworkConfigurationStatusWithSnatEnabled,
					func(_ context.Context, _ client.Client, _ *v1alpha1.VPCNetworkConfiguration, autoSnatEnabled bool) {
						if !autoSnatEnabled {
							assert.FailNow(t, "should set VPCNetworkConfiguration status with AutoSnatEnabled=true")
						}
					})
				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCIsReady.getNSNetworkCondition()))
					})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultNormal,
			wantErr: false,
		},
		{
			name: "AutoSnatNotEnabledInSystemVPCForTepMode",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "system",
					},
				}))
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (string, error) {
					return servicecommon.SystemVPCNetworkConfigurationName, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						ObjectMeta: metav1.ObjectMeta{Name: servicecommon.SystemVPCNetworkConfigurationName},
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
							NSXProject:             "/orgs/default/projects/project-quality",
						},
					}, true, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateConnectionStatus", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration, _ string, _ bool) (*servicecommon.VPCConnectionStatus, error) {
					return &servicecommon.VPCConnectionStatus{
						GatewayConnectionReady: true,
						ServiceClusterReady:    false,
						ServiceClusterReason:   servicecommon.ReasonServiceClusterNotSet,
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
					return vpc.NSXLB, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNSXLBSNATIP", func(_ *vpc.VPCService, _ model.Vpc, _ string) ([]string, error) {
					return []string{"100.64.0.3"}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (bool, error) {
					return false, nil
				})
				patches.ApplyMethodSeq(reflect.TypeOf(r.Service.Service.NSXClient.VPCConnectivityProfilesClient), "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.VpcConnectivityProfile{
						ExternalIpBlocks: []string{"fake-ip-block"},
						ServiceGateway: &model.VpcServiceGatewayConfig{
							EdgeClusterPaths: []string{"fake-edge-cluster-path"},
						},
					}, nil},
					Times: 2,
				}})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultNSXLBSPathByVPC", func(_ *vpc.VPCService, _ string) string {
					return "lbs-path"
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultSNATIP", func(_ *vpc.VPCService, _ model.Vpc) (string, error) {
					assert.FailNow(t, "should not be called")
					return "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
				})
				patches.ApplyFunc(setVPCNetworkConfigurationStatusWithSnatEnabled,
					func(_ context.Context, _ client.Client, _ *v1alpha1.VPCNetworkConfiguration, autoSnatEnabled bool) {
						if autoSnatEnabled {
							assert.FailNow(t, "should set VPCNetworkConfiguration status with AutoSnatEnabled=false")
						}
					})
				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCAutoSNATDisabled.getNSNetworkCondition()))
					})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultRequeueAfter60sec,
			wantErr: false,
		},
		{
			name: "AutoSnatNotEnabledInSystemVPCForTepLessMode",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "system",
					},
				}))
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (string, error) {
					return servicecommon.SystemVPCNetworkConfigurationName, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						ObjectMeta: metav1.ObjectMeta{Name: servicecommon.SystemVPCNetworkConfigurationName},
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
							NSXProject:             "/orgs/default/projects/project-quality",
						},
					}, true, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateConnectionStatus", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration, _ string, _ bool) (*servicecommon.VPCConnectionStatus, error) {
					return &servicecommon.VPCConnectionStatus{
						GatewayConnectionReady: true,
						ServiceClusterReady:    false,
						ServiceClusterReason:   servicecommon.ReasonServiceClusterNotSet,
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
					return vpc.NSXLB, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNSXLBSNATIP", func(_ *vpc.VPCService, _ model.Vpc, _ string) ([]string, error) {
					return []string{"100.64.0.3"}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (bool, error) {
					return false, nil
				})
				patches.ApplyMethodSeq(reflect.TypeOf(r.Service.Service.NSXClient.VPCConnectivityProfilesClient), "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.VpcConnectivityProfile{
						ExternalIpBlocks: []string{"fake-ip-block"},
						ServiceGateway: &model.VpcServiceGatewayConfig{
							EdgeClusterPaths: []string{"fake-edge-cluster-path"},
						},
					}, nil},
					Times: 2,
				}})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultNSXLBSPathByVPC", func(_ *vpc.VPCService, _ string) string {
					return "lbs-path"
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultSNATIP", func(_ *vpc.VPCService, _ model.Vpc) (string, error) {
					assert.FailNow(t, "should not be called")
					return "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.VLANBackedVPC, nil
				})
				patches.ApplyFunc(setVPCNetworkConfigurationStatusWithSnatEnabled,
					func(_ context.Context, _ client.Client, _ *v1alpha1.VPCNetworkConfiguration, autoSnatEnabled bool, networkStack v1alpha1.NetworkStackType) {
						if autoSnatEnabled {
							assert.FailNow(t, "should set VPCNetworkConfiguration status with AutoSnatEnabled=false")
						}
						require.Equal(t, v1alpha1.VLANBackedVPC, networkStack)
					})
				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCIsReady.getNSNetworkCondition()))
					})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultNormal,
			wantErr: false,
		},
		{
			name: "AutoSnatEnabledInNonSystemVPC",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: "system"},
					Status: v1alpha1.VPCNetworkConfigurationStatus{
						Conditions: []v1alpha1.Condition{{
							Type:   v1alpha1.GatewayConnectionReady,
							Status: corev1.ConditionTrue,
						}},
					},
				}))
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (string, error) {
					return "non-system", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						ObjectMeta: metav1.ObjectMeta{Name: "non-system"},
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
							NSXProject:             "/orgs/default/projects/project-quality",
						},
					}, true, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateConnectionStatus", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration, _ string, _ bool) (*servicecommon.VPCConnectionStatus, error) {
					return &servicecommon.VPCConnectionStatus{
						GatewayConnectionReady: true,
						ServiceClusterReady:    false,
						ServiceClusterReason:   servicecommon.ReasonServiceClusterNotSet,
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
					return vpc.NSXLB, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNSXLBSNATIP", func(_ *vpc.VPCService, _ model.Vpc, _ string) ([]string, error) {
					return []string{"100.64.0.3"}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (bool, error) {
					return false, nil
				})
				patches.ApplyMethodSeq(reflect.TypeOf(r.Service.Service.NSXClient.VPCConnectivityProfilesClient), "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.VpcConnectivityProfile{
						ExternalIpBlocks: []string{"fake-ip-block"},
						ServiceGateway: &model.VpcServiceGatewayConfig{
							Enable: servicecommon.Bool(true),
							NatConfig: &model.VpcNatConfig{
								EnableDefaultSnat: servicecommon.Bool(true),
							},
						},
					}, nil},
					Times: 1,
				}})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultNSXLBSPathByVPC", func(_ *vpc.VPCService, _ string) string {
					return "lbs-path"
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNSXLBSNATIP", func(_ *vpc.VPCService, _ model.Vpc, _ string) ([]string, error) {
					return []string{"100.64.0.3"}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultSNATIP", func(_ *vpc.VPCService, _ model.Vpc) (string, error) {
					return "snat-ip", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
				})
				patches.ApplyFunc(setVPCNetworkConfigurationStatusWithSnatEnabled,
					func(_ context.Context, _ client.Client, _ *v1alpha1.VPCNetworkConfiguration, autoSnatEnabled bool) {
						assert.FailNow(t, "should not be called")
					})
				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCIsReady.getNSNetworkCondition()))
					})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultNormal,
			wantErr: false,
		},
		{
			name: "Pre-create VPC success case with AVILB",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: "system"},
					Status: v1alpha1.VPCNetworkConfigurationStatus{
						Conditions: []v1alpha1.Condition{{
							Type:   v1alpha1.GatewayConnectionReady,
							Status: corev1.ConditionTrue,
						}},
					},
				}))
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (string, error) {
					return "pre-vpc-nc", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVpcConnectivityProfilePathByVpcPath", func(_ *vpc.VPCService, _ string) (string, error) {
					return "connectivity_profile", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							NSXProject: "/orgs/default/projects/project-quality",
							VPC:        "/orgs/default/projects/nsx_operator_e2e_test/vpcs/pre-vpc",
						},
					}, true, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
					return vpc.AVILB, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})

				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBSsFromNSXByVPC", func(_ *vpc.VPCService, _ string) (string, error) {
					return "/orgs/default/projects/project-quality/vpcs/fake-vpc/vpc-lbs/lbs", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
				})
				patches.ApplyMethodSeq(reflect.TypeOf(r.Service.Service.NSXClient.VPCConnectivityProfilesClient), "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.VpcConnectivityProfile{
						ServiceGateway: nil,
					}, nil},
					Times: 1,
				}})
				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCIsReady.getNSNetworkCondition()))
					})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultNormal,
			wantErr: false,
		},
		{
			name: "Pre-create VPC success case with NSXLB",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: "system"},
					Status: v1alpha1.VPCNetworkConfigurationStatus{
						Conditions: []v1alpha1.Condition{{
							Type:   v1alpha1.GatewayConnectionReady,
							Status: corev1.ConditionTrue,
						}},
					},
				}))
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (string, error) {
					return "pre-vpc-nc", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVpcConnectivityProfilePathByVpcPath", func(_ *vpc.VPCService, _ string) (string, error) {
					return "connectivity_profile", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							NSXProject: "/orgs/default/projects/project-quality",
							VPC:        "/orgs/default/projects/nsx_operator_e2e_test/vpcs/pre-vpc",
						},
					}, true, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
					return vpc.NSXLB, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})

				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBSsFromNSXByVPC", func(_ *vpc.VPCService, _ string) (string, error) {
					return "/orgs/default/projects/project-quality/vpcs/fake-vpc/vpc-lbs/lbs", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateConnectionStatus", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration, _ string, _ bool) (*servicecommon.VPCConnectionStatus, error) {
					return &servicecommon.VPCConnectionStatus{
						GatewayConnectionReady: true,
						ServiceClusterReady:    false,
						ServiceClusterReason:   servicecommon.ReasonServiceClusterNotSet,
					}, nil
				})
				patches.ApplyMethodSeq(reflect.TypeOf(r.Service.Service.NSXClient.VPCConnectivityProfilesClient), "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.VpcConnectivityProfile{
						ServiceGateway: nil,
					}, nil},
					Times: 2,
				}})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNSXLBSNATIP", func(_ *vpc.VPCService, _ model.Vpc, interfaceID string) ([]string, error) {
					assert.Equal(t, "gateway-interface", interfaceID)
					return []string{"100.64.0.3"}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
				})
				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCIsReady.getNSNetworkCondition()))
					})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultNormal,
			wantErr: false,
		},
		{
			name:              "Pre-create VPC without attachment",
			customStatusWrite: true,
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "system",
					},
				}))
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (string, error) {
					return "pre-vpc-nc", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVpcConnectivityProfilePathByVpcPath", func(_ *vpc.VPCService, _ string) (string, error) {
					return "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							NSXProject: "/orgs/default/projects/project-quality",
							VPC:        "/orgs/default/projects/nsx_operator_e2e_test/vpcs/pre-vpc",
						},
					}, true, nil
				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ *v1alpha1.VPCNetworkConfiguration) (bool, string) {
					return true, ""
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
					return vpc.AVILB, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})

				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCIsReady.getNSNetworkCondition()))
					})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
				})
				patches.ApplyFunc(setVPCNetworkConfigurationStatusWithLBS,
					func(ctx context.Context, client client.Client, ncName string, vpcName string, aviSubnetPath string, nsxLBSPath string, vpcPath string) error {
						require.Equal(t, "", nsxLBSPath)
						require.Equal(t, "vpc-name", vpcName)
						return nil
					})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultNormal,
			wantErr: false,
		},
		{
			name:              "Pre-create VPC without attachment with AVILB enabled",
			customStatusWrite: true,
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "system",
					},
				}))
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (string, error) {
					return "pre-vpc-nc", nil
				})
				// Return empty connectivity profile path to simulate VPC without attachment
				patches.ApplyMethod(reflect.TypeOf(r), "GetVpcConnectivityProfilePathByVpcPath", func(_ *NetworkInfoReconciler, _ string) (string, error) {
					return "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							NSXProject: "/orgs/default/projects/project-quality",
							VPC:        "/orgs/default/projects/nsx_operator_e2e_test/vpcs/pre-vpc",
						},
					}, true, nil
				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ *v1alpha1.VPCNetworkConfiguration) (bool, string) {
					return true, ""
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
					return vpc.AVILB, nil
				})
				// Return VPC with LoadBalancerVpcEndpoint.Enabled = true to trigger AVI LB path retrieval
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
						LoadBalancerVpcEndpoint: &model.LoadBalancerVPCEndpoint{
							Enabled: servicecommon.Bool(true),
						},
					}, nil
				})
				// GetAVISubnetInfo should be called even without connectivity profile
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetAVISubnetInfo", func(_ *vpc.VPCService, _ model.Vpc) (string, []string, error) {
					return "/orgs/default/projects/project-quality/vpcs/fake-vpc/subnets/avi-subnet", []string{"100.64.0.0/24"}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
				})
				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCIsReady.getNSNetworkCondition()))
					})
				patches.ApplyFunc(setVPCNetworkConfigurationStatusWithLBS,
					func(ctx context.Context, client client.Client, ncName string, vpcName string, aviSubnetPath string, nsxLBSPath string, vpcPath string) error {
						// aviSubnetPath should be set even without connectivity profile
						require.Equal(t, "/orgs/default/projects/project-quality/vpcs/fake-vpc/subnets/avi-subnet", aviSubnetPath)
						require.Equal(t, "", nsxLBSPath)
						require.Equal(t, "vpc-name", vpcName)
						return nil
					})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultNormal,
			wantErr: false,
		},
		{
			name:              "Pre-create VPC without attachment with NSXLB",
			customStatusWrite: true,
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "system",
					},
				}))
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (string, error) {
					return "pre-vpc-nc", nil
				})
				// Return empty connectivity profile path to simulate VPC without attachment
				patches.ApplyMethod(reflect.TypeOf(r), "GetVpcConnectivityProfilePathByVpcPath", func(_ *NetworkInfoReconciler, _ string) (string, error) {
					return "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							NSXProject: "/orgs/default/projects/project-quality",
							VPC:        "/orgs/default/projects/nsx_operator_e2e_test/vpcs/pre-vpc",
						},
					}, true, nil
				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ *v1alpha1.VPCNetworkConfiguration) (bool, string) {
					return true, ""
				})
				// Use NSXLB provider to verify GetLBSsFromNSXByVPC is NOT called when connectivity profile is empty
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
					return vpc.NSXLB, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})
				// This should NOT be called when vpcConnectivityProfilePath is empty
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBSsFromNSXByVPC", func(_ *vpc.VPCService, _ string) (string, error) {
					assert.FailNow(t, "GetLBSsFromNSXByVPC should not be called when vpcConnectivityProfilePath is empty")
					return "", nil
				})
				// This should NOT be called when vpcConnectivityProfilePath is empty
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNSXLBSNATIP", func(_ *vpc.VPCService, _ model.Vpc, _ string) ([]string, error) {
					assert.FailNow(t, "GetNSXLBSNATIP should not be called when vpcConnectivityProfilePath is empty")
					return nil, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
				})
				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCIsReady.getNSNetworkCondition()))
					})
				patches.ApplyFunc(setVPCNetworkConfigurationStatusWithLBS,
					func(ctx context.Context, client client.Client, ncName string, vpcName string, aviSubnetPath string, nsxLBSPath string, vpcPath string) error {
						// nsxLBSPath should be empty since GetLBSsFromNSXByVPC was not called
						require.Equal(t, "", nsxLBSPath)
						require.Equal(t, "vpc-name", vpcName)
						return nil
					})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultNormal,
			wantErr: false,
		},
		{
			name: "NSX LB SNAT IP failure",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "system",
					},
				}))
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (string, error) {
					return servicecommon.SystemVPCNetworkConfigurationName, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						ObjectMeta: metav1.ObjectMeta{Name: servicecommon.SystemVPCNetworkConfigurationName},
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
							NSXProject:             "/orgs/default/projects/project-quality",
						},
					}, true, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateConnectionStatus", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration, _ string, _ bool) (*servicecommon.VPCConnectionStatus, error) {
					return &servicecommon.VPCConnectionStatus{
						GatewayConnectionReady: true,
						ServiceClusterReady:    false,
						ServiceClusterReason:   servicecommon.ReasonServiceClusterNotSet,
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
					return vpc.NSXLB, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (bool, error) {
					return false, nil
				})
				patches.ApplyMethodSeq(reflect.TypeOf(r.Service.Service.NSXClient.VPCConnectivityProfilesClient), "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.VpcConnectivityProfile{
						ExternalIpBlocks: []string{"fake-ip-block"},
						ServiceGateway: &model.VpcServiceGatewayConfig{
							Enable:           servicecommon.Bool(true),
							EdgeClusterPaths: []string{"fake-edge-cluster-path"},
						},
					}, nil},
					Times: 2,
				}})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultNSXLBSPathByVPC", func(_ *vpc.VPCService, _ string) string {
					return "lbs-path"
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultSNATIP", func(_ *vpc.VPCService, _ model.Vpc) (string, error) {
					assert.FailNow(t, "should not be called")
					return "", nil
				})
				patches.ApplyFunc(setVPCNetworkConfigurationStatusWithSnatEnabled,
					func(_ context.Context, _ client.Client, _ *v1alpha1.VPCNetworkConfiguration, autoSnatEnabled bool) {
						if autoSnatEnabled {
							assert.FailNow(t, "should set VPCNetworkConfiguration status with AutoSnatEnabled=false")
						}
					})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNSXLBSNATIP", func(_ *vpc.VPCService, _ model.Vpc, _ string) ([]string, error) {
					return nil, fmt.Errorf("tier1 uplink port IP not found")
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
				})
				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCNSXLBSNATIPError.getNSNetworkCondition(fmt.Errorf("tier1 uplink port IP not found"))))
					})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultRequeueAfter10sec,
			wantErr: true,
		},
		{
			name:              "DNS zones in VpcNetworkConfiguration",
			customStatusWrite: true,
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "system",
					},
				}))
				dnsZonePath := "/orgs/default/projects/project-quality/dns-services/default-dns-service/zones/project-zone1"
				invalidDNSZone := "/orgs/default/projects/project-quality/dns-services/default-dns-service/zones/inv-zone"
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: "pre-vpc-nc"},
					Spec:       v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{dnsZonePath, invalidDNSZone}},
				}))
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (string, error) {
					return "pre-vpc-nc", nil
				})
				// Return empty connectivity profile path to simulate VPC without attachment
				patches.ApplyMethod(reflect.TypeOf(r), "GetVpcConnectivityProfilePathByVpcPath", func(_ *NetworkInfoReconciler, _ string) (string, error) {
					return "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						ObjectMeta: metav1.ObjectMeta{Name: "pre-vpc-nc"},
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							NSXProject: "/orgs/default/projects/project-quality",
							VPC:        "/orgs/default/projects/nsx_operator_e2e_test/vpcs/pre-vpc",
							DNSZones: []string{
								dnsZonePath,
								invalidDNSZone,
							},
						},
					}, true, nil
				})
				r.DNSRecordService = &mockDNSZoneSyncer{
					dnsZoneConfigurations: map[string]string{
						dnsZonePath:    "example.com",
						invalidDNSZone: "",
					},
				}

				patches.ApplyFunc(getGatewayConnectionStatus, func(_ *v1alpha1.VPCNetworkConfiguration) (bool, string) {
					return true, ""
				})
				// Use NSXLB provider to verify GetLBSsFromNSXByVPC is NOT called when connectivity profile is empty
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
					return vpc.NSXLB, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
				})
				patches.ApplyFunc(setVPCNetworkConfigurationStatusWithLBS,
					func(ctx context.Context, client client.Client, ncName string, vpcName string, aviSubnetPath string, nsxLBSPath string, vpcPath string) error {
						return nil
					})
				patches.ApplyFunc(setNetworkInfoVPCStatus,
					func(client client.Client, ctx context.Context, obj client.Object, _ metav1.Time, args ...interface{}) {
						require.NotNil(t, args)
						require.True(t, len(args) >= 2)
						require.ElementsMatch(t, []string{"example.com"}, args[1])
					})
				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCIsReady.getNSNetworkCondition()))
					})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultNormal,
			wantErr: false,
			verifyFunc: func(t *testing.T, r *NetworkInfoReconciler) {
				got := &v1alpha1.VPCNetworkConfiguration{}
				require.NoError(t, r.Client.Get(context.Background(), types.NamespacedName{Name: "pre-vpc-nc"}, got))
				condition := getExistingConditionOfType(v1alpha1.DNSZonesReady, got.Status.Conditions)
				require.NotNil(t, condition)
				require.Equal(t, corev1.ConditionFalse, condition.Status)
				require.Equal(t, "DNSZoneNotFound", condition.Reason)
				require.Contains(t, condition.Message, "/zones/inv-zone")
			},
		},
		{
			name: "Failed to sync DNS zones in VpcNetworkConfiguration",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "system",
					},
				}))
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (string, error) {
					return "pre-vpc-nc", nil
				})
				// Return empty connectivity profile path to simulate VPC without attachment
				patches.ApplyMethod(reflect.TypeOf(r), "GetVpcConnectivityProfilePathByVpcPath", func(_ *NetworkInfoReconciler, _ string) (string, error) {
					return "", nil
				})
				invalidDNSZone := "/orgs/default/projects/project-quality/dns-services/default-dns-service/zones/inv-zone"
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							NSXProject: "/orgs/default/projects/project-quality",
							VPC:        "/orgs/default/projects/nsx_operator_e2e_test/vpcs/pre-vpc",
							DNSZones: []string{
								invalidDNSZone,
							},
						},
					}, true, nil
				})
				r.DNSRecordService = &mockDNSZoneSyncer{
					dnsZoneConfigurations: map[string]string{
						invalidDNSZone: "",
					},
					syncErr: fmt.Errorf("unable to query NSX DNS zone"),
				}

				patches.ApplyFunc(getGatewayConnectionStatus, func(_ *v1alpha1.VPCNetworkConfiguration) (bool, string) {
					return true, ""
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
					return vpc.NSXLB, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
					return v1alpha1.FullStackVPC, nil
				})
				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCDNSZonesSyncError.getNSNetworkCondition(fmt.Errorf("unable to query NSX DNS zone"))))
					})
				patches.ApplyFunc(setNetworkInfoVPCStatus,
					func(client client.Client, ctx context.Context, obj client.Object, _ metav1.Time, args ...interface{}) {
						require.NotNil(t, args)
						// Guard against architectural variances in gomonkey's variadic argument unpacking.
						// In failure cases, 'args' only contains the state (len=1), so we bypass the domain slice check.
						if len(args) >= 2 {
							_, ok := args[1].([]string)
							require.False(t, ok)
						} else {
							require.Equal(t, 1, len(args), "In failure case, only 'state' should be passed")
						}
					})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultRequeueAfter10sec,
			wantErr: true,
		},
		{
			name: "LBCapabilitySetBeforeVPCCreationFailure",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: "system"},
					Status: v1alpha1.VPCNetworkConfigurationStatus{
						Conditions: []v1alpha1.Condition{
							{Type: v1alpha1.GatewayConnectionReady, Status: corev1.ConditionTrue},
							{Type: v1alpha1.ServiceClusterReady, Status: corev1.ConditionTrue},
						},
					},
				}))
				r.Service.NSXConfig.K8sConfig.IPFamily = "IPv6"
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, _ context.Context, _ string) (string, error) {
					return servicecommon.SystemVPCNetworkConfigurationName, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						ObjectMeta: metav1.ObjectMeta{Name: servicecommon.SystemVPCNetworkConfigurationName},
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
							NSXProject:             "/orgs/default/projects/project-quality",
						},
					}, true, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
					return vpc.NSXLB, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVpcConnectivityProfile", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration, _ string) (*model.VpcConnectivityProfile, error) {
					return &model.VpcConnectivityProfile{
						ServiceGateway: &model.VpcServiceGatewayConfig{
							EdgeClusterPaths: []string{
								"/infra/sites/default/enforcement-points/default/virtual-network-appliance-clusters/vnac-1",
							},
						},
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, _ context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
					return nil, fmt.Errorf("VPC LBS creation failed: VNA mode does not support IPv6 LB")
				})
				lbCapabilitySet := false
				patches.ApplyFunc(setVPCNetworkConfigurationStatusWithLBCapability, func(_ context.Context, _ client.Client, _ *v1alpha1.VPCNetworkConfiguration, lbCapable bool) {
					assert.False(t, lbCapable, "LBCapability must be False for VNA+NSX-LB+IPv6")
					lbCapabilitySet = true
				})
				patches.ApplyFunc(setNSNetworkReadyCondition, func(_ context.Context, _ client.Client, _ string, _ *corev1.NamespaceCondition) {})
				patches.ApplyFunc((*common.StatusUpdater).UpdateFail, func(_ *common.StatusUpdater, _ context.Context, _ client.Object, _ error, _ string, _ common.UpdateFailStatusFn, _ ...interface{}) {
				})
				t.Cleanup(func() {
					assert.True(t, lbCapabilitySet, "setVPCNetworkConfigurationStatusWithLBCapability must be called before CreateOrUpdateVPC fails")
				})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultRequeueAfter10sec,
			wantErr: true,
		},
		{
			name: "VPCCreationFailureWithLBSEdgeCapacityError",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "system",
					},
				}))
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (string, error) {
					return servicecommon.SystemVPCNetworkConfigurationName, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						ObjectMeta: metav1.ObjectMeta{Name: servicecommon.SystemVPCNetworkConfigurationName},
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
							NSXProject:             "/orgs/default/projects/project-quality",
						},
					}, true, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
					return vpc.NSXLB, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateConnectionStatus", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration, _ string, _ bool) (*servicecommon.VPCConnectionStatus, error) {
					return &servicecommon.VPCConnectionStatus{
						GatewayConnectionReady: true,
						ServiceClusterReady:    true,
					}, nil
				})
				lbsErr := nsxutil.NewRealizeStateError("/orgs/default/projects/project-quality/vpcs/ns-1/vpc-lbs/default realized with errors: There is no available capacity on edge node", 502103)
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, _ context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
					return nil, lbsErr
				})
				conditionSet := false
				patches.ApplyFunc(setNSNetworkReadyCondition, func(_ context.Context, _ client.Client, _ string, condition *corev1.NamespaceCondition) {
					assert.Equal(t, corev1.ConditionFalse, condition.Status)
					assert.Equal(t, NSReasonVPCNotReady, condition.Reason)
					assert.Contains(t, condition.Message, "Load Balancer Service (size: SMALL) creation failed: Insufficient Edge Cluster capacity on NSX (error code: 502103)")
					conditionSet = true
				})
				patches.ApplyFunc((*common.StatusUpdater).UpdateFail, func(_ *common.StatusUpdater, _ context.Context, _ client.Object, _ error, msg string, _ common.UpdateFailStatusFn, _ ...interface{}) {
					assert.Contains(t, msg, "Load Balancer Service (size: SMALL) creation failed")
				})
				t.Cleanup(func() {
					assert.True(t, conditionSet, "setNSNetworkReadyCondition must be called with enhanced LBS capacity error")
				})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultRequeueAfter10sec,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns1 := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "ns1"},
			}
			r := createNetworkInfoReconciler([]client.Object{ns1})
			v1alpha1.AddToScheme(r.Scheme)
			ctx := context.TODO()
			patches := &gomonkey.Patches{}
			if tt.prepareFunc != nil {
				patches = tt.prepareFunc(t, r, ctx)
			}
			if !tt.customStatusWrite {
				patches.ApplyFunc(setVPCNetworkConfigurationStatusWithLBS, func(context.Context, client.Client, string, string, string, string, string) error {
					return nil
				})
			}
			defer patches.Reset()
			got, err := r.Reconcile(ctx, tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("Reconcile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Reconcile() got = %v, want %v", got, tt.want)
			}
			if tt.verifyFunc != nil {
				tt.verifyFunc(t, r)
			}
		})
	}
}
func TestNetworkInfoReconciler_Reconcile_ExternalIPBlockInSystemVPC(t *testing.T) {
	// These cases exercise VPC reconciliation with successful status persistence.
	// Persistence failures and recovery are covered by TestSystemAVIStatusWriteRetriesUntilRecovery.
	statusPatches := gomonkey.ApplyFunc(setVPCNetworkConfigurationStatusWithLBS,
		func(context.Context, client.Client, string, string, string, string, string) error { return nil })
	defer statusPatches.Reset()
	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "name"},
	}

	tests := []struct {
		nameSuffix string
		ipType     v1alpha1.IPAddressType
	}{
		{nameSuffix: "IPv4", ipType: v1alpha1.IPAddressTypeIPv4},
		{nameSuffix: "IPv6", ipType: v1alpha1.IPAddressTypeIPv6},
		{nameSuffix: "IPv4IPv6", ipType: v1alpha1.IPAddressTypeIPv4IPv6},
	}

	for _, tt := range tests {
		t.Run("VPCNetworkConfigurationStatusWithNoExternalIPBlockInSystemVPC_"+tt.nameSuffix, func(t *testing.T) {
			ns1 := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "ns1"},
			}
			r := createNetworkInfoReconciler([]client.Object{ns1})
			v1alpha1.AddToScheme(r.Scheme)
			ctx := context.TODO()

			assert.NoError(t, r.Client.Create(ctx, &v1alpha1.NetworkInfo{
				ObjectMeta: metav1.ObjectMeta{Namespace: req.Namespace, Name: req.Name},
			}))
			assert.NoError(t, r.Client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "system"},
			}))

			patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, _ context.Context, _ string) (string, error) {
				return servicecommon.SystemVPCNetworkConfigurationName, nil
			})
			defer patches.Reset()
			patches.ApplyFunc(setVPCNetworkConfigurationStatusWithLBS, func(context.Context, client.Client, string, string, string, string, string) error {
				return nil
			})

			patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
				return &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: servicecommon.SystemVPCNetworkConfigurationName},
					Spec: v1alpha1.VPCNetworkConfigurationSpec{
						VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
						NSXProject:             "/orgs/default/projects/project-quality",
					},
				}, true, nil
			})

			patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateConnectionStatus", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration, _ string, _ bool) (*servicecommon.VPCConnectionStatus, error) {
				return &servicecommon.VPCConnectionStatus{GatewayConnectionReady: true}, nil
			})

			patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) {
				return vpc.NSXLB, nil
			})

			// Dynamically mock the SNAT IP based on the IP format variation
			patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNSXLBSNATIP", func(_ *vpc.VPCService, _ model.Vpc, _ string) ([]string, error) {
				if tt.ipType == v1alpha1.IPAddressTypeIPv6 {
					return []string{"2001::1"}, nil
				}
				return []string{"100.64.0.3"}, nil
			})

			patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, _ context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider) (*model.Vpc, error) {
				return &model.Vpc{DisplayName: servicecommon.String("vpc-name"), Path: servicecommon.String("path"), Id: servicecommon.String("vpc-id")}, nil
			})

			patches.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, _ context.Context, _ string) (bool, error) {
				return false, nil
			})

			// Dynamically mock the cluster IP address layout structure configuration block
			patches.ApplyMethod(reflect.TypeOf(&config.K8sConfig{}), "GetIPAddressType", func(_ *config.K8sConfig) v1alpha1.IPAddressType {
				return tt.ipType
			})

			patches.ApplyMethodSeq(reflect.TypeOf(r.Service.Service.NSXClient.VPCConnectivityProfilesClient), "Get", []gomonkey.OutputCell{{
				Values: gomonkey.Params{model.VpcConnectivityProfile{
					ServiceGateway: &model.VpcServiceGatewayConfig{
						EdgeClusterPaths: []string{"fake-edge-cluster-path"},
					},
				}, nil},
				Times: 2,
			}})

			patches.ApplyFunc(setVPCNetworkConfigurationStatusWithNoExternalIPBlock, func(_ context.Context, _ client.Client, _ *v1alpha1.VPCNetworkConfiguration, _ bool) {
				t.Log("setVPCNetworkConfigurationStatusWithNoExternalIPBlock called")
			})

			patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultNSXLBSPathByVPC", func(_ *vpc.VPCService, _ string) string {
				return "lbs-path"
			})

			patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
				return v1alpha1.FullStackVPC, nil
			})

			patches.ApplyFunc(setVPCNetworkConfigurationStatusWithSnatEnabled, func(_ context.Context, _ client.Client, _ *v1alpha1.VPCNetworkConfiguration, autoSnatEnabled bool) {
				if autoSnatEnabled {
					assert.FailNow(t, "should set VPCNetworkConfiguration status with AutoSnatEnabled=false")
				}
			})

			patches.ApplyFunc(setNSNetworkReadyCondition, func(_ context.Context, _ client.Client, _ string, condition *corev1.NamespaceCondition) {
				require.True(t, nsConditionEquals(*condition, *nsMsgVPCNoExternalIPBlock.getNSNetworkCondition()))
			})

			got, err := r.Reconcile(ctx, req)
			if err != nil {
				t.Errorf("Reconcile() error = %v, wantErr false", err)
				return
			}
			if !reflect.DeepEqual(got, common.ResultRequeueAfter60sec) {
				t.Errorf("Reconcile() got = %v, want %v", got, common.ResultRequeueAfter60sec)
			}
		})
	}
}

func TestNetworkInfoReconciler_deleteStaleVPCs(t *testing.T) {
	testCases := []struct {
		name                            string
		prepareFuncs                    func(r *NetworkInfoReconciler) *gomonkey.Patches
		existingVPCNetworkConfiguration *v1alpha1.VPCNetworkConfiguration
		expectErrStr                    string
	}{
		{
			name: "no VPC exists for the Namespace,, skip deletion",
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCsByNamespace", func(_ *vpc.VPCService, _ string) []*model.Vpc {
					return []*model.Vpc{}
				})
				return patches
			},
		},
		{
			name: "NSX VPC is used by a valid Namespace,, skip deletion",
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCsByNamespace", func(_ *vpc.VPCService, _ string) []*model.Vpc {
					return []*model.Vpc{
						{Tags: []model.Tag{
							{Scope: servicecommon.String(servicecommon.TagScopeNamespaceUID), Tag: servicecommon.String("vpc1")},
						}},
					}
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "listNamespaceCRsNameIDSet", func(_ *NetworkInfoReconciler, _ context.Context) (sets.Set[string], sets.Set[string], error) {
					return sets.Set[string]{}, sets.New[string]("vpc1"), nil
				})
				return patches
			},
		},
		{
			name: "failed to delete VPC",
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCsByNamespace", func(_ *vpc.VPCService, _ string) []*model.Vpc {
					return []*model.Vpc{
						{
							Tags: []model.Tag{
								{Scope: servicecommon.String(servicecommon.TagScopeNamespaceUID), Tag: servicecommon.String("vpc1")},
							},
							Path: servicecommon.String("/orgs/default/projects/default/vpcs/vpc1"),
						},
					}
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "listNamespaceCRsNameIDSet", func(_ *NetworkInfoReconciler, _ context.Context) (sets.Set[string], sets.Set[string], error) {
					return sets.Set[string]{}, sets.Set[string]{}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "DeleteVPC", func(_ *vpc.VPCService, _ string) error {
					return fmt.Errorf("delete failed")
				})
				return patches
			},
			expectErrStr: "delete failed",
		},
		{
			name: "successful deletion of VPCs",
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCsByNamespace", func(_ *vpc.VPCService, _ string) []*model.Vpc {
					return []*model.Vpc{
						{
							Tags: []model.Tag{
								{Scope: servicecommon.String(servicecommon.TagScopeNamespaceUID), Tag: servicecommon.String("vpc1")},
							},
							Path: servicecommon.String("/orgs/default/projects/default/vpcs/vpc1"),
						},
					}
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "listNamespaceCRsNameIDSet", func(_ *NetworkInfoReconciler, _ context.Context) (sets.Set[string], sets.Set[string], error) {
					return sets.Set[string]{}, sets.Set[string]{}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "DeleteVPC", func(_ *vpc.VPCService, _ string) error {
					return nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return nil, nil
				})
				return patches
			},
		},
		{
			name: "successful deletion of VPCs And update delete stale VPCs in status",
			existingVPCNetworkConfiguration: &v1alpha1.VPCNetworkConfiguration{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "fakeNetworkconfigName"},
				Spec:       v1alpha1.VPCNetworkConfigurationSpec{},
				Status: v1alpha1.VPCNetworkConfigurationStatus{
					VPCs: []v1alpha1.VPCInfo{
						{
							Name: "fakeDisplayName2",
						},
						{
							Name: "fakeDisplayName1",
						},
					},
				},
			},
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCsByNamespace", func(_ *vpc.VPCService, _ string) []*model.Vpc {
					return []*model.Vpc{
						{
							Tags: []model.Tag{
								{Scope: servicecommon.String(servicecommon.TagScopeNamespaceUID), Tag: servicecommon.String("vpc1")},
							},
							Path:        servicecommon.String("/orgs/default/projects/default/vpcs/vpc1"),
							DisplayName: servicecommon.String("fakeDisplayName1"),
						},
					}
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "listNamespaceCRsNameIDSet", func(_ *NetworkInfoReconciler, _ context.Context) (sets.Set[string], sets.Set[string], error) {
					return sets.Set[string]{}, sets.Set[string]{}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "DeleteVPC", func(_ *vpc.VPCService, _ string) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "listVPCsByNetworkConfigName", func(_ *NetworkInfoReconciler, _ string) ([]*model.Vpc, error) {
					return []*model.Vpc{
						{
							Tags: []model.Tag{
								{Scope: servicecommon.String(servicecommon.TagScopeNamespaceUID), Tag: servicecommon.String("vpc1")},
							},
							Path:        servicecommon.String("/orgs/default/projects/default/vpcs/vpc2"),
							DisplayName: servicecommon.String("fakeDisplayName2"),
						},
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return &v1alpha1.VPCNetworkConfiguration{
						ObjectMeta: metav1.ObjectMeta{
							Name: "fakeNetworkconfigName",
						},
					}, nil
				})
				return patches
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var objs []client.Object
			if tc.existingVPCNetworkConfiguration != nil {
				objs = append(objs, tc.existingVPCNetworkConfiguration)
			}
			r := createNetworkInfoReconciler(objs)

			ctx := context.TODO()
			namespace := "test-ns"

			if tc.prepareFuncs != nil {
				patches := tc.prepareFuncs(r)
				defer patches.Reset()
			}

			err := r.deleteVPCsByNamespace(ctx, namespace)

			if tc.expectErrStr != "" {
				assert.ErrorContains(t, err, tc.expectErrStr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestNetworkInfoReconciler_DeleteNetworkInfo(t *testing.T) {
	testCases := []struct {
		name                string
		expectErrStr        string
		expectRes           reconcile.Result
		req                 reconcile.Request
		existingNamespace   *corev1.Namespace
		existingNetworkInfo *v1alpha1.NetworkInfo
		prepareFuncs        func(r *NetworkInfoReconciler) *gomonkey.Patches
	}{
		{
			name:              "Delete NetworkInfo and Namespace not existed",
			existingNamespace: nil,
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCsByNamespace", func(_ *vpc.VPCService, _ string) []*model.Vpc {
					return []*model.Vpc{}
				})
				return patches
			},
			expectErrStr: "",
			expectRes:    common.ResultNormal,
			req:          reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "testNamespace", Name: "testNetworkInfo"}},
		},
		{
			name: "Delete NetworkInfo with delete stale VPC error",
			existingNamespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "testNamespace", UID: "fakeNamespaceUID"},
			},
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCsByNamespace", func(_ *vpc.VPCService, _ string) []*model.Vpc {
					return []*model.Vpc{
						{
							Tags: []model.Tag{
								{Scope: servicecommon.String(servicecommon.TagScopeNamespaceUID), Tag: servicecommon.String("vpc1")},
							},
							Path: servicecommon.String("/orgs/default/projects/default/vpcs/vpc1"),
						},
					}
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "DeleteVPC", func(_ *vpc.VPCService, _ string) error {
					return fmt.Errorf("delete failed")
				})
				return patches
			},
			expectErrStr:        "multiple errors occurred while deleting VPCs",
			existingNetworkInfo: nil,
			expectRes:           common.ResultRequeue,
			req:                 reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "testNamespace", Name: "testNetworkInfo"}},
		},
		{
			name:              "Delete NetworkInfo with finalizer",
			existingNamespace: nil,
			expectErrStr:      "",
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name: "testNetworkInfo", Namespace: "testNamespace",
					DeletionTimestamp: &metav1.Time{Time: time.Now()}, Finalizers: []string{"test-Finalizers"},
				},
				VPCs: nil,
			},
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service.VpcStore), "GetByIndex", func(_ *vpc.VPCStore, key string, value string) []*model.Vpc {
					return nil
				})
				return patches
			},
			expectRes: common.ResultNormal,
			req:       reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "testNamespace", Name: "testNetworkInfo"}},
		},
		{
			name: "Delete NetworkInfo with finalizer error",
			existingNamespace: &corev1.Namespace{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "testNamespace", UID: "fakeNamespaceUID"},
				Spec:       corev1.NamespaceSpec{},
				Status:     corev1.NamespaceStatus{},
			},
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service.VpcStore), "GetByIndex", func(_ *vpc.VPCStore, key string, value string) []*model.Vpc {
					id := "fakeNamespaceUID"
					scope := servicecommon.TagScopeNamespaceUID
					tag1 := []model.Tag{
						{
							Scope: &scope,
							Tag:   &id,
						},
					}
					name := "name"
					return []*model.Vpc{
						{
							DisplayName: &name,
							Tags:        tag1,
						},
					}
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "deleteVPCs", func(_ *NetworkInfoReconciler, _ context.Context, _ []*model.Vpc, ns string) error {
					return fmt.Errorf("failed to get VPCNetworkConfiguration for Namespace when deleting stale VPCs")
				})
				return patches
			},
			expectErrStr: "failed to get VPCNetworkConfiguration for Namespace when deleting stale VPCs",
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name: "testNetworkInfo", Namespace: "testNamespace", UID: "fakeNamespaceUID",
					DeletionTimestamp: &metav1.Time{Time: time.Now()}, Finalizers: []string{"test-Finalizers"},
				},
				VPCs: nil,
			},
			expectRes: common.ResultRequeue,
			req:       reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "testNamespace", Name: "testNetworkInfo"}},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var objs []client.Object
			if testCase.existingNamespace != nil {
				objs = append(objs, testCase.existingNamespace)
			}
			if testCase.existingNetworkInfo != nil {
				objs = append(objs, testCase.existingNetworkInfo)
			}
			reconciler := createNetworkInfoReconciler(objs)
			ctx := context.Background()

			v1alpha1.AddToScheme(reconciler.Scheme)

			if testCase.prepareFuncs != nil {
				patches := testCase.prepareFuncs(reconciler)
				defer patches.Reset()
			}

			result, err := reconciler.Reconcile(ctx, testCase.req)
			if testCase.expectErrStr != "" {
				assert.ErrorContains(t, err, testCase.expectErrStr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, testCase.expectRes, result)
		})
	}
}

func TestNetworkInfoReconciler_Create(t *testing.T) {
	testCases := []struct {
		name                string
		expectErrStr        string
		expectRes           reconcile.Result
		req                 reconcile.Request
		existingNamespace   *corev1.Namespace
		existingNetworkInfo *v1alpha1.NetworkInfo
		prepareFuncs        func(r *NetworkInfoReconciler) *gomonkey.Patches
	}{
		{
			name: "Create NetworkInfo",
			existingNamespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "testNamespace", UID: "fakeNamespaceUID"},
			},
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCsByNamespace", func(_ *vpc.VPCService, _ string) []*model.Vpc {
					vpcName := "fakeVPCName"
					vpcPath := "fakeVPCPath"
					return []*model.Vpc{
						{
							DisplayName: &vpcName,
							Path:        &vpcPath,
						},
					}
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, ctx context.Context, _ string) (bool, error) {
					return false, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "DeleteVPC", func(_ *vpc.VPCService, _ string) error {
					return fmt.Errorf("delete failed")
				})
				return patches
			},
			expectErrStr: "failed to locate default NetworkConfig",
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				ObjectMeta: metav1.ObjectMeta{Name: "testNetworkInfo", Namespace: "testNamespace", UID: "fakeNamespaceUID"},
				VPCs:       nil,
			},
			expectRes: common.ResultRequeueAfter10sec,
			req:       reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "testNamespace", Name: "testNetworkInfo"}},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var objs []client.Object
			if testCase.existingNamespace != nil {
				objs = append(objs, testCase.existingNamespace)
			}
			if testCase.existingNetworkInfo != nil {
				objs = append(objs, testCase.existingNetworkInfo)
			}
			reconciler := createNetworkInfoReconciler(objs)
			ctx := context.Background()

			v1alpha1.AddToScheme(reconciler.Scheme)

			if testCase.prepareFuncs != nil {
				patches := testCase.prepareFuncs(reconciler)
				defer patches.Reset()
			}

			result, err := reconciler.Reconcile(ctx, testCase.req)
			if testCase.expectErrStr != "" {
				assert.ErrorContains(t, err, testCase.expectErrStr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, testCase.expectRes, result)
		})
	}
}

func TestNetworkInfoReconciler_CollectGarbage(t *testing.T) {
	r := createNetworkInfoReconciler(nil)

	ctx := context.TODO()

	t.Run("no VPCs found in the store", func(t *testing.T) {
		patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "ListVPC", func(_ *vpc.VPCService) []model.Vpc {
			return nil
		})
		defer patches.Reset()

		r.CollectGarbage(ctx)
		// No errors expected
	})

	t.Run("successful garbage collection", func(t *testing.T) {
		patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "ListVPC", func(_ *vpc.VPCService) []model.Vpc {
			vpcPath1 := "/vpc/1"
			vpcPath2 := "/vpc/2"
			return []model.Vpc{{Path: &vpcPath1}, {Path: &vpcPath2}}
		})
		patches.ApplyMethod(reflect.TypeOf(r.Service), "DeleteVPC", func(_ *vpc.VPCService, _ string) error {
			return nil
		})
		defer patches.Reset()

		r.CollectGarbage(ctx)
	})

	t.Run("failed to delete VPC", func(t *testing.T) {
		patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "ListVPC", func(_ *vpc.VPCService) []model.Vpc {
			vpcPath1 := "/vpc/1"
			vpcPath2 := "/vpc/2"
			return []model.Vpc{{Path: &vpcPath1}, {Path: &vpcPath2}}
		})
		patches.ApplyMethod(reflect.TypeOf(r.Service), "DeleteVPC", func(_ *vpc.VPCService, _ string) error {
			return errors.New("deletion error")
		})
		defer patches.Reset()
		r.CollectGarbage(ctx)
	})
}

func TestSyncPreCreatedVpcIPs(t *testing.T) {
	stopSig := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "stop",
		},
	}
	getQueuedReqs := func(queue workqueue.TypedRateLimitingInterface[reconcile.Request]) []reconcile.Request {
		var requests []reconcile.Request
		for {
			obj, shutdown := queue.Get()
			if shutdown {
				return requests
			}
			if obj.Namespace == "stop" {
				return requests
			}
			requests = append(requests, obj)
		}
	}

	r := createNetworkInfoReconciler(nil)
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	r.Client = k8sClient
	r.queue = workqueue.NewTypedRateLimitingQueueWithConfig(
		workqueue.DefaultTypedControllerRateLimiter[reconcile.Request](),
		workqueue.TypedRateLimitingQueueConfig[reconcile.Request]{
			Name: "test",
		})
	defer r.queue.ShuttingDown()

	v1alpha1.AddToScheme(r.Scheme)
	ctx := context.TODO()

	k8sClient.EXPECT().List(ctx, gomock.Any()).Return(nil).Do(
		func(_ context.Context, list client.ObjectList, opts ...*client.ListOption) error {
			networkInfos, _ := list.(*v1alpha1.NetworkInfoList)
			networkInfos.Items = []v1alpha1.NetworkInfo{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "net1", Namespace: "ns1"},
					VPCs: []v1alpha1.VPCState{
						{PrivateIPs: []string{"1.1.1.0/24"}},
					},
				}, {
					ObjectMeta: metav1.ObjectMeta{Name: "net1", Namespace: "ns2"},
					VPCs: []v1alpha1.VPCState{
						{PrivateIPs: []string{"1.1.1.0/24"}},
					},
				}, {
					ObjectMeta: metav1.ObjectMeta{Name: "net1", Namespace: "ns3"},
					VPCs: []v1alpha1.VPCState{
						{PrivateIPs: []string{"1.1.1.0/24", "1.1.2.0/24"}},
					},
				}, {
					ObjectMeta: metav1.ObjectMeta{Name: "net1", Namespace: "ns5"},
				}, {
					ObjectMeta: metav1.ObjectMeta{Name: "net1", Namespace: "ns6"},
					VPCs: []v1alpha1.VPCState{
						{PrivateIPs: []string{"1.1.1.0/24"}},
					},
				},
			}

			return nil
		})

	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetAllVPCsFromNSX", func(_ *vpc.VPCService) map[string]model.Vpc {
		return map[string]model.Vpc{
			"/orgs/default/projects/p1/vpcs/vpc1": {
				PrivateIps: []string{"1.1.1.0/24"},
			},
			"/orgs/default/projects/p1/vpcs/vpc2": {
				PrivateIps: []string{"1.1.1.0/24", "1.1.2.0/24"},
			},
			"/orgs/default/projects/p1/vpcs/vpc3": {
				PrivateIps: []string{"1.1.1.0/24", "1.1.3.0/24"},
			},
			"/orgs/default/projects/p1/vpcs/vpc5": {
				PrivateIps: []string{"1.1.1.0/24"},
			},
		}
	})
	patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNamespacesWithPreCreatedVPCs", func(_ *vpc.VPCService) (map[string]string, error) {
		return map[string]string{
			"ns1": "/orgs/default/projects/p1/vpcs/vpc1",
			"ns2": "/orgs/default/projects/p1/vpcs/vpc2",
			"ns3": "/orgs/default/projects/p1/vpcs/vpc3",
			"ns4": "/orgs/default/projects/p1/vpcs/vpc4",
			"ns5": "/orgs/default/projects/p1/vpcs/vpc5",
			"ns6": "/orgs/default/projects/p1/vpcs/vpc6",
		}, nil
	})
	defer patches.Reset()

	expRequests := []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: "net1", Namespace: "ns1"}},
		{NamespacedName: types.NamespacedName{Name: "net1", Namespace: "ns2"}},
		{NamespacedName: types.NamespacedName{Name: "net1", Namespace: "ns3"}},
		{NamespacedName: types.NamespacedName{Name: "net1", Namespace: "ns6"}},
	}

	r.syncPreCreatedVpcs(ctx)
	r.queue.Add(stopSig)
	requests := getQueuedReqs(r.queue)
	assert.ElementsMatch(t, expRequests, requests)
}

func TestListVPCsByNetworkConfigName(t *testing.T) {
	r := &NetworkInfoReconciler{
		Service: &vpc.VPCService{},
	}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNamespacesByNetworkconfigName", func(_ *vpc.VPCService, _c string) ([]string, error) {
		return []string{"ns1", "ns2"}, nil
	})
	patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCsByNamespace", func(_ *vpc.VPCService, ns string) []*model.Vpc {
		if ns == "ns1" {
			return []*model.Vpc{{Id: servicecommon.String("vpc1")}}
		}
		return []*model.Vpc{{Id: servicecommon.String("vpc2")}}
	})
	defer patches.Reset()
	expVPCs := []*model.Vpc{{Id: servicecommon.String("vpc1")}, {Id: servicecommon.String("vpc2")}}
	actVPCs, err := r.listVPCsByNetworkConfigName("nc1")
	assert.Nil(t, err)
	assert.ElementsMatch(t, expVPCs, actVPCs)
}

func TestNetworkInfoReconciler_StartController(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects().Build()
	vpcService := &vpc.VPCService{
		Service: servicecommon.Service{
			Client: fakeClient,
			NSXClient: &nsx.Client{
				QueryClient: &fakeQueryClient{},
				NsxConfig: &config.NSXOperatorConfig{
					CoeConfig: &config.CoeConfig{Cluster: "unit-test"},
				},
			},
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{Cluster: "unit-test"},
			},
		},
	}
	ipblocksInfoService := &ipblocksinfo.IPBlocksInfoService{
		Service: servicecommon.Service{
			Client: fakeClient,
		},
	}
	mockMgr := &MockManager{scheme: runtime.NewScheme()}
	patches := gomonkey.ApplyFunc((*NetworkInfoReconciler).setupWithManager, func(r *NetworkInfoReconciler, mgr manager.Manager) error {
		return nil
	})
	patches.ApplyFunc((*NetworkInfoReconciler).syncPreCreatedVpcs, func(r *NetworkInfoReconciler, ctx context.Context) {
	})
	patches.ApplyFunc(common.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context) error) {
	})
	defer patches.Reset()
	dnsSvc, err := dns.InitializeDNSRecordService(vpcService.Service, vpcService)
	require.NoError(t, err)
	r := NewNetworkInfoReconciler(mockMgr, vpcService, ipblocksInfoService, dnsSvc)
	err = r.StartController(mockMgr, nil)
	assert.Nil(t, err)
	// Sleep to make sure the patches are reset after the goroutine in StartController get executed
	time.Sleep(time.Second)
}

func TestNetworkInfoReconciler_RestoreReconcile(t *testing.T) {
	defaultReq := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "testNamespace",
			Name:      "testNetworkInfo",
		},
	}
	testCases := []struct {
		name                string
		expectErrStr        string
		expectReq           reconcile.Request
		existingNetworkInfo *v1alpha1.NetworkInfo
		prepareFuncs        func(r *NetworkInfoReconciler) *gomonkey.Patches
	}{
		{
			name:                "NSX version not support",
			expectErrStr:        "",
			expectReq:           defaultReq,
			existingNetworkInfo: nil,
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return false
				})
				return patches
			},
		},
		{
			name:                "Failed to list NetworkInfo CR",
			expectErrStr:        "failed to get NetworkInfo restore list: failed to list NetworkInfo",
			expectReq:           defaultReq,
			existingNetworkInfo: nil,
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return true
				})
				mockCtl := gomock.NewController(t)
				k8sClient := mock_client.NewMockClient(mockCtl)
				ctx := context.TODO()
				k8sClient.EXPECT().List(ctx, gomock.Any()).Return(fmt.Errorf("failed to list NetworkInfo"))
				r.Client = k8sClient
				return patches
			},
		},
		{
			name:         "Failed to validate pre-created VPC",
			expectErrStr: "failed to get NetworkInfo restore list: failed to check pre-created VPC",
			expectReq:    defaultReq,
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1",
					Name:      "networkinfo1",
				},
			},
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return true
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNamespacesWithPreCreatedVPCs", func(_ *vpc.VPCService) (map[string]string, error) {
					return nil, fmt.Errorf("failed to check pre-created VPC")
				})
				return patches
			},
		},
		{
			name:         "NetworkInfo doesn't contain VPCs",
			expectErrStr: "",
			expectReq:    defaultReq,
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1",
					Name:      "networkinfo1",
				},
			},
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return true
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetCurrentVPCsByNamespace", func(_ *vpc.VPCService, _ context.Context, _ string) []*model.Vpc {
					assert.FailNow(t, "should not be called")
					return nil
				})
				return patches
			},
		},
		{
			name:         "Skip pre-created VPC",
			expectErrStr: "",
			expectReq:    defaultReq,
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1",
					Name:      "networkinfo1",
				},
				VPCs: []v1alpha1.VPCState{
					{
						Name: "vpc1",
					},
				},
			},
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return true
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNamespacesWithPreCreatedVPCs", func(_ *vpc.VPCService) (map[string]string, error) {
					return map[string]string{
						"ns1": "vpc1",
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetCurrentVPCsByNamespace", func(_ *vpc.VPCService, _ context.Context, _ string) []*model.Vpc {
					assert.FailNow(t, "should not be called")
					return nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCFromNSXByPath", func(_ *vpc.VPCService, vpcPath string) (*model.Vpc, error) {
					return &model.Vpc{}, nil
				})
				return patches
			},
		},
		{
			name:         "Pre-created VPC not exists",
			expectErrStr: "",
			expectReq:    defaultReq,
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1",
					Name:      "networkinfo1",
				},
				VPCs: []v1alpha1.VPCState{
					{
						Name: "vpc1",
					},
				},
			},
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return true
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNamespacesWithPreCreatedVPCs", func(_ *vpc.VPCService) (map[string]string, error) {
					return map[string]string{
						"ns1": "vpc1",
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetCurrentVPCsByNamespace", func(_ *vpc.VPCService, _ context.Context, _ string) []*model.Vpc {
					assert.FailNow(t, "should not be called")
					return nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCFromNSXByPath", func(_ *vpc.VPCService, vpcPath string) (*model.Vpc, error) {
					return nil, nsxutil.NewNSXApiError(&model.ApiError{}, stderrors.ErrorType_NOT_FOUND)
				})
				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCCreateUpdateError.getNSNetworkCondition(fmt.Errorf("pre-created VPC is not found in NSX: vpc1"))))
					})
				return patches
			},
		},
		{
			name:         "Pre-created VPC get failure",
			expectErrStr: "mock GetVPCFromNSXByPath error",
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1",
					Name:      "networkinfo1",
				},
				VPCs: []v1alpha1.VPCState{
					{
						Name: "vpc1",
					},
				},
			},
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return true
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNamespacesWithPreCreatedVPCs", func(_ *vpc.VPCService) (map[string]string, error) {
					return map[string]string{
						"ns1": "vpc1",
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetCurrentVPCsByNamespace", func(_ *vpc.VPCService, _ context.Context, _ string) []*model.Vpc {
					assert.FailNow(t, "should not be called")
					return nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCFromNSXByPath", func(_ *vpc.VPCService, vpcPath string) (*model.Vpc, error) {
					return nil, errors.New("mock GetVPCFromNSXByPath error")
				})
				return patches
			},
		},
		{
			name:         "NSX VPC exists",
			expectErrStr: "",
			expectReq:    defaultReq,
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1",
					Name:      "networkinfo1",
				},
				VPCs: []v1alpha1.VPCState{
					{
						Name: "vpc1",
					},
				},
			},
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return true
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetCurrentVPCsByNamespace", func(_ *vpc.VPCService, _ context.Context, _ string) []*model.Vpc {
					return []*model.Vpc{
						{
							DisplayName: servicecommon.String("vpc1"),
						},
					}
				})
				patches.ApplyMethod(reflect.TypeOf(r), "Reconcile", func(_ *NetworkInfoReconciler, _ context.Context, req controllerruntime.Request) (controllerruntime.Result, error) {
					assert.FailNow(t, "should not be called")
					return common.ResultNormal, nil
				})
				return patches
			},
		},
		{
			name:         "Collect NetworkInfo",
			expectErrStr: "",
			expectReq:    defaultReq,
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1",
					Name:      "networkinfo1",
				},
				VPCs: []v1alpha1.VPCState{
					{
						Name: "vpc1",
					},
				},
			},
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return true
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetCurrentVPCsByNamespace", func(_ *vpc.VPCService, _ context.Context, _ string) []*model.Vpc {
					return []*model.Vpc{}
				})
				patches.ApplyMethod(reflect.TypeOf(r), "Reconcile", func(_ *NetworkInfoReconciler, _ context.Context, req controllerruntime.Request) (controllerruntime.Result, error) {
					expectReq := reconcile.Request{
						NamespacedName: types.NamespacedName{
							Namespace: "ns1",
							Name:      "networkinfo1",
						},
					}
					assert.Equal(t, req, expectReq)
					return common.ResultNormal, nil
				})
				return patches
			},
		},
		{
			name:         "Reconcile fail",
			expectErrStr: "failed to restore NetworkInfo ns1/networkinfo1, error: reconcile failed",
			expectReq:    defaultReq,
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1",
					Name:      "networkinfo1",
				},
				VPCs: []v1alpha1.VPCState{
					{
						Name: "vpc1",
					},
				},
			},
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return true
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetCurrentVPCsByNamespace", func(_ *vpc.VPCService, _ context.Context, _ string) []*model.Vpc {
					return []*model.Vpc{}
				})
				patches.ApplyMethod(reflect.TypeOf(r), "Reconcile", func(_ *NetworkInfoReconciler, _ context.Context, req controllerruntime.Request) (controllerruntime.Result, error) {
					expectReq := reconcile.Request{
						NamespacedName: types.NamespacedName{
							Namespace: "ns1",
							Name:      "networkinfo1",
						},
					}
					assert.Equal(t, req, expectReq)
					return common.ResultRequeue, fmt.Errorf("reconcile failed")
				})
				return patches
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var objs []client.Object
			if testCase.existingNetworkInfo != nil {
				objs = append(objs, testCase.existingNetworkInfo)
			}
			reconciler := createNetworkInfoReconciler(objs)
			v1alpha1.AddToScheme(reconciler.Scheme)
			if testCase.prepareFuncs != nil {
				patches := testCase.prepareFuncs(reconciler)
				if patches != nil {
					defer patches.Reset()
				}
			}
			err := reconciler.RestoreReconcile()
			if testCase.expectErrStr != "" {
				assert.ErrorContains(t, err, testCase.expectErrStr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNetworkInfoReconciler_ComputeSubnetSetIPAddressType(t *testing.T) {
	tests := []struct {
		name               string
		supervisorIPFamily v1alpha1.IPAddressType
		hasIPv4CIDR        bool
		hasIPv6CIDR        bool
		expected           v1alpha1.IPAddressType
	}{
		{
			name:               "Both supervisor and VPC support IPv4 and IPv6",
			supervisorIPFamily: v1alpha1.IPAddressTypeIPv4IPv6,
			hasIPv4CIDR:        true,
			hasIPv6CIDR:        true,
			expected:           v1alpha1.IPAddressTypeIPv4IPv6,
		},
		{
			name:               "Supervisor IPv4IPv6 with VPC IPv4 only",
			supervisorIPFamily: v1alpha1.IPAddressTypeIPv4IPv6,
			hasIPv4CIDR:        true,
			hasIPv6CIDR:        false,
			expected:           v1alpha1.IPAddressTypeIPv4,
		},
		{
			name:               "Supervisor IPv4IPv6 with VPC IPv6 only",
			supervisorIPFamily: v1alpha1.IPAddressTypeIPv4IPv6,
			hasIPv4CIDR:        false,
			hasIPv6CIDR:        true,
			expected:           v1alpha1.IPAddressTypeIPv6,
		},
		{
			name:               "Supervisor IPv4 with VPC IPv4 and IPv6",
			supervisorIPFamily: v1alpha1.IPAddressTypeIPv4,
			hasIPv4CIDR:        true,
			hasIPv6CIDR:        true,
			expected:           v1alpha1.IPAddressTypeIPv4,
		},
		{
			name:               "Supervisor IPv4 with VPC IPv6 only - no intersection",
			supervisorIPFamily: v1alpha1.IPAddressTypeIPv4,
			hasIPv4CIDR:        false,
			hasIPv6CIDR:        true,
			expected:           IPAddressTypeNone,
		},
		{
			name:               "Supervisor IPv6 with VPC IPv4 only - no intersection",
			supervisorIPFamily: v1alpha1.IPAddressTypeIPv6,
			hasIPv4CIDR:        true,
			hasIPv6CIDR:        false,
			expected:           IPAddressTypeNone,
		},
		{
			name:               "Supervisor IPv4 with VPC IPv4 only",
			supervisorIPFamily: v1alpha1.IPAddressTypeIPv4,
			hasIPv4CIDR:        true,
			hasIPv6CIDR:        false,
			expected:           v1alpha1.IPAddressTypeIPv4,
		},
		{
			name:               "Supervisor IPv6 with VPC IPv6 only",
			supervisorIPFamily: v1alpha1.IPAddressTypeIPv6,
			hasIPv4CIDR:        false,
			hasIPv6CIDR:        true,
			expected:           v1alpha1.IPAddressTypeIPv6,
		},
		{
			name:               "VPC has no CIDR blocks",
			supervisorIPFamily: v1alpha1.IPAddressTypeIPv4IPv6,
			hasIPv4CIDR:        false,
			hasIPv6CIDR:        false,
			expected:           IPAddressTypeNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &NetworkInfoReconciler{
				Service: &vpc.VPCService{
					Service: servicecommon.Service{
						NSXConfig: &config.NSXOperatorConfig{
							NsxConfig: &config.NsxConfig{
								EnforcementPoint: "test-ep",
							},
						},
					},
				},
			}

			// Mock the GetIPAddressType method
			patches := gomonkey.ApplyMethod(
				reflect.TypeOf(reconciler.Service.NSXConfig.K8sConfig),
				"GetIPAddressType",
				func(_ *config.K8sConfig) v1alpha1.IPAddressType {
					return tt.supervisorIPFamily
				},
			)
			defer patches.Reset()

			result := reconciler.computeSubnetSetIPAddressType(tt.hasIPv4CIDR, tt.hasIPv6CIDR)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNetworkInfoReconciler_UpdateDefaultSubnetSet(t *testing.T) {
	subnetSet := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-default",
			Namespace: "ns-1",
			Labels:    map[string]string{servicecommon.LabelDefaultNetwork: "pod"},
		},
		Spec: v1alpha1.SubnetSetSpec{
			IPAddressType:  v1alpha1.IPAddressTypeIPv4,
			AccessMode:     "Public",
			IPv4SubnetSize: 32,
		},
	}
	testCases := []struct {
		name                     string
		autoCreatedIPAddressType v1alpha1.IPAddressType
		hasPrecreatedSubnet      bool
		subnetSetList            []client.Object
		expectErrStr             string
		existingIPAddressType    v1alpha1.IPAddressType
		subnetNames              []string
		// Unified verification fields:
		expectNotExists        bool
		expectIPAddressType    v1alpha1.IPAddressType
		expectSubnetNames      *[]string
		expectIPv6PrefixLength int
	}{
		{
			name:                     "Create default SubnetSet",
			autoCreatedIPAddressType: v1alpha1.IPAddressTypeIPv4,
			hasPrecreatedSubnet:      false,
			expectIPAddressType:      v1alpha1.IPAddressTypeIPv4,
		},
		{
			name:                     "Delete default SubnetSet 1",
			autoCreatedIPAddressType: "",
			hasPrecreatedSubnet:      false,
			subnetSetList:            []client.Object{subnetSet},
			expectNotExists:          true,
		},
		{
			name:                     "Delete default SubnetSet 2",
			autoCreatedIPAddressType: "",
			hasPrecreatedSubnet:      true,
			subnetSetList:            []client.Object{subnetSet},
			expectNotExists:          true,
		},
		{
			name:                     "Existing default SubnetSet",
			autoCreatedIPAddressType: v1alpha1.IPAddressTypeIPv4,
			hasPrecreatedSubnet:      false,
			subnetSetList:            []client.Object{subnetSet},
			expectIPAddressType:      v1alpha1.IPAddressTypeIPv4,
		},
		{
			name:                     "Existing default SubnetSet with IPAddressType change",
			autoCreatedIPAddressType: v1alpha1.IPAddressTypeIPv6,
			hasPrecreatedSubnet:      false,
			subnetSetList:            []client.Object{subnetSet},
			expectIPAddressType:      v1alpha1.IPAddressTypeIPv6,
			expectIPv6PrefixLength:   80,
		},
		{
			name:                     "Create default SubnetSet with IPv6",
			autoCreatedIPAddressType: v1alpha1.IPAddressTypeIPv6,
			hasPrecreatedSubnet:      false,
			expectIPAddressType:      v1alpha1.IPAddressTypeIPv6,
			expectIPv6PrefixLength:   80,
		},
		{
			name:                     "Create default SubnetSet with dual stack",
			autoCreatedIPAddressType: v1alpha1.IPAddressTypeIPv4IPv6,
			hasPrecreatedSubnet:      false,
			expectIPAddressType:      v1alpha1.IPAddressTypeIPv4IPv6,
			expectIPv6PrefixLength:   80,
		},
		{
			name:                     "Existing default SubnetSet with subnetNames and IPAddressType change (no delete)",
			autoCreatedIPAddressType: v1alpha1.IPAddressTypeIPv4,
			existingIPAddressType:    v1alpha1.IPAddressTypeIPv4IPv6,
			subnetNames:              []string{"subnet-1"},
			hasPrecreatedSubnet:      false,
			subnetSetList:            []client.Object{subnetSet},
			expectIPAddressType:      v1alpha1.IPAddressTypeIPv4IPv6,
			expectSubnetNames:        &[]string{"subnet-1"},
		},
		{
			name:                     "Existing default SubnetSet without subnetNames and IPAddressType change (delete)",
			autoCreatedIPAddressType: v1alpha1.IPAddressTypeIPv4,
			existingIPAddressType:    v1alpha1.IPAddressTypeIPv4IPv6,
			hasPrecreatedSubnet:      false,
			subnetSetList:            []client.Object{subnetSet},
			expectIPAddressType:      v1alpha1.IPAddressTypeIPv4,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			subnetSet := &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-default",
					Namespace: "ns-1",
					Labels:    map[string]string{servicecommon.LabelDefaultNetwork: "pod"},
				},
				Spec: v1alpha1.SubnetSetSpec{
					IPAddressType:  v1alpha1.IPAddressTypeIPv4,
					AccessMode:     "Public",
					IPv4SubnetSize: 32,
				},
			}
			if tc.existingIPAddressType != "" {
				subnetSet.Spec.IPAddressType = tc.existingIPAddressType
			}
			if len(tc.subnetNames) > 0 {
				subnetSet.Spec.SubnetNames = &tc.subnetNames
			}

			var objs []client.Object
			if len(tc.subnetSetList) > 0 {
				objs = []client.Object{subnetSet}
			}

			r := createNetworkInfoReconciler(objs)
			ctx := context.TODO()

			nc := &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{
					DefaultSubnetSize:       32,
					DefaultIPv6PrefixLength: 80,
				},
			}
			if len(tc.subnetSetList) == 0 {
				err := r.updateDefaultSubnetSet(ctx, "pod", "ns-1", nc, tc.hasPrecreatedSubnet, tc.autoCreatedIPAddressType)
				if tc.expectErrStr != "" {
					assert.ErrorContains(t, err, tc.expectErrStr)
				} else {
					require.NoError(t, err)
				}
			} else {
				// Don't error out on "already exists" for existing tests
				err := r.updateDefaultSubnetSet(ctx, "pod", "ns-1", nc, tc.hasPrecreatedSubnet, tc.autoCreatedIPAddressType)
				if tc.expectErrStr != "" {
					assert.ErrorContains(t, err, tc.expectErrStr)
				} else {
					if err != nil && !apierrors.IsAlreadyExists(err) {
						require.NoError(t, err)
					}
				}
			}

			guard := unittests.NewGuard(t, unittests.Metadata{
				Feature:     "tepless",
				Component:   "networkinfo_controller",
				Scenario:    "default_subnetset_update",
				Description: fmt.Sprintf("Verify default SubnetSet update behavior under scenario: %s", tc.name),
			})

			updated := &v1alpha1.SubnetSet{}
			err := r.Client.Get(ctx, types.NamespacedName{Namespace: "ns-1", Name: "pod-default"}, updated)
			if tc.expectNotExists {
				assert.True(t, apierrors.IsNotFound(err))
				guard.AssertOutput(map[string]interface{}{"exists": false})
			} else {
				require.NoError(t, err)
				if tc.expectIPAddressType != "" {
					assert.Equal(t, tc.expectIPAddressType, updated.Spec.IPAddressType)
				}
				if tc.expectSubnetNames != nil {
					assert.Equal(t, tc.expectSubnetNames, updated.Spec.SubnetNames)
				} else {
					assert.Nil(t, updated.Spec.SubnetNames)
				}
				assert.Equal(t, tc.expectIPv6PrefixLength, updated.Spec.IPv6PrefixLength)
				guard.AssertOutput(updated)
			}
		})
	}
}

type MockManager struct {
	controllerruntime.Manager
	client client.Client
	scheme *runtime.Scheme
}

func (m *MockManager) GetClient() client.Client {
	return m.client
}

func (m *MockManager) GetScheme() *runtime.Scheme {
	return m.scheme
}

func (m *MockManager) GetEventRecorderFor(name string) record.EventRecorder {
	return nil
}

func (m *MockManager) Add(runnable manager.Runnable) error {
	return nil
}

func (m *MockManager) Start(context.Context) error {
	return nil
}

func TestPrimaryLBIP(t *testing.T) {
	tests := []struct {
		name string
		ips  []string
		want string
	}{
		{
			name: "nil slice returns empty string",
			ips:  nil,
			want: "",
		},
		{
			name: "empty slice returns empty string",
			ips:  []string{},
			want: "",
		},
		{
			name: "single IPv4 bare IP",
			ips:  []string{"100.64.0.1"},
			want: "100.64.0.1",
		},
		{
			name: "single IPv6 bare IP falls back to first",
			ips:  []string{"2001:db8::1"},
			want: "2001:db8::1",
		},
		{
			name: "dual-stack: IPv4 preferred (bare IPs)",
			ips:  []string{"100.64.0.1", "2001:db8::1"},
			want: "100.64.0.1",
		},
		{
			name: "dual-stack: IPv4 preferred even when IPv6 is first",
			ips:  []string{"2001:db8::1", "100.64.0.1"},
			want: "100.64.0.1",
		},
		{
			name: "single IPv4 CIDR (AVI format)",
			ips:  []string{"100.64.0.0/24"},
			want: "100.64.0.0/24",
		},
		{
			name: "single IPv6 CIDR falls back to first",
			ips:  []string{"2001:db8::/64"},
			want: "2001:db8::/64",
		},
		{
			name: "dual-stack CIDR: IPv4 preferred",
			ips:  []string{"100.64.0.0/24", "2001:db8::/64"},
			want: "100.64.0.0/24",
		},
		{
			name: "invalid IP before valid IPv6 is skipped in fallback",
			ips:  []string{"not-an-ip", "2001:db8::1"},
			want: "2001:db8::1",
		},
		{
			name: "all invalid IPs returns empty string",
			ips:  []string{"not-an-ip", "also-invalid"},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, primaryLBIP(tt.ips))
		})
	}
}

type mockDNSZoneSyncer struct {
	zoneChangeHandler     func(string)
	force                 func(string) (string, error)
	cached                map[string]string
	refresh               func(*v1alpha1.VPCNetworkConfiguration) (map[string]string, error)
	dnsZoneConfigurations map[string]string
	syncErr               error
}

func (s *mockDNSZoneSyncer) SyncDNSZonesByVpcNetworkConfig(_ *v1alpha1.VPCNetworkConfiguration) (map[string]string, error) {
	return s.dnsZoneConfigurations, s.syncErr
}

func (s *mockDNSZoneSyncer) RefreshDNSZone(path string, bypassStore bool) (string, error) {
	if bypassStore && s.force != nil {
		return s.force(path)
	}
	if s.refresh != nil {
		nc := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{path}}}
		zones, err := s.refresh(nc)
		var missing *dns.DNSZoneNotFoundError
		if err != nil && !bypassStore && !errors.As(err, &missing) {
			return s.cached[path], err
		}
		return zones[path], err
	}
	if s.syncErr != nil && !bypassStore {
		return s.cached[path], s.syncErr
	}
	return s.dnsZoneConfigurations[path], s.syncErr
}

func (s *mockDNSZoneSyncer) SetDNSZoneChangeHandler(handler func(string)) {
	s.zoneChangeHandler = handler
}
func TestSyncDNSZones(t *testing.T) {
	for _, tc := range []struct {
		name    string
		domains map[string]string
		err     error
		want    []string
	}{
		{name: "refresh", domains: map[string]string{"zone1": "new.example", "zone2": "a.example", "empty": ""}, want: []string{"a.example", "new.example"}},
		{name: "removed zones", want: nil},
		{name: "query failure preserves domains", err: errors.New("NSX unavailable"), want: []string{"old.example"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			info := &v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "info", Namespace: "ns"}, AllowedDNSRecordDomains: []string{"old.example"}}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(info).Build()
			r := &NetworkInfoReconciler{Client: c, DNSRecordService: &mockDNSZoneSyncer{dnsZoneConfigurations: tc.domains, syncErr: tc.err}}
			patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getNetworkConfig", func(_ *NetworkInfoReconciler, _ context.Context, _ *v1alpha1.NetworkInfo) (*v1alpha1.VPCNetworkConfiguration, error) {
				var paths []string
				for path := range tc.domains {
					paths = append(paths, path)
				}
				slices.Sort(paths)
				if tc.err != nil {
					paths = []string{"failed-zone"}
				}
				return &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "config"}, Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: paths}}, nil
			})
			defer patches.Reset()
			r.syncDNSZones(context.Background())
			got := &v1alpha1.NetworkInfo{}
			require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(info), got))
			assert.Equal(t, tc.want, got.AllowedDNSRecordDomains)
		})
	}
}

func TestSyncDNSZonesRechecksConfiguration(t *testing.T) {
	for _, bindingChange := range []bool{false, true} {
		t.Run(fmt.Sprintf("bindingChange=%v", bindingChange), func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			info := &v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "info", Namespace: "ns"}}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(info).Build()
			currentConfig := &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "config", ResourceVersion: "1"}, Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{"old-zone"}}}
			calls := 0
			dnsSvc := &mockDNSZoneSyncer{refresh: func(nc *v1alpha1.VPCNetworkConfiguration) (map[string]string, error) {
				calls++
				if calls == 1 {
					currentConfig = currentConfig.DeepCopy()
					if bindingChange {
						currentConfig.Name = "other-config"
					} else {
						currentConfig.ResourceVersion = "2"
					}
					currentConfig.Spec.DNSZones = []string{"new-zone"}
					return map[string]string{"old-zone": "old.example"}, nil
				}
				require.Equal(t, []string{"new-zone"}, nc.Spec.DNSZones)
				return map[string]string{"new-zone": "new.example"}, nil
			}}
			r := &NetworkInfoReconciler{Client: c, DNSRecordService: dnsSvc}
			patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getNetworkConfig", func(_ *NetworkInfoReconciler, _ context.Context, _ *v1alpha1.NetworkInfo) (*v1alpha1.VPCNetworkConfiguration, error) {
				return currentConfig.DeepCopy(), nil
			})
			defer patches.Reset()
			r.syncDNSZones(context.Background())
			got := &v1alpha1.NetworkInfo{}
			require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(info), got))
			require.Equal(t, []string{"new.example"}, got.AllowedDNSRecordDomains)
			require.Equal(t, 2, calls)
		})
	}
}

func TestSyncDNSZonesIgnoresResourceVersionChange(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	info := &v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "info", Namespace: "ns"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(info).Build()
	currentConfig := &v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "config", UID: types.UID("config-uid"), ResourceVersion: "1"},
		Spec:       v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{"zone"}},
	}
	configReads := 0
	zoneRefreshes := 0
	dnsSvc := &mockDNSZoneSyncer{refresh: func(_ *v1alpha1.VPCNetworkConfiguration) (map[string]string, error) {
		zoneRefreshes++
		currentConfig = currentConfig.DeepCopy()
		currentConfig.ResourceVersion = "2"
		return map[string]string{"zone": "new.example"}, nil
	}}
	r := &NetworkInfoReconciler{Client: c, DNSRecordService: dnsSvc}
	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getNetworkConfig", func(_ *NetworkInfoReconciler, _ context.Context, _ *v1alpha1.NetworkInfo) (*v1alpha1.VPCNetworkConfiguration, error) {
		configReads++
		return currentConfig.DeepCopy(), nil
	})
	defer patches.Reset()

	require.NoError(t, r.syncDNSZones(context.Background()))
	got := &v1alpha1.NetworkInfo{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(info), got))
	assert.Equal(t, []string{"new.example"}, got.AllowedDNSRecordDomains)
	assert.Equal(t, 2, configReads, "read once before and once after the NSX refresh")
	assert.Equal(t, 1, zoneRefreshes)
}

type dnsConflictClient struct {
	client.Client
	conflict bool
}

func (c *dnsConflictClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if !c.conflict {
		c.conflict = true
		return apierrors.NewConflict(v1alpha1.Resource("networkinfos"), obj.GetName(), errors.New("concurrent reconcile"))
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func TestSyncDNSZonesRecomputesAfterConflict(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	info := &v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "info", Namespace: "ns"}}
	c := &dnsConflictClient{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(info).Build()}
	calls := 0
	r := &NetworkInfoReconciler{Client: c, DNSRecordService: &mockDNSZoneSyncer{refresh: func(_ *v1alpha1.VPCNetworkConfiguration) (map[string]string, error) {
		calls++
		return map[string]string{"zone": fmt.Sprintf("version%d.example", calls)}, nil
	}}}
	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getNetworkConfig", func(_ *NetworkInfoReconciler, _ context.Context, _ *v1alpha1.NetworkInfo) (*v1alpha1.VPCNetworkConfiguration, error) {
		return &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "config"}, Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{"zone"}}}, nil
	})
	defer patches.Reset()
	r.syncDNSZones(context.Background())
	got := &v1alpha1.NetworkInfo{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(info), got))
	require.Equal(t, []string{"version2.example"}, got.AllowedDNSRecordDomains)
	require.Equal(t, 2, calls)
}

func TestSyncDNSZonesSharesZoneResultsAcrossConfigurations(t *testing.T) {
	for _, failShared := range []bool{false, true} {
		t.Run(fmt.Sprintf("sharedFailure=%v", failShared), func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				&v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "info", Namespace: "a"}, AllowedDNSRecordDomains: []string{"old.example"}},
				&v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "info", Namespace: "b"}, AllowedDNSRecordDomains: []string{"old.example"}},
				&v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "info", Namespace: "healthy"}},
			).Build()
			queries := make(map[string]int)
			r := &NetworkInfoReconciler{Client: c, DNSRecordService: &mockDNSZoneSyncer{refresh: func(nc *v1alpha1.VPCNetworkConfiguration) (map[string]string, error) {
				path := nc.Spec.DNSZones[0]
				queries[path]++
				if failShared && path == "shared" {
					return nil, errors.New("NSX unavailable")
				}
				return map[string]string{path: path + ".example"}, nil
			}}}
			patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getNetworkConfig", func(_ *NetworkInfoReconciler, _ context.Context, info *v1alpha1.NetworkInfo) (*v1alpha1.VPCNetworkConfiguration, error) {
				paths := []string{"shared", info.Namespace, "shared"}
				if info.Namespace == "healthy" {
					paths = []string{"healthy"}
				}
				return &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "config-" + info.Namespace}, Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: paths}}, nil
			})
			defer patches.Reset()
			r.syncDNSZones(context.Background())
			require.Equal(t, 1, queries["shared"], "shared success or failure should be queried once per round")
			for _, ns := range []string{"a", "b", "healthy"} {
				got := &v1alpha1.NetworkInfo{}
				require.NoError(t, c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "info"}, got))
				want := []string{ns + ".example", "shared.example"}
				if failShared {
					want = []string{"old.example"}
				}
				if ns == "healthy" {
					want = []string{"healthy.example"}
				}
				require.Equal(t, want, got.AllowedDNSRecordDomains)
			}
			// Results and errors must not persist into the next polling round.
			r.syncDNSZones(context.Background())
			require.Equal(t, 2, queries["shared"])
			require.Equal(t, 2, queries["healthy"])
		})
	}
}

func TestSyncDNSZonesRetainsCachedDomainWhenRefreshFails(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	info := &v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "info", Namespace: "ns"}, AllowedDNSRecordDomains: []string{"old-a.example", "old-b.example"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(info).Build()
	unavailable := true
	svc := &mockDNSZoneSyncer{}
	svc.refresh = func(nc *v1alpha1.VPCNetworkConfiguration) (map[string]string, error) {
		path := nc.Spec.DNSZones[0]
		if path == "b" && unavailable {
			return nil, errors.New("zone b unavailable")
		}
		return map[string]string{path: "new-" + path + ".example"}, nil
	}
	r := &NetworkInfoReconciler{Client: c, DNSRecordService: svc}
	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getNetworkConfig", func(_ *NetworkInfoReconciler, _ context.Context, _ *v1alpha1.NetworkInfo) (*v1alpha1.VPCNetworkConfiguration, error) {
		return &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "config"}, Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{"a", "b"}}}, nil
	})
	defer patches.Reset()
	require.Error(t, r.syncDNSZones(context.Background()))
	got := &v1alpha1.NetworkInfo{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(info), got))
	require.Equal(t, []string{"old-a.example", "old-b.example"}, got.AllowedDNSRecordDomains)

	unavailable = false
	require.NoError(t, r.syncDNSZones(context.Background()))
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(info), got))
	require.Equal(t, []string{"new-a.example", "new-b.example"}, got.AllowedDNSRecordDomains)
}

func TestHandleDNSZoneChangeDoesNotPatchUnchangedDomains(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	affected := &v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "info", Namespace: "affected"}, AllowedDNSRecordDomains: []string{"same.example"}}
	other := &v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "info", Namespace: "other"}, AllowedDNSRecordDomains: []string{"other.example"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(affected, other).Build()
	forced := 0
	svc := &mockDNSZoneSyncer{dnsZoneConfigurations: map[string]string{"zone": "same.example", "other-zone": "other.example"}, force: func(path string) (string, error) {
		require.Equal(t, "zone", path)
		forced++
		if forced == 1 {
			return "", errors.New("NSX unavailable")
		}
		return "same.example", nil
	}}
	r := &NetworkInfoReconciler{Client: c, DNSRecordService: svc}
	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getNetworkConfig", func(_ *NetworkInfoReconciler, _ context.Context, info *v1alpha1.NetworkInfo) (*v1alpha1.VPCNetworkConfiguration, error) {
		path := "zone"
		if info.Namespace == "other" {
			path = "other-zone"
		}
		return &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: info.Namespace}, Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{path}}}, nil
	})
	defer patches.Reset()
	require.Error(t, r.handleDNSZoneChange(context.Background(), "zone"))
	current := &v1alpha1.NetworkInfo{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(affected), current))
	beforeVersion := current.ResourceVersion
	require.NoError(t, r.handleDNSZoneChange(context.Background(), "zone"))
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(affected), current))
	require.Equal(t, []string{"same.example"}, current.AllowedDNSRecordDomains)
	require.Equal(t, beforeVersion, current.ResourceVersion)
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(other), current))
	require.Empty(t, current.Annotations)
	require.Equal(t, 2, forced)
}

func TestDNSZoneRefreshQueueRetries(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	info := &v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "info", Namespace: "ns"}, AllowedDNSRecordDomains: []string{"old.example"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(info).Build()
	attempts := make(chan string, 4)
	calls := 0
	svc := &mockDNSZoneSyncer{dnsZoneConfigurations: map[string]string{"zone": "example.com"}, force: func(path string) (string, error) {
		calls++
		attempts <- path
		if calls == 1 {
			return "", errors.New("temporary failure")
		}
		return "example.com", nil
	}}
	r := &NetworkInfoReconciler{Client: c, DNSRecordService: svc, dnsZoneRefreshQueue: workqueue.NewTypedRateLimitingQueue(workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Millisecond, time.Millisecond))}
	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getNetworkConfig", func(_ *NetworkInfoReconciler, _ context.Context, _ *v1alpha1.NetworkInfo) (*v1alpha1.VPCNetworkConfiguration, error) {
		return &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "config"}, Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{"zone"}}}, nil
	})
	defer patches.Reset()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.runDNSZoneRefreshQueue(ctx) }()
	r.dnsZoneRefreshQueue.Add("zone")
	r.dnsZoneRefreshQueue.Add("zone")
	require.Eventually(t, func() bool {
		current := &v1alpha1.NetworkInfo{}
		return c.Get(context.Background(), client.ObjectKeyFromObject(info), current) == nil && len(current.AllowedDNSRecordDomains) == 1 && current.AllowedDNSRecordDomains[0] == "example.com"
	}, 5*time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)
	require.Equal(t, "zone", <-attempts)
	require.Equal(t, "zone", <-attempts)
	require.Equal(t, 2, calls)
}

type failOneDNSNotificationClient struct {
	client.Client
	patches map[string]int
}

func (c *failOneDNSNotificationClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	c.patches[obj.GetNamespace()]++
	if obj.GetNamespace() == "b" && c.patches["b"] == 1 {
		return errors.New("temporary API failure")
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func TestDNSZoneRefreshRetryDoesNotRepeatSuccessfulPatches(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	objects := []client.Object{
		&v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "info", Namespace: "a"}, AllowedDNSRecordDomains: []string{"old.example"}},
		&v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "info", Namespace: "b"}, AllowedDNSRecordDomains: []string{"old.example"}},
	}
	c := &failOneDNSNotificationClient{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build(), patches: map[string]int{}}
	svc := &mockDNSZoneSyncer{dnsZoneConfigurations: map[string]string{"zone": "example.com"}}
	r := &NetworkInfoReconciler{Client: c, DNSRecordService: svc}
	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getNetworkConfig", func(_ *NetworkInfoReconciler, _ context.Context, _ *v1alpha1.NetworkInfo) (*v1alpha1.VPCNetworkConfiguration, error) {
		return &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "config"}, Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{"zone"}}}, nil
	})
	defer patches.Reset()
	require.Error(t, r.handleDNSZoneChange(context.Background(), "zone"))
	require.NoError(t, r.handleDNSZoneChange(context.Background(), "zone"))
	require.Equal(t, 1, c.patches["a"])
	require.Equal(t, 2, c.patches["b"])
	for _, ns := range []string{"a", "b"} {
		current := &v1alpha1.NetworkInfo{}
		require.NoError(t, c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "info"}, current))
		require.Equal(t, []string{"example.com"}, current.AllowedDNSRecordDomains)
		require.Empty(t, current.Annotations)
	}
}

func TestDNSZoneDomainTransitionsUpdateNetworkInfo(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	info := &v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "info", Namespace: "ns"}, AllowedDNSRecordDomains: []string{"old.example"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(info).Build()
	domain := "old.example"
	svc := &mockDNSZoneSyncer{refresh: func(_ *v1alpha1.VPCNetworkConfiguration) (map[string]string, error) {
		return map[string]string{"zone": domain}, nil
	}}
	r := &NetworkInfoReconciler{Client: c, DNSRecordService: svc}
	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getNetworkConfig", func(_ *NetworkInfoReconciler, _ context.Context, _ *v1alpha1.NetworkInfo) (*v1alpha1.VPCNetworkConfiguration, error) {
		return &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "config"}, Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{"zone"}}}, nil
	})
	defer patches.Reset()
	current := &v1alpha1.NetworkInfo{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(info), current))
	initialVersion := current.ResourceVersion
	domain = "mid.example"
	require.NoError(t, r.syncDNSZones(context.Background()))
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(info), current))
	require.Equal(t, []string{"mid.example"}, current.AllowedDNSRecordDomains)
	midVersion := current.ResourceVersion
	require.NotEqual(t, initialVersion, midVersion)
	domain = "old.example"
	require.NoError(t, r.syncDNSZones(context.Background()))
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(info), current))
	require.Equal(t, []string{"old.example"}, current.AllowedDNSRecordDomains)
	require.NotEqual(t, midVersion, current.ResourceVersion)
	require.Empty(t, current.Annotations)
}

func TestDeletedDNSZoneUpdatesConfigurationAndNetworkInfos(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	nc := &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "config"}, Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{"deleted", "healthy"}}}
	info := func(ns string) *v1alpha1.NetworkInfo {
		return &v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "info", Namespace: ns}, AllowedDNSRecordDomains: []string{"deleted.example", "healthy.example"}}
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(nc).WithObjects(nc, info("a"), info("b")).Build()
	deleted := true
	svc := &mockDNSZoneSyncer{}
	svc.force = func(path string) (string, error) {
		if path == "deleted" && deleted {
			return "", &dns.DNSZoneNotFoundError{Path: path, Err: errors.New("not found")}
		}
		return path + ".example", nil
	}
	svc.refresh = func(nc *v1alpha1.VPCNetworkConfiguration) (map[string]string, error) {
		path := nc.Spec.DNSZones[0]
		return map[string]string{path: path + ".example"}, nil
	}
	r := &NetworkInfoReconciler{Client: c, DNSRecordService: svc}
	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getNetworkConfig", func(_ *NetworkInfoReconciler, ctx context.Context, _ *v1alpha1.NetworkInfo) (*v1alpha1.VPCNetworkConfiguration, error) {
		current := &v1alpha1.VPCNetworkConfiguration{}
		err := c.Get(ctx, client.ObjectKey{Name: "config"}, current)
		return current, err
	})
	defer patches.Reset()

	require.NoError(t, r.handleDNSZoneChange(ctx, "deleted"))
	for _, ns := range []string{"a", "b"} {
		got := &v1alpha1.NetworkInfo{}
		require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: ns, Name: "info"}, got))
		require.Equal(t, []string{"healthy.example"}, got.AllowedDNSRecordDomains)
	}
	gotNC := &v1alpha1.VPCNetworkConfiguration{}
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "config"}, gotNC))
	condition := getExistingConditionOfType(v1alpha1.DNSZonesReady, gotNC.Status.Conditions)
	require.NotNil(t, condition)
	require.Equal(t, corev1.ConditionFalse, condition.Status)
	require.Equal(t, "DNSZoneNotFound", condition.Reason)
	require.Contains(t, condition.Message, "deleted")

	deleted = false
	require.NoError(t, r.syncDNSZones(ctx))
	for _, ns := range []string{"a", "b"} {
		got := &v1alpha1.NetworkInfo{}
		require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: ns, Name: "info"}, got))
		require.Equal(t, []string{"deleted.example", "healthy.example"}, got.AllowedDNSRecordDomains)
	}
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "config"}, gotNC))
	condition = getExistingConditionOfType(v1alpha1.DNSZonesReady, gotNC.Status.Conditions)
	require.Equal(t, corev1.ConditionTrue, condition.Status)
	require.Equal(t, "DNSZonesAvailable", condition.Reason)
}

func TestDeletedDNSZoneWithoutNetworkInfo(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	nc := &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "orphan-config"}, Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{"zone"}}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(nc).WithObjects(nc).Build()
	missing := true
	unavailable := false
	svc := &mockDNSZoneSyncer{refresh: func(_ *v1alpha1.VPCNetworkConfiguration) (map[string]string, error) {
		if unavailable {
			return nil, errors.New("NSX unavailable")
		}
		if missing {
			return nil, &dns.DNSZoneNotFoundError{Path: "zone", Err: errors.New("not found")}
		}
		return map[string]string{"zone": "example.com"}, nil
	}}
	r := &NetworkInfoReconciler{Client: c, DNSRecordService: svc}
	require.NoError(t, r.syncDNSZones(ctx))
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: nc.Name}, nc))
	condition := getExistingConditionOfType(v1alpha1.DNSZonesReady, nc.Status.Conditions)
	require.NotNil(t, condition)
	require.Equal(t, corev1.ConditionFalse, condition.Status)
	missing = false
	unavailable = true
	require.Error(t, r.syncDNSZones(ctx))
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: nc.Name}, nc))
	require.Equal(t, corev1.ConditionFalse, getExistingConditionOfType(v1alpha1.DNSZonesReady, nc.Status.Conditions).Status)
	unavailable = false
	require.NoError(t, r.syncDNSZones(ctx))
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: nc.Name}, nc))
	condition = getExistingConditionOfType(v1alpha1.DNSZonesReady, nc.Status.Conditions)
	require.Equal(t, corev1.ConditionTrue, condition.Status)
}

func TestDNSZonePollAndForcedRefreshAreSerialized(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	info := &v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "info"}, AllowedDNSRecordDomains: []string{"new.example"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(info).Build()
	pollStarted, releasePoll, forced := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releasePoll) })
	var refreshCalls atomic.Int32
	svc := &mockDNSZoneSyncer{
		force: func(_ string) (string, error) {
			if refreshCalls.Add(1) == 1 {
				close(pollStarted)
				<-releasePoll
				return "old.example", nil
			}
			close(forced)
			return "new.example", nil
		},
	}
	r := &NetworkInfoReconciler{Client: c, DNSRecordService: svc}
	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getNetworkConfig", func(_ *NetworkInfoReconciler, _ context.Context, _ *v1alpha1.NetworkInfo) (*v1alpha1.VPCNetworkConfiguration, error) {
		return &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "config", ResourceVersion: "1"}, Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{"zone"}}}, nil
	})
	defer patches.Reset()
	pollDone, callbackDone := make(chan error, 1), make(chan error, 1)
	go func() { pollDone <- r.syncDNSZones(context.Background()) }()
	select {
	case <-pollStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("poll did not start")
	}
	callbackStarted := make(chan struct{})
	go func() {
		close(callbackStarted)
		callbackDone <- r.handleDNSZoneChange(context.Background(), "zone")
	}()
	<-callbackStarted
	select {
	case <-forced:
		t.Fatal("forced refresh ran before the older poll finished")
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(releasePoll) })
	require.NoError(t, <-pollDone)
	require.NoError(t, <-callbackDone)
	current := &v1alpha1.NetworkInfo{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(info), current))
	require.Equal(t, []string{"new.example"}, current.AllowedDNSRecordDomains)
}

func TestDNSZonePollWaitsForForcedRefresh(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	info := &v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "info"}, AllowedDNSRecordDomains: []string{"old.example"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(info).Build()
	forceStarted, releaseForce, refreshCalled := make(chan struct{}), make(chan struct{}), make(chan struct{}, 2)
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseForce) })
	var refreshCalls atomic.Int32
	svc := &mockDNSZoneSyncer{
		force: func(_ string) (string, error) {
			if refreshCalls.Add(1) == 1 {
				close(forceStarted)
				<-releaseForce
				return "new.example", nil
			}
			refreshCalled <- struct{}{}
			return "new.example", nil
		},
	}
	r := &NetworkInfoReconciler{Client: c, DNSRecordService: svc}
	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getNetworkConfig", func(_ *NetworkInfoReconciler, _ context.Context, _ *v1alpha1.NetworkInfo) (*v1alpha1.VPCNetworkConfiguration, error) {
		return &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "config"}, Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{"zone"}}}, nil
	})
	defer patches.Reset()
	pollDone, callbackDone := make(chan error, 1), make(chan error, 1)
	go func() { callbackDone <- r.handleDNSZoneChange(context.Background(), "zone") }()
	select {
	case <-forceStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("forced refresh did not start")
	}
	go func() { pollDone <- r.syncDNSZones(context.Background()) }()
	select {
	case <-refreshCalled:
		t.Fatal("poll read zones while forced refresh was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(releaseForce) })
	require.NoError(t, <-callbackDone)
	require.NoError(t, <-pollDone)
	current := &v1alpha1.NetworkInfo{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(info), current))
	require.Equal(t, []string{"new.example"}, current.AllowedDNSRecordDomains)
}
