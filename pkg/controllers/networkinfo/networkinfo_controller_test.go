/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkinfo

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

func createNetworkInfoReconciler() *NetworkInfoReconciler {
	service := &vpc.VPCService{
		Service: servicecommon.Service{
			Client: nil,
			NSXClient: &nsx.Client{
				VPCConnectivityProfilesClient: &fakeVPCConnectivityProfilesClient{},
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

	return &NetworkInfoReconciler{
		Client:  fake.NewClientBuilder().Build(),
		Scheme:  fake.NewClientBuilder().Build().Scheme(),
		Service: service,
	}
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
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, _ string) (bool, error) {
					return false, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCsByNamespace", func(_ *vpc.VPCService, _ string) []*model.Vpc {
					return nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, _ string) (string, error) {
					return "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ListVPC", func(_ *vpc.VPCService) []model.Vpc {
					return nil
				})
				patches.ApplyFunc(deleteVPCNetworkConfigurationStatus, func(ctx context.Context, client client.Client, ncName string, staleVPCs []*model.Vpc, aliveVPCs []model.Vpc) {
					return
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
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, _ string) (string, error) {
					return servicecommon.SystemVPCNetworkConfigurationName, nil

				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (servicecommon.VPCNetworkConfigInfo, bool) {
					return servicecommon.VPCNetworkConfigInfo{
						VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
						Org:                    "default",
						NSXProject:             "project-quality",
					}, true

				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ context.Context, _ *v1alpha1.VPCNetworkConfiguration) (bool, string, error) {
					return false, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateGatewayConnectionStatus", func(_ *vpc.VPCService, _ *servicecommon.VPCNetworkConfigInfo) (bool, string, error) {
					return true, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, _ *v1alpha1.NetworkInfo, _ *servicecommon.VPCNetworkConfigInfo, _ vpc.LBProvider) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, _ string) (bool, error) {
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
				patches.ApplyFunc(updateSuccess,
					func(_ *NetworkInfoReconciler, _ context.Context, o *v1alpha1.NetworkInfo, _ client.Client, _ *v1alpha1.VPCState, _ string, _ string) {
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
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, _ string) (string, error) {
					return servicecommon.SystemVPCNetworkConfigurationName, nil

				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (servicecommon.VPCNetworkConfigInfo, bool) {
					return servicecommon.VPCNetworkConfigInfo{
						VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
						Org:                    "default",
						NSXProject:             "project-quality",
					}, true

				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ context.Context, _ *v1alpha1.VPCNetworkConfiguration) (bool, string, error) {
					return true, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateGatewayConnectionStatus", func(_ *vpc.VPCService, _ *servicecommon.VPCNetworkConfigInfo) (bool, string, error) {
					assert.FailNow(t, "should not be called")
					return true, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, _ *v1alpha1.NetworkInfo, _ *servicecommon.VPCNetworkConfigInfo, _ vpc.LBProvider) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, _ string) (bool, error) {
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
				patches.ApplyFunc(updateSuccess,
					func(_ *NetworkInfoReconciler, _ context.Context, o *v1alpha1.NetworkInfo, _ client.Client, _ *v1alpha1.VPCState, _ string, _ string) {
					})
				return patches

			},
			args:    requestArgs,
			want:    common.ResultRequeueAfter60sec,
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
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, _ string) (string, error) {
					return "non-system", nil

				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (servicecommon.VPCNetworkConfigInfo, bool) {
					return servicecommon.VPCNetworkConfigInfo{
						VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
						Org:                    "default",
						NSXProject:             "project-quality",
					}, true

				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ context.Context, _ *v1alpha1.VPCNetworkConfiguration) (bool, string, error) {
					return false, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateGatewayConnectionStatus", func(_ *vpc.VPCService, _ *servicecommon.VPCNetworkConfigInfo) (bool, string, error) {
					assert.FailNow(t, "should not be called")
					return true, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) vpc.LBProvider {
					return vpc.NSXLB
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, _ *v1alpha1.NetworkInfo, _ *servicecommon.VPCNetworkConfigInfo, _ vpc.LBProvider) (*model.Vpc, error) {
					assert.FailNow(t, "should not be called")
					return &model.Vpc{}, nil
				})
				return patches

			},
			args:    requestArgs,
			want:    common.ResultRequeueAfter60sec,
			wantErr: false,
		},
		{
			name: "NoneLbProviderReady",
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
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, _ string) (string, error) {
					return "non-system", nil

				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (servicecommon.VPCNetworkConfigInfo, bool) {
					return servicecommon.VPCNetworkConfigInfo{
						VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
						Org:                    "default",
						NSXProject:             "project-quality",
					}, true

				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ context.Context, _ *v1alpha1.VPCNetworkConfiguration) (bool, string, error) {
					return false, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateGatewayConnectionStatus", func(_ *vpc.VPCService, _ *servicecommon.VPCNetworkConfigInfo) (bool, string, error) {
					assert.FailNow(t, "should not be called")
					return true, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) vpc.LBProvider {
					return vpc.NSXLB
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, _ *v1alpha1.NetworkInfo, _ *servicecommon.VPCNetworkConfigInfo, _ vpc.LBProvider) (*model.Vpc, error) {
					assert.FailNow(t, "should not be called")
					return &model.Vpc{}, nil
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
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, _ string) (string, error) {
					return servicecommon.SystemVPCNetworkConfigurationName, nil

				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (servicecommon.VPCNetworkConfigInfo, bool) {
					return servicecommon.VPCNetworkConfigInfo{
						VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
						Org:                    "default",
						NSXProject:             "project-quality",
					}, true

				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ context.Context, _ *v1alpha1.VPCNetworkConfiguration) (bool, string, error) {
					return false, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateGatewayConnectionStatus", func(_ *vpc.VPCService, _ *servicecommon.VPCNetworkConfigInfo) (bool, string, error) {
					return true, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) vpc.LBProvider {
					return vpc.NSXLB
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, _ *v1alpha1.NetworkInfo, _ *servicecommon.VPCNetworkConfigInfo, _ vpc.LBProvider) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, _ string) (bool, error) {
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
				patches.ApplyFunc(updateSuccess,
					func(_ *NetworkInfoReconciler, _ context.Context, o *v1alpha1.NetworkInfo, _ client.Client, _ *v1alpha1.VPCState, _ string, _ string) {
					})
				patches.ApplyFunc(setVPCNetworkConfigurationStatusWithSnatEnabled,
					func(_ context.Context, _ client.Client, _ *v1alpha1.VPCNetworkConfiguration, autoSnatEnabled bool) {
						if !autoSnatEnabled {
							assert.FailNow(t, "should set VPCNetworkConfiguration status with AutoSnatEnabled=true")
						}
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
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, _ string) (string, error) {
					return servicecommon.SystemVPCNetworkConfigurationName, nil

				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (servicecommon.VPCNetworkConfigInfo, bool) {
					return servicecommon.VPCNetworkConfigInfo{
						VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
						Org:                    "default",
						NSXProject:             "project-quality",
					}, true

				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ context.Context, _ *v1alpha1.VPCNetworkConfiguration) (bool, string, error) {
					return false, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateGatewayConnectionStatus", func(_ *vpc.VPCService, _ *servicecommon.VPCNetworkConfigInfo) (bool, string, error) {
					return true, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) vpc.LBProvider {
					return vpc.NSXLB
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, _ *v1alpha1.NetworkInfo, _ *servicecommon.VPCNetworkConfigInfo, _ vpc.LBProvider) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, _ string) (bool, error) {
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
					return "snat-ip", nil

				})
				patches.ApplyFunc(updateSuccess,
					func(_ *NetworkInfoReconciler, _ context.Context, o *v1alpha1.NetworkInfo, _ client.Client, _ *v1alpha1.VPCState, _ string, _ string) {
					})
				patches.ApplyFunc(setVPCNetworkConfigurationStatusWithSnatEnabled,
					func(_ context.Context, _ client.Client, _ *v1alpha1.VPCNetworkConfiguration, autoSnatEnabled bool) {
						if autoSnatEnabled {
							assert.FailNow(t, "should set VPCNetworkConfiguration status with AutoSnatEnabled=false")
						}
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
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, _ string) (string, error) {
					return "non-system", nil

				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (servicecommon.VPCNetworkConfigInfo, bool) {
					return servicecommon.VPCNetworkConfigInfo{
						VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
						Org:                    "default",
						NSXProject:             "project-quality",
					}, true

				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ context.Context, _ *v1alpha1.VPCNetworkConfiguration) (bool, string, error) {
					return true, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateGatewayConnectionStatus", func(_ *vpc.VPCService, _ *servicecommon.VPCNetworkConfigInfo) (bool, string, error) {
					return true, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) vpc.LBProvider {
					return vpc.NSXLB
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, _ *v1alpha1.NetworkInfo, _ *servicecommon.VPCNetworkConfigInfo, _ vpc.LBProvider) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, _ string) (bool, error) {
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
				patches.ApplyFunc(updateSuccess,
					func(_ *NetworkInfoReconciler, _ context.Context, o *v1alpha1.NetworkInfo, _ client.Client, _ *v1alpha1.VPCState, _ string, _ string) {
					})
				patches.ApplyFunc(setVPCNetworkConfigurationStatusWithSnatEnabled,
					func(_ context.Context, _ client.Client, _ *v1alpha1.VPCNetworkConfiguration, autoSnatEnabled bool) {
						assert.FailNow(t, "should not be called")
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
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, _ string) (string, error) {
					return servicecommon.SystemVPCNetworkConfigurationName, nil

				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (servicecommon.VPCNetworkConfigInfo, bool) {
					return servicecommon.VPCNetworkConfigInfo{
						VPCConnectivityProfile: "/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default",
						Org:                    "default",
						NSXProject:             "project-quality",
					}, true

				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ context.Context, _ *v1alpha1.VPCNetworkConfiguration) (bool, string, error) {
					return false, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "ValidateGatewayConnectionStatus", func(_ *vpc.VPCService, _ *servicecommon.VPCNetworkConfigInfo) (bool, string, error) {
					return true, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) vpc.LBProvider {
					return vpc.NSXLB
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, _ *v1alpha1.NetworkInfo, _ *servicecommon.VPCNetworkConfigInfo, _ vpc.LBProvider) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName: servicecommon.String("vpc-name"),
						Path:        servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:          servicecommon.String("vpc-id"),
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, _ string) (bool, error) {
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
				patches.ApplyFunc(updateSuccess,
					func(_ *NetworkInfoReconciler, _ context.Context, o *v1alpha1.NetworkInfo, _ client.Client, _ *v1alpha1.VPCState, _ string, _ string) {
					})
				patches.ApplyFunc(setVPCNetworkConfigurationStatusWithSnatEnabled,
					func(_ context.Context, _ client.Client, _ *v1alpha1.VPCNetworkConfiguration, autoSnatEnabled bool) {
						if autoSnatEnabled {
							assert.FailNow(t, "should set VPCNetworkConfiguration status with AutoSnatEnabled=false")
						}
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
				patches = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, _ string) (string, error) {
					return "pre-vpc-nc", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCNetworkConfig", func(_ *vpc.VPCService, _ string) (servicecommon.VPCNetworkConfigInfo, bool) {
					return servicecommon.VPCNetworkConfigInfo{
						Org:        "default",
						NSXProject: "project-quality",
						VPCPath:    "/orgs/default/projects/nsx_operator_e2e_test/vpcs/pre-vpc",
					}, true

				})
				patches.ApplyFunc(getGatewayConnectionStatus, func(_ context.Context, _ *v1alpha1.VPCNetworkConfiguration) (bool, string, error) {
					return true, "", nil
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) vpc.LBProvider {
					return vpc.AVILB
				})
				patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, _ *v1alpha1.NetworkInfo, _ *servicecommon.VPCNetworkConfigInfo, _ vpc.LBProvider) (*model.Vpc, error) {
					return &model.Vpc{
						DisplayName:            servicecommon.String("vpc-name"),
						Path:                   servicecommon.String("/orgs/default/projects/project-quality/vpcs/fake-vpc"),
						Id:                     servicecommon.String("vpc-id"),
						VpcConnectivityProfile: servicecommon.String("/orgs/default/projects/nsx_operator_e2e_test/vpc-connectivity-profiles/default"),
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
				patches.ApplyFunc(updateSuccess,
					func(_ *NetworkInfoReconciler, _ context.Context, o *v1alpha1.NetworkInfo, _ client.Client, _ *v1alpha1.VPCState, _ string, _ string) {
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
			r := createNetworkInfoReconciler()
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
	r := createNetworkInfoReconciler()

	ctx := context.TODO()
	namespace := "test-ns"

	t.Run("shared namespace, skip deletion", func(t *testing.T) {
		patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, _ string) (bool, error) {
			return true, nil
		})
		defer patches.Reset()

		err := r.deleteVPCsByName(ctx, namespace)
		require.NoError(t, err)
	})

	t.Run("non-shared namespace, no VPCs found", func(t *testing.T) {
		patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, _ string) (bool, error) {
			return false, nil
		})
		patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCsByNamespace", func(_ *vpc.VPCService, _ string) []*model.Vpc {
			return nil
		})
		defer patches.Reset()

		err := r.deleteVPCsByName(ctx, namespace)
		require.NoError(t, err)
	})

	t.Run("failed to delete VPC", func(t *testing.T) {
		patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, _ string) (bool, error) {
			return false, nil
		})
		patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCsByNamespace", func(_ *vpc.VPCService, _ string) []*model.Vpc {
			vpcPath := "/vpc/1"
			return []*model.Vpc{{Path: &vpcPath}}
		})
		patches.ApplyMethod(reflect.TypeOf(r.Service), "DeleteVPC", func(_ *vpc.VPCService, _ string) error {
			return fmt.Errorf("delete failed")
		})
		defer patches.Reset()

		err := r.deleteVPCsByName(ctx, namespace)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "delete failed")
	})

	t.Run("successful deletion of VPCs", func(t *testing.T) {
		patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "IsSharedVPCNamespaceByNS", func(_ *vpc.VPCService, _ string) (bool, error) {
			return false, nil
		})
		patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVPCsByNamespace", func(_ *vpc.VPCService, _ string) []*model.Vpc {
			vpcPath1 := "/vpc/1"
			vpcPath2 := "/vpc/2"
			return []*model.Vpc{{Path: &vpcPath1}, {Path: &vpcPath2}}
		})
		patches.ApplyMethod(reflect.TypeOf(r.Service), "DeleteVPC", func(_ *vpc.VPCService, _ string) error {
			return nil
		})
		patches.ApplyMethod(reflect.TypeOf(r.Service), "ListVPC", func(_ *vpc.VPCService) []model.Vpc {
			return nil
		})
		patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkconfigNameFromNS", func(_ *vpc.VPCService, _ string) (string, error) {
			return "", nil
		})
		patches.ApplyFunc(deleteVPCNetworkConfigurationStatus, func(ctx context.Context, client client.Client, ncName string, staleVPCs []*model.Vpc, aliveVPCs []model.Vpc) {
			return
		})
		defer patches.Reset()

		err := r.deleteVPCsByName(ctx, namespace)
		require.NoError(t, err)
	})
}

func TestNetworkInfoReconciler_CollectGarbage(t *testing.T) {
	r := createNetworkInfoReconciler()

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
	stopSig := "stop"
	getQueuedReqs := func(queue workqueue.RateLimitingInterface) []reconcile.Request {
		var requests []reconcile.Request
		for {
			obj, shutdown := queue.Get()
			if shutdown {
				return requests
			}
			if val, ok := obj.(string); ok && val == stopSig {
				return requests
			}
			req, _ := obj.(reconcile.Request)
			requests = append(requests, req)
		}
	}

	r := createNetworkInfoReconciler()
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	r.Client = k8sClient
	r.queue = workqueue.NewRateLimitingQueueWithConfig(workqueue.DefaultControllerRateLimiter(),
		workqueue.RateLimitingQueueConfig{
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

	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "ListAllVPCsFromNSX", func(_ *vpc.VPCService) map[string]model.Vpc {
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
	patches.ApplyMethod(reflect.TypeOf(r.Service), "ListNamespacesWithPreCreatedVPCs", func(_ *vpc.VPCService) map[string]string {
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
