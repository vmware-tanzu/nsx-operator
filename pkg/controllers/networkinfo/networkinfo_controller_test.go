/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkinfo

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

type fakeVPCConnectivityProfilesClient struct{}

func (c *fakeVPCConnectivityProfilesClient) Get(orgIdParam string, projectIdParam string, vpcConnectivityProfileIdParam string) (model.VpcConnectivityProfile, error) {
	return model.VpcConnectivityProfile{}, nil
}

func (c *fakeVPCConnectivityProfilesClient) Delete(orgIdParam string, projectIdParam string, vpcConnectivityProfileIdParam string) error {
	return nil
}

func (c *fakeVPCConnectivityProfilesClient) List(orgIdParam string, projectIdParam string, cursorParam *string, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.VpcConnectivityProfileListResult, error) {
	return model.VpcConnectivityProfileListResult{}, nil
}

func (c *fakeVPCConnectivityProfilesClient) Patch(orgIdParam string, projectIdParam string, vpcConnectivityProfileIdParam string, vpcConnectivityProfileParam model.VpcConnectivityProfile) error {
	return nil
}

func (c *fakeVPCConnectivityProfilesClient) Update(orgIdParam string, projectIdParam string, vpcConnectivityProfileIdParam string, vpcConnectivityProfileParam model.VpcConnectivityProfile) (model.VpcConnectivityProfile, error) {
	return model.VpcConnectivityProfile{}, nil
}

type fakeVpcAttachmentClient struct{}

func (c *fakeVpcAttachmentClient) List(orgIdParam string, projectIdParam string, vpcIdParam string, cursorParam *string, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.VpcAttachmentListResult, error) {
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
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(objs...).Build()

	vpcStore := &vpc.VPCStore{ResourceStore: servicecommon.ResourceStore{
		BindingType: model.VpcBindingType(),
	}}

	service := &vpc.VPCService{
		VpcStore: vpcStore,
		Service: servicecommon.Service{
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
		VPCNetworkConfigStore: vpc.VPCNetworkInfoStore{
			VPCNetworkConfigMap: map[string]servicecommon.VPCNetworkConfigInfo{},
		},
		VPCNSNetworkConfigStore: vpc.VPCNsNetworkConfigStore{
			VPCNSNetworkConfigMap: map[string]string{},
		},
	}

	r := &NetworkInfoReconciler{
		Client:   fakeClient,
		Scheme:   fake.NewClientBuilder().Build().Scheme(),
		Service:  service,
		Recorder: &fakeRecorder{},
	}
	r.StatusUpdater = common.NewStatusUpdater(r.Client, r.Service.NSXConfig, r.Recorder, MetricResType, "VPC", "NetworkInfo")
	return r
}

func TestNetworkInfoReconciler_Reconcile(t *testing.T) {
	type args struct {
		req controllerruntime.Request
	}
	requestArgs := args{
		req: controllerruntime.Request{NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "name"}},
	}
	tests := []struct {
		name        string
		prepareFunc func(*testing.T, *NetworkInfoReconciler, context.Context) *gomonkey.Patches
		args        args
		want        controllerruntime.Result
		wantErr     bool
	}{
		{
			name: "Empty",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCsByNamespace", func(_ *vpc.VPCService, _ string) []*model.Vpc {
					return []*model.Vpc{}
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
				patches.ApplyMethod(reflect.TypeOf(&vpc.VPCService{}), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (servicecommon.VPCNetworkConfigInfo, bool) {
					return servicecommon.VPCNetworkConfigInfo{
						Name:                   servicecommon.SystemVPCNetworkConfigurationName,
						VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
						Org:                    "default",
						NSXProject:             "project-quality",
					}, true
				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ context.Context, _ *v1alpha1.VPCNetworkConfiguration) (bool, string) {
					return false, ""
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateGatewayConnectionStatus", func(_ *vpc.VPCService, _ *servicecommon.VPCNetworkConfigInfo) (bool, string, error) {
					return true, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *servicecommon.VPCNetworkConfigInfo, _ vpc.LBProvider) (*model.Vpc, error) {
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
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) vpc.LBProvider {
					return vpc.NSXLB
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
					ObjectMeta: metav1.ObjectMeta{
						Name: "system",
					},
				}))
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, _ context.Context, _ string) (string, error) {
					return "non-system", nil

				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (servicecommon.VPCNetworkConfigInfo, bool) {
					return servicecommon.VPCNetworkConfigInfo{
						Name:                   "non-system",
						VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
						Org:                    "default",
						NSXProject:             "project-quality",
					}, true

				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ context.Context, _ *v1alpha1.VPCNetworkConfiguration) (bool, string) {
					return true, ""
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateGatewayConnectionStatus", func(_ *vpc.VPCService, _ *servicecommon.VPCNetworkConfigInfo) (bool, string, error) {
					assert.FailNow(t, "should not be called")
					return true, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *servicecommon.VPCNetworkConfigInfo, _ vpc.LBProvider) (*model.Vpc, error) {
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
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) vpc.LBProvider {
					return vpc.NSXLB
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultNSXLBSPathByVPC", func(_ *vpc.VPCService, _ string) string {
					return "lbs-path"

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
				patches.ApplyMethod(reflect.TypeOf(r), "GetVpcConnectivityProfilePathByVpcPath", func(_ *NetworkInfoReconciler, _ string) (string, error) {
					return "connectivity_profile", nil
				})

				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (servicecommon.VPCNetworkConfigInfo, bool) {
					return servicecommon.VPCNetworkConfigInfo{
						Name:                   "non-system",
						VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
						Org:                    "default",
						NSXProject:             "project-quality",
					}, true

				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ context.Context, _ *v1alpha1.VPCNetworkConfiguration) (bool, string) {
					return false, ""
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateGatewayConnectionStatus", func(_ *vpc.VPCService, _ *servicecommon.VPCNetworkConfigInfo) (bool, string, error) {
					assert.FailNow(t, "should not be called")
					return true, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *servicecommon.VPCNetworkConfigInfo, _ vpc.LBProvider) (*model.Vpc, error) {
					assert.FailNow(t, "should not be called")
					return &model.Vpc{}, nil
				})
				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCGwConnectionNotReady.getNSNetworkCondition()))
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
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (servicecommon.VPCNetworkConfigInfo, bool) {
					return servicecommon.VPCNetworkConfigInfo{
						Name:                   servicecommon.SystemVPCNetworkConfigurationName,
						VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
						Org:                    "default",
						NSXProject:             "project-quality",
					}, true

				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ context.Context, _ *v1alpha1.VPCNetworkConfiguration) (bool, string) {
					return false, ""
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateGatewayConnectionStatus", func(_ *vpc.VPCService, _ *servicecommon.VPCNetworkConfigInfo) (bool, string, error) {
					return true, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) vpc.LBProvider {
					return vpc.NSXLB
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *servicecommon.VPCNetworkConfigInfo, _ vpc.LBProvider) (*model.Vpc, error) {
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
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) vpc.LBProvider {
					return vpc.NSXLB
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultSNATIP", func(_ *vpc.VPCService, _ model.Vpc) (string, error) {
					return "snat-ip", nil

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
			name: "AutoSnatNotEnabledInSystemVPC",
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
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (servicecommon.VPCNetworkConfigInfo, bool) {
					return servicecommon.VPCNetworkConfigInfo{
						Name:                   servicecommon.SystemVPCNetworkConfigurationName,
						VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
						Org:                    "default",
						NSXProject:             "project-quality",
					}, true

				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ context.Context, _ *v1alpha1.VPCNetworkConfiguration) (bool, string) {
					return false, ""
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateGatewayConnectionStatus", func(_ *vpc.VPCService, _ *servicecommon.VPCNetworkConfigInfo) (bool, string, error) {
					return true, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) vpc.LBProvider {
					return vpc.NSXLB
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *servicecommon.VPCNetworkConfigInfo, _ vpc.LBProvider) (*model.Vpc, error) {
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
						ServiceGateway:   nil,
					}, nil},
					Times: 1,
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
			name: "AutoSnatEnabledInNonSystemVPC",
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
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (servicecommon.VPCNetworkConfigInfo, bool) {
					return servicecommon.VPCNetworkConfigInfo{
						Name:                   "non-system",
						VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
						Org:                    "default",
						NSXProject:             "project-quality",
					}, true

				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ context.Context, _ *v1alpha1.VPCNetworkConfiguration) (bool, string) {
					return true, ""
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateGatewayConnectionStatus", func(_ *vpc.VPCService, _ *servicecommon.VPCNetworkConfigInfo) (bool, string, error) {
					return true, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) vpc.LBProvider {
					return vpc.NSXLB
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *servicecommon.VPCNetworkConfigInfo, _ vpc.LBProvider) (*model.Vpc, error) {
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
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultSNATIP", func(_ *vpc.VPCService, _ model.Vpc) (string, error) {
					return "snat-ip", nil

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
			name: "VPCNetworkConfigurationStatusWithNoExternalIPBlockInSystemVPC",
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
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (servicecommon.VPCNetworkConfigInfo, bool) {
					return servicecommon.VPCNetworkConfigInfo{
						Name:                   servicecommon.SystemVPCNetworkConfigurationName,
						VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
						Org:                    "default",
						NSXProject:             "project-quality",
					}, true

				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ context.Context, _ *v1alpha1.VPCNetworkConfiguration) (bool, string) {
					return false, ""
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateGatewayConnectionStatus", func(_ *vpc.VPCService, _ *servicecommon.VPCNetworkConfigInfo) (bool, string, error) {
					return true, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) vpc.LBProvider {
					return vpc.NSXLB
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *servicecommon.VPCNetworkConfigInfo, _ vpc.LBProvider) (*model.Vpc, error) {
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
						ServiceGateway: nil,
					}, nil},
					Times: 1,
				}})
				patches.ApplyFunc(setVPCNetworkConfigurationStatusWithNoExternalIPBlock,
					func(_ context.Context, _ client.Client, _ *v1alpha1.VPCNetworkConfiguration, _ bool) {
						t.Log("setVPCNetworkConfigurationStatusWithNoExternalIPBlock")
					})

				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultNSXLBSPathByVPC", func(_ *vpc.VPCService, _ string) string {
					return "lbs-path"

				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultSNATIP", func(_ *vpc.VPCService, _ model.Vpc) (string, error) {
					return "snat-ip", nil

				})
				patches.ApplyFunc(setVPCNetworkConfigurationStatusWithSnatEnabled,
					func(_ context.Context, _ client.Client, _ *v1alpha1.VPCNetworkConfiguration, autoSnatEnabled bool) {
						if autoSnatEnabled {
							assert.FailNow(t, "should set VPCNetworkConfiguration status with AutoSnatEnabled=false")
						}
					})
				patches.ApplyFunc(setNSNetworkReadyCondition,
					func(ctx context.Context, client client.Client, nsName string, condition *corev1.NamespaceCondition) {
						require.True(t, nsConditionEquals(*condition, *nsMsgVPCNoExternalIPBlock.getNSNetworkCondition()))
					})
				return patches
			},
			args:    requestArgs,
			want:    common.ResultRequeueAfter60sec,
			wantErr: false,
		}, {
			name: "Pre-create VPC success case",
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
				patches.ApplyMethod(reflect.TypeOf(r), "GetVpcConnectivityProfilePathByVpcPath", func(_ *NetworkInfoReconciler, _ string) (string, error) {
					return "connectivity_profile", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (servicecommon.VPCNetworkConfigInfo, bool) {
					return servicecommon.VPCNetworkConfigInfo{
						Org:        "default",
						NSXProject: "project-quality",
						VPCPath:    "/orgs/default/projects/nsx_operator_e2e_test/vpcs/pre-vpc",
					}, true

				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ context.Context, _ *v1alpha1.VPCNetworkConfiguration) (bool, string) {
					return true, ""
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) vpc.LBProvider {
					return vpc.AVILB
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, ctx context.Context, _ *v1alpha1.NetworkInfo, _ *servicecommon.VPCNetworkConfigInfo, _ vpc.LBProvider) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})

				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBSsFromNSXByVPC", func(_ *vpc.VPCService, _ string) (string, error) {
					return "/orgs/default/projects/project-quality/vpcs/fake-vpc/vpc-lbs/lbs", nil
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := createNetworkInfoReconciler(nil)
			v1alpha1.AddToScheme(r.Scheme)
			ctx := context.TODO()
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc(t, r, ctx)
				defer patches.Reset()
			}
			got, err := r.Reconcile(ctx, tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("Reconcile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Reconcile() got = %v, want %v", got, tt.want)
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
		}, {
			name: "NSX VPC is used by a valid Namespace,, skip deletion",
			prepareFuncs: func(r *NetworkInfoReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCsByNamespace", func(_ *vpc.VPCService, _ string) []*model.Vpc {
					return []*model.Vpc{
						{Tags: []model.Tag{
							{Scope: servicecommon.String(servicecommon.TagScopeNamespaceUID), Tag: servicecommon.String("vpc1")}}}}
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
						}}
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
						}}
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "listNamespaceCRsNameIDSet", func(_ *NetworkInfoReconciler, _ context.Context) (sets.Set[string], sets.Set[string], error) {
					return sets.Set[string]{}, sets.Set[string]{}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "DeleteVPC", func(_ *vpc.VPCService, _ string) error {
					return nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, _ string) *servicecommon.VPCNetworkConfigInfo {
					return nil
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
				Status: v1alpha1.VPCNetworkConfigurationStatus{VPCs: []v1alpha1.VPCInfo{
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
						}}
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "listNamespaceCRsNameIDSet", func(_ *NetworkInfoReconciler, _ context.Context) (sets.Set[string], sets.Set[string], error) {
					return sets.Set[string]{}, sets.Set[string]{}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "DeleteVPC", func(_ *vpc.VPCService, _ string) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "listVPCsByNetworkConfigName", func(_ *NetworkInfoReconciler, _ string) []*model.Vpc {
					return []*model.Vpc{
						{
							Tags: []model.Tag{
								{Scope: servicecommon.String(servicecommon.TagScopeNamespaceUID), Tag: servicecommon.String("vpc1")},
							},
							Path:        servicecommon.String("/orgs/default/projects/default/vpcs/vpc2"),
							DisplayName: servicecommon.String("fakeDisplayName2"),
						},
					}
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, _ string) *servicecommon.VPCNetworkConfigInfo {
					return &servicecommon.VPCNetworkConfigInfo{
						Name: "fakeNetworkconfigName",
					}
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

			err := r.deleteVPCsByName(ctx, namespace)

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
						}}
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
				ObjectMeta: metav1.ObjectMeta{Name: "testNetworkInfo", Namespace: "testNamespace",
					DeletionTimestamp: &metav1.Time{Time: time.Now()}, Finalizers: []string{"test-Finalizers"}},
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
				ObjectMeta: metav1.ObjectMeta{Name: "testNetworkInfo", Namespace: "testNamespace", UID: "fakeNamespaceUID",
					DeletionTimestamp: &metav1.Time{Time: time.Now()}, Finalizers: []string{"test-Finalizers"}},
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
			expectErrStr: "failed to locate default network config",
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

func TestNetworkInfoReconciler_GetVpcConnectivityProfilePathByVpcPath(t *testing.T) {
	tests := []struct {
		name        string
		vpcPath     string
		prepareFunc func(*testing.T, *NetworkInfoReconciler, context.Context) *gomonkey.Patches
		want        string
		wantErr     bool
	}{
		{
			name:    "Invalid VPC Path",
			vpcPath: "/invalid/path",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) *gomonkey.Patches {
				return nil
			},
			want:    "",
			wantErr: true,
		},
		{
			name:    "Failed to list VPC attachment",
			vpcPath: "/orgs/default/projects/project-quality/vpcs/fake-vpc",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service.NSXClient.VpcAttachmentClient), "List", func(_ *fakeVpcAttachmentClient, _ string, _ string, _ string, _ *string, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.VpcAttachmentListResult, error) {
					return model.VpcAttachmentListResult{}, fmt.Errorf("list error")
				})
				return patches
			},
			want:    "",
			wantErr: true,
		},
		{
			name:    "No VPC attachment found",
			vpcPath: "/orgs/default/projects/project-quality/vpcs/fake-vpc",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service.NSXClient.VpcAttachmentClient), "List", func(_ *fakeVpcAttachmentClient, _ string, _ string, _ string, _ *string, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.VpcAttachmentListResult, error) {
					return model.VpcAttachmentListResult{Results: []model.VpcAttachment{}}, nil
				})
				return patches
			},
			want:    "",
			wantErr: true,
		},
		{
			name:    "Successful VPC attachment retrieval",
			vpcPath: "/orgs/default/projects/project-quality/vpcs/fake-vpc",
			prepareFunc: func(t *testing.T, r *NetworkInfoReconciler, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(fakeAttachmentClient), "List", func(_ *fakeVpcAttachmentClient, _ string, _ string, _ string, _ *string, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.VpcAttachmentListResult, error) {
					return model.VpcAttachmentListResult{
						Results: []model.VpcAttachment{
							{VpcConnectivityProfile: servicecommon.String("/orgs/default/projects/project-quality/vpc-connectivity-profiles/default"),
								ParentPath: servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
								Path:       servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc/attachments/default")},
						},
					}, nil
				})
				return patches
			},
			want:    "/orgs/default/projects/project-quality/vpc-connectivity-profiles/default",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := createNetworkInfoReconciler(nil)
			ctx := context.TODO()
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc(t, r, ctx)
				if patches != nil {
					defer patches.Reset()
				}
			}
			got, err := r.GetVpcConnectivityProfilePathByVpcPath(tt.vpcPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetVpcConnectivityProfilePathByVpcPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetVpcConnectivityProfilePathByVpcPath() got = %v, want %v", got, tt.want)
			}
		})
	}
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
	patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNamespacesWithPreCreatedVPCs", func(_ *vpc.VPCService) map[string]string {
		return map[string]string{
			"ns1": "/orgs/default/projects/p1/vpcs/vpc1",
			"ns2": "/orgs/default/projects/p1/vpcs/vpc2",
			"ns3": "/orgs/default/projects/p1/vpcs/vpc3",
			"ns4": "/orgs/default/projects/p1/vpcs/vpc4",
			"ns5": "/orgs/default/projects/p1/vpcs/vpc5",
			"ns6": "/orgs/default/projects/p1/vpcs/vpc6",
		}
	})
	defer patches.Reset()

	expRequests := []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: "net1", Namespace: "ns2"}},
		{NamespacedName: types.NamespacedName{Name: "net1", Namespace: "ns3"}},
		{NamespacedName: types.NamespacedName{Name: "net1", Namespace: "ns6"}},
	}

	r.syncPreCreatedVpcIPs(ctx)
	r.queue.Add(stopSig)
	requests := getQueuedReqs(r.queue)
	assert.ElementsMatch(t, expRequests, requests)
}

func TestListVPCsByNetworkConfigName(t *testing.T) {
	r := &NetworkInfoReconciler{
		Service: &vpc.VPCService{},
	}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNamespacesByNetworkconfigName", func(_ *vpc.VPCService, _c string) []string {
		return []string{"ns1", "ns2"}
	})
	patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCsByNamespace", func(_ *vpc.VPCService, ns string) []*model.Vpc {
		if ns == "ns1" {
			return []*model.Vpc{{Id: servicecommon.String("vpc1")}}
		}
		return []*model.Vpc{{Id: servicecommon.String("vpc2")}}
	})
	defer patches.Reset()
	expVPCs := []*model.Vpc{{Id: servicecommon.String("vpc1")}, {Id: servicecommon.String("vpc2")}}
	actVPCs := r.listVPCsByNetworkConfigName("nc1")
	assert.ElementsMatch(t, expVPCs, actVPCs)
}
