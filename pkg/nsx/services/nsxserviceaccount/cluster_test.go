/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsxserviceaccount

import (
	"context"
	"fmt"
	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	nsxvmwarecomv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

type fakeQueryClient struct{}

func (c *fakeQueryClient) List(queryParam string, cursorParam *string, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.SearchResponse, error) {
	return model.SearchResponse{}, nil
}

type fakeClusterControlPlanesClient struct{}

func (c *fakeClusterControlPlanesClient) Delete(siteIdParam string, enforcementpointIdParam string, clusterControlPlaneIdParam string, cascadeParam *bool) error {
	return nil
}

func (c *fakeClusterControlPlanesClient) Get(siteIdParam string, enforcementpointIdParam string, clusterControlPlaneIdParam string) (model.ClusterControlPlane, error) {
	return model.ClusterControlPlane{}, nil
}

func (c *fakeClusterControlPlanesClient) List(siteIdParam string, enforcementpointIdParam string, cursorParam *string, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.ClusterControlPlaneListResult, error) {
	return model.ClusterControlPlaneListResult{}, nil
}

func (c *fakeClusterControlPlanesClient) Update(siteIdParam string, enforcementpointIdParam string, clusterControlPlaneIdParam string, clusterControlPlaneParam model.ClusterControlPlane) (model.ClusterControlPlane, error) {
	return model.ClusterControlPlane{}, nil
}

type fakeMPQueryClient struct{}

func (c *fakeMPQueryClient) List(queryParam string, cursorParam *string, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (mpmodel.SearchResponse, error) {
	return mpmodel.SearchResponse{}, nil
}

type fakeWithCertificateClient struct{}

func (c *fakeWithCertificateClient) Create(principalIdentityWithCertificateParam mpmodel.PrincipalIdentityWithCertificate) (mpmodel.PrincipalIdentity, error) {
	return mpmodel.PrincipalIdentity{}, nil
}

type fakePrincipalIdentitiesClient struct{}

func (c *fakePrincipalIdentitiesClient) Create(principalIdentityParam mpmodel.PrincipalIdentity) (mpmodel.PrincipalIdentity, error) {
	return mpmodel.PrincipalIdentity{}, nil
}

func (c *fakePrincipalIdentitiesClient) Delete(principalIdentityIdParam string) error {
	return nil
}

func (c *fakePrincipalIdentitiesClient) Get(principalIdentityIdParam string) (mpmodel.PrincipalIdentity, error) {
	return mpmodel.PrincipalIdentity{}, nil
}

func (c *fakePrincipalIdentitiesClient) List() (mpmodel.PrincipalIdentityList, error) {
	return mpmodel.PrincipalIdentityList{}, nil
}

func (c *fakePrincipalIdentitiesClient) Updatecertificate(updatePrincipalIdentityCertificateRequestParam mpmodel.UpdatePrincipalIdentityCertificateRequest) (mpmodel.PrincipalIdentity, error) {
	return mpmodel.PrincipalIdentity{}, nil
}

type fakeCertificatesClient struct{}

func (c *fakeCertificatesClient) Applycertificate(certIdParam string, serviceTypeParam string, nodeIdParam *string) error {
	return nil
}

func (c *fakeCertificatesClient) Delete(certIdParam string) error {
	return nil
}

func (c *fakeCertificatesClient) Fetchpeercertificatechain(tlsServiceEndpointParam mpmodel.TlsServiceEndpoint) (mpmodel.PeerCertificateChain, error) {
	return mpmodel.PeerCertificateChain{}, nil
}

func (c *fakeCertificatesClient) Get(certIdParam string, detailsParam *bool) (mpmodel.Certificate, error) {
	return mpmodel.Certificate{}, nil
}

func (c *fakeCertificatesClient) Importcertificate(trustObjectDataParam mpmodel.TrustObjectData) (mpmodel.CertificateList, error) {
	return mpmodel.CertificateList{}, nil
}

func (c *fakeCertificatesClient) Importtrustedca(aliasParam string, trustObjectDataParam mpmodel.TrustObjectData) error {
	return nil
}

func (c *fakeCertificatesClient) List(cursorParam *string, detailsParam *bool, includedFieldsParam *string, nodeIdParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string, type_Param *string) (mpmodel.CertificateList, error) {
	return mpmodel.CertificateList{}, nil
}

func (c *fakeCertificatesClient) Setapplianceproxycertificateforintersitecommunication(setInterSiteAphCertificateRequestParam mpmodel.SetInterSiteAphCertificateRequest) error {
	return nil
}

func (c *fakeCertificatesClient) Setpicertificateforfederation(setPrincipalIdentityCertificateForFederationRequestParam mpmodel.SetPrincipalIdentityCertificateForFederationRequest) error {
	return nil
}

func (c *fakeCertificatesClient) Validate(certIdParam string, usageParam *string) (mpmodel.CertificateCheckingStatus, error) {
	return mpmodel.CertificateCheckingStatus{}, nil
}

func newFakeCommonService() common.Service {
	client := fake.NewClientBuilder().Build()
	scheme := client.Scheme()
	clientgoscheme.AddToScheme(scheme)
	nsxvmwarecomv1alpha1.AddToScheme(scheme)
	service := common.Service{
		Client: client,
		NSXClient: &nsx.Client{
			NsxConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "k8scl-one:test",
				},
				NsxConfig: &config.NsxConfig{
					NsxApiManagers: []string{"mgr1:443", "mgr2:443"},
				},
			},
			RestConnector:              nil,
			QueryClient:                &fakeQueryClient{},
			GroupClient:                nil,
			SecurityClient:             nil,
			RuleClient:                 nil,
			InfraClient:                nil,
			ClusterControlPlanesClient: &fakeClusterControlPlanesClient{},
			MPQueryClient:              &fakeMPQueryClient{},
			CertificatesClient:         &fakeCertificatesClient{},
			PrincipalIdentitiesClient:  &fakePrincipalIdentitiesClient{},
			WithCertificateClient:      &fakeWithCertificateClient{},
			NSXChecker:                 nsx.NSXHealthChecker{},
			NSXVerChecker:              nsx.NSXVersionChecker{},
		},
		NSXConfig: &config.NSXOperatorConfig{
			CoeConfig: &config.CoeConfig{
				Cluster: "k8scl-one:test",
			},
			NsxConfig: &config.NsxConfig{
				NsxApiManagers: []string{"mgr1:443", "mgr2:443"},
			},
		},
	}
	return service
}

func TestInitializeNSXServiceAccount(t *testing.T) {
	tests := []struct {
		name        string
		prepareFunc func(*testing.T, *common.Service, context.Context) *gomonkey.Patches
		wantErr     bool
	}{
		{
			name: "error",
			prepareFunc: func(t *testing.T, s *common.Service, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.QueryClient, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.SearchResponse{}, fmt.Errorf("mock error")},
					Times:  2,
				}})
				return patches
			},
			wantErr: true,
		},
		{
			name: "success",
			prepareFunc: func(t *testing.T, s *common.Service, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.QueryClient, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.SearchResponse{}, nil},
					Times:  2,
				}})
				return patches
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()
			commonService := newFakeCommonService()
			patches := tt.prepareFunc(t, &commonService, ctx)
			defer patches.Reset()
			got, err := InitializeNSXServiceAccount(commonService)
			if (err != nil) != tt.wantErr {
				t.Errorf("InitializeNSXServiceAccount() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got.Service, commonService) {
				t.Errorf("InitializeNSXServiceAccount() got = %v, want %v", got.Service, commonService)
			}
		})
	}
}

func TestNSXServiceAccountService_CreateOrUpdateNSXServiceAccount(t *testing.T) {
	type args struct {
		obj *v1alpha1.NSXServiceAccount
	}
	tests := []struct {
		name        string
		prepareFunc func(*testing.T, *NSXServiceAccountService, context.Context, *nsxvmwarecomv1alpha1.NSXServiceAccount) *gomonkey.Patches
		args        args
		wantErr     bool
		wantSecret  bool
		expectedCR  *nsxvmwarecomv1alpha1.NSXServiceAccount
	}{
		{
			name: "GenerateCertificateError",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *nsxvmwarecomv1alpha1.NSXServiceAccount) *gomonkey.Patches {
				patches := gomonkey.ApplyFuncSeq(util.GenerateCertificate, []gomonkey.OutputCell{{
					Values: gomonkey.Params{"", "", fmt.Errorf("mock error")},
					Times:  1,
				}})
				return patches
			},
			args: args{
				obj: &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
				},
			},
			wantErr:    true,
			wantSecret: false,
			expectedCR: nil,
		},
		{
			name: "Success",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *nsxvmwarecomv1alpha1.NSXServiceAccount) *gomonkey.Patches {
				assert.NoError(t, s.Client.Create(ctx, obj))
				normalizedClusterName := "k8scl-one_test-ns1-name1"
				vpcPath := "/orgs/default/projects/k8scl-one:test/vpcs/vpc1"
				piId := "Id1"
				uid := "00000000-0000-0000-0000-000000000001"
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.WithCertificateClient, "Create", []gomonkey.OutputCell{{
					Values: gomonkey.Params{mpmodel.PrincipalIdentity{
						IsProtected: &isProtectedTrue,
						Name:        &normalizedClusterName,
						NodeId:      &normalizedClusterName,
						Role:        nil,
						RolesForPaths: []mpmodel.RolesForPath{{
							Path: &readerPath,
							Roles: []mpmodel.Role{{
								Role: &readerRole,
							}},
						}, {
							Path: &vpcPath,
							Roles: []mpmodel.Role{{
								Role: &vpcRole,
							}},
						}},
						Id: &piId,
						Tags: []mpmodel.Tag{{
							Scope: &tagScopeCluster,
							Tag:   &s.NSXConfig.CoeConfig.Cluster,
						}, {
							Scope: &tagScopeNamespace,
							Tag:   &obj.Namespace,
						}, {
							Scope: &tagScopeNSXServiceAccountCRName,
							Tag:   &obj.Name,
						}, {
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid,
						}},
					}, nil},
					Times: 1,
				}})
				nodeId := "clusterId1"
				patches.ApplyMethodSeq(s.NSXClient.ClusterControlPlanesClient, "Update", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.ClusterControlPlane{
						Id:           &normalizedClusterName,
						NodeId:       &nodeId,
						Revision:     &revision1,
						ResourceType: &antreaClusterResourceType,
						Certificate:  nil,
						VhcPath:      &vpcPath,
						Tags: []model.Tag{{
							Scope: &tagScopeCluster,
							Tag:   &s.NSXConfig.CoeConfig.Cluster,
						}, {
							Scope: &tagScopeNamespace,
							Tag:   &obj.Namespace,
						}, {
							Scope: &tagScopeNSXServiceAccountCRName,
							Tag:   &obj.Name,
						}, {
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid,
						}},
					}, nil},
					Times: 1,
				}})
				return patches
			},
			args: args{
				obj: &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: nsxvmwarecomv1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
				},
			},
			wantErr:    false,
			wantSecret: true,
			expectedCR: &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "name1",
					Namespace:       "ns1",
					UID:             "00000000-0000-0000-0000-000000000001",
					ResourceVersion: "2",
				},
				Spec: nsxvmwarecomv1alpha1.NSXServiceAccountSpec{
					VPCName: "vpc1",
				},
				Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
					Phase:  "realized",
					Reason: "Success",
					Conditions: []metav1.Condition{
						{
							Type:    nsxvmwarecomv1alpha1.ConditionTypeRealized,
							Status:  metav1.ConditionTrue,
							Reason:  nsxvmwarecomv1alpha1.ConditionReasonRealizationSuccess,
							Message: "Success.",
						},
					},
					VPCPath:        "/orgs/default/projects/k8scl-one_test/vpcs/ns1-default-vpc",
					NSXManagers:    []string{"mgr1:443", "mgr2:443"},
					ProxyEndpoints: v1alpha1.NSXProxyEndpoint{},
					ClusterID:      "clusterId1",
					ClusterName:    "k8scl-one_test-ns1-name1",
					Secrets:        []v1alpha1.NSXSecret{{Name: "name1-nsx-cert", Namespace: "ns1"}},
				},
			},
		},
		{
			name: "LongNameSuccess",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *nsxvmwarecomv1alpha1.NSXServiceAccount) *gomonkey.Patches {
				assert.NoError(t, s.Client.Create(ctx, obj))
				s.NSXConfig.CoeConfig.Cluster = "k8scl-one:1234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890"
				normalizedClusterName := "k8scl-one_12345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456-1a6417ee"
				vpcPath := "/orgs/default/projects/k8scl-one:test/vpcs/ns1-default-vpc"
				piId := "Id1"
				uid := "00000000-0000-0000-0000-000000000001"
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.WithCertificateClient, "Create", []gomonkey.OutputCell{{
					Values: gomonkey.Params{mpmodel.PrincipalIdentity{
						IsProtected: &isProtectedTrue,
						Name:        &normalizedClusterName,
						NodeId:      &normalizedClusterName,
						Role:        nil,
						RolesForPaths: []mpmodel.RolesForPath{{
							Path: &readerPath,
							Roles: []mpmodel.Role{{
								Role: &readerRole,
							}},
						}, {
							Path: &vpcPath,
							Roles: []mpmodel.Role{{
								Role: &vpcRole,
							}},
						}},
						Id: &piId,
						Tags: []mpmodel.Tag{{
							Scope: &tagScopeCluster,
							Tag:   &s.NSXConfig.CoeConfig.Cluster,
						}, {
							Scope: &tagScopeNamespace,
							Tag:   &obj.Namespace,
						}, {
							Scope: &tagScopeNSXServiceAccountCRName,
							Tag:   &obj.Name,
						}, {
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid,
						}},
					}, nil},
					Times: 1,
				}})
				nodeId := "clusterId1"
				patches.ApplyMethodSeq(s.NSXClient.ClusterControlPlanesClient, "Update", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.ClusterControlPlane{
						Id:           &normalizedClusterName,
						NodeId:       &nodeId,
						Revision:     &revision1,
						ResourceType: &antreaClusterResourceType,
						Certificate:  nil,
						VhcPath:      &vpcPath,
						Tags: []model.Tag{{
							Scope: &tagScopeCluster,
							Tag:   &s.NSXConfig.CoeConfig.Cluster,
						}, {
							Scope: &tagScopeNamespace,
							Tag:   &obj.Namespace,
						}, {
							Scope: &tagScopeNSXServiceAccountCRName,
							Tag:   &obj.Name,
						}, {
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid,
						}},
					}, nil},
					Times: 1,
				}})
				return patches
			},
			args: args{
				obj: &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
				},
			},
			wantErr:    false,
			wantSecret: true,
			expectedCR: &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "name1",
					Namespace:       "ns1",
					UID:             "00000000-0000-0000-0000-000000000001",
					ResourceVersion: "2",
				},
				Spec: nsxvmwarecomv1alpha1.NSXServiceAccountSpec{},
				Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
					Phase:  "realized",
					Reason: "Success",
					Conditions: []metav1.Condition{
						{
							Type:    nsxvmwarecomv1alpha1.ConditionTypeRealized,
							Status:  metav1.ConditionTrue,
							Reason:  nsxvmwarecomv1alpha1.ConditionReasonRealizationSuccess,
							Message: "Success.",
						},
					},
					VPCPath:        "/orgs/default/projects/k8scl-one_12345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456-e8ad9afc/vpcs/ns1-default-vpc",
					NSXManagers:    []string{"mgr1:443", "mgr2:443"},
					ProxyEndpoints: v1alpha1.NSXProxyEndpoint{},
					ClusterID:      "clusterId1",
					ClusterName:    "k8scl-one_12345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456-1a6417ee",
					Secrets:        []v1alpha1.NSXSecret{{Name: "name1-nsx-cert", Namespace: "ns1"}},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()
			commonService := newFakeCommonService()
			s := &NSXServiceAccountService{Service: commonService}
			s.SetUpStore()
			patches := tt.prepareFunc(t, s, ctx, tt.args.obj)
			defer patches.Reset()

			if err := s.CreateOrUpdateNSXServiceAccount(ctx, tt.args.obj); (err != nil) != tt.wantErr {
				t.Errorf("CreateOrUpdateNSXServiceAccount() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantSecret {
				secret := &v1.Secret{}
				assert.NoError(t, s.Client.Get(ctx, types.NamespacedName{
					Namespace: tt.args.obj.Namespace,
					Name:      tt.args.obj.Name + SecretSuffix,
				}, secret))
				assert.Equal(t, 2, len(secret.Data))
			}
			actualCR := &nsxvmwarecomv1alpha1.NSXServiceAccount{}
			err := s.Client.Get(ctx, types.NamespacedName{
				Namespace: tt.args.obj.Namespace,
				Name:      tt.args.obj.Name,
			}, actualCR)
			if tt.expectedCR == nil {
				assert.True(t, errors.IsNotFound(err))
			} else {
				assert.Equal(t, tt.expectedCR.ObjectMeta, actualCR.ObjectMeta)
				assert.Equal(t, tt.expectedCR.Spec, actualCR.Spec)
				for i := range actualCR.Status.Conditions {
					actualCR.Status.Conditions[i].LastTransitionTime = metav1.Time{}
				}
				assert.Equal(t, tt.expectedCR.Status, actualCR.Status)
			}
			if !tt.wantErr {
				expectedKeys := []string{util.NormalizeId(s.getClusterName(tt.expectedCR.Namespace, tt.expectedCR.Name))}
				assert.Equal(t, expectedKeys, s.PrincipalIdentityStore.ListKeys())
				assert.Equal(t, expectedKeys, s.ClusterControlPlaneStore.ListKeys())
			}
		})
	}
}

func TestNSXServiceAccountService_UpdateRealizedNSXServiceAccount(t *testing.T) {
	type args struct {
		obj *v1alpha1.NSXServiceAccount
	}
	tests := []struct {
		name        string
		prepareFunc func(*testing.T, *NSXServiceAccountService, context.Context, *nsxvmwarecomv1alpha1.NSXServiceAccount) *gomonkey.Patches
		args        args
		wantErr     bool
	}{
		{
			name: "Skip",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *nsxvmwarecomv1alpha1.NSXServiceAccount) *gomonkey.Patches {
				normalizedClusterName := "k8scl-one_test-ns1-name1"
				vpcPath := "/orgs/default/projects/k8scl-one:test/vpcs/vpc1"
				piId := "Id1"
				uid := "00000000-0000-0000-0000-000000000001"
				s.PrincipalIdentityStore.Add(mpmodel.PrincipalIdentity{
					IsProtected: &isProtectedTrue,
					Name:        &normalizedClusterName,
					NodeId:      &normalizedClusterName,
					Role:        nil,
					RolesForPaths: []mpmodel.RolesForPath{{
						Path: &readerPath,
						Roles: []mpmodel.Role{{
							Role: &readerRole,
						}},
					}, {
						Path: &vpcPath,
						Roles: []mpmodel.Role{{
							Role: &vpcRole,
						}},
					}},
					Id: &piId,
					Tags: []mpmodel.Tag{{
						Scope: &tagScopeCluster,
						Tag:   &s.NSXConfig.CoeConfig.Cluster,
					}, {
						Scope: &tagScopeNamespace,
						Tag:   &obj.Namespace,
					}, {
						Scope: &tagScopeNSXServiceAccountCRName,
						Tag:   &obj.Name,
					}, {
						Scope: &tagScopeNSXServiceAccountCRUID,
						Tag:   &uid,
					}},
				})
				nodeId := "clusterId1"
				s.ClusterControlPlaneStore.Add(model.ClusterControlPlane{
					Id:           &normalizedClusterName,
					NodeId:       &nodeId,
					Revision:     &revision1,
					ResourceType: &antreaClusterResourceType,
					Certificate:  nil,
					VhcPath:      &vpcPath,
					Tags: []model.Tag{{
						Scope: &tagScopeCluster,
						Tag:   &s.NSXConfig.CoeConfig.Cluster,
					}, {
						Scope: &tagScopeNamespace,
						Tag:   &obj.Namespace,
					}, {
						Scope: &tagScopeNSXServiceAccountCRName,
						Tag:   &obj.Name,
					}, {
						Scope: &tagScopeNSXServiceAccountCRUID,
						Tag:   &uid,
					}},
				})
				return nil
			},
			args: args{
				obj: &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: nsxvmwarecomv1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						Phase:       nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []nsxvmwarecomv1alpha1.NSXSecret{{
							Name:      "name1" + SecretSuffix,
							Namespace: "ns1",
						}},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "CacheNotMatch",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *nsxvmwarecomv1alpha1.NSXServiceAccount) *gomonkey.Patches {
				normalizedClusterName := "k8scl-one_test-ns1-name1"
				vpcPath := "/orgs/default/projects/k8scl-one:test/vpcs/vpc1"
				piId := "Id1"
				uid := "00000000-0000-0000-0000-000000000001"
				s.PrincipalIdentityStore.Add(mpmodel.PrincipalIdentity{
					IsProtected: &isProtectedTrue,
					Name:        &normalizedClusterName,
					NodeId:      &normalizedClusterName,
					Role:        nil,
					RolesForPaths: []mpmodel.RolesForPath{{
						Path: &readerPath,
						Roles: []mpmodel.Role{{
							Role: &readerRole,
						}},
					}, {
						Path: &vpcPath,
						Roles: []mpmodel.Role{{
							Role: &vpcRole,
						}},
					}},
					Id: &piId,
					Tags: []mpmodel.Tag{{
						Scope: &tagScopeCluster,
						Tag:   &s.NSXConfig.CoeConfig.Cluster,
					}, {
						Scope: &tagScopeNamespace,
						Tag:   &obj.Namespace,
					}, {
						Scope: &tagScopeNSXServiceAccountCRName,
						Tag:   &obj.Name,
					}, {
						Scope: &tagScopeNSXServiceAccountCRUID,
						Tag:   &uid,
					}},
				})
				return nil
			},
			args: args{
				obj: &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: nsxvmwarecomv1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						Phase:       nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []nsxvmwarecomv1alpha1.NSXSecret{{
							Name:      "name1" + SecretSuffix,
							Namespace: "ns1",
						}},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "CacheNotSync",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *nsxvmwarecomv1alpha1.NSXServiceAccount) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.ClusterControlPlanesClient, "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.ClusterControlPlane{}, nil},
					Times:  1,
				}})
				return patches
			},
			args: args{
				obj: &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: nsxvmwarecomv1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						Phase:       nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []nsxvmwarecomv1alpha1.NSXSecret{{
							Name:      "name1" + SecretSuffix,
							Namespace: "ns1",
						}},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "GetCCPError",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *nsxvmwarecomv1alpha1.NSXServiceAccount) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.ClusterControlPlanesClient, "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.ClusterControlPlane{}, fmt.Errorf("mock error")},
					Times:  1,
				}})
				return patches
			},
			args: args{
				obj: &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: nsxvmwarecomv1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						Phase:       nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []nsxvmwarecomv1alpha1.NSXSecret{{
							Name:      "name1" + SecretSuffix,
							Namespace: "ns1",
						}},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "GetSecretError",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *nsxvmwarecomv1alpha1.NSXServiceAccount) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.ClusterControlPlanesClient, "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.ClusterControlPlane{}, vapierrors.NotFound{}},
					Times:  1,
				}})
				return patches
			},
			args: args{
				obj: &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: nsxvmwarecomv1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						Phase:       nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []nsxvmwarecomv1alpha1.NSXSecret{{
							Name:      "name1" + SecretSuffix,
							Namespace: "ns1",
						}},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "Success",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *nsxvmwarecomv1alpha1.NSXServiceAccount) *gomonkey.Patches {
				normalizedClusterName := "k8scl-one_test-ns1-name1"
				vpcPath := "/orgs/default/projects/k8scl-one:test/vpcs/vpc1"
				piId := "Id1"
				uid := "00000000-0000-0000-0000-000000000001"
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.ClusterControlPlanesClient, "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.ClusterControlPlane{}, vapierrors.NotFound{}},
					Times:  1,
				}})
				secretName := obj.Status.Secrets[0].Name
				secretNamespace := obj.Status.Secrets[0].Namespace
				cert := "fakeCert"
				key := "fakeKey"
				assert.NoError(t, s.Client.Create(ctx, &v1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:        secretName,
						Namespace:   secretNamespace,
						Labels:      nil,
						Annotations: nil,
						OwnerReferences: []metav1.OwnerReference{{
							APIVersion:         obj.APIVersion,
							Kind:               obj.Kind,
							Name:               obj.Name,
							UID:                obj.UID,
							Controller:         nil,
							BlockOwnerDeletion: nil,
						}},
						Finalizers: nil,
					},
					Immutable: nil,
					Data:      map[string][]byte{SecretCertName: []byte(cert), SecretKeyName: []byte(key)},
					Type:      "",
				}))

				patches.ApplyMethodSeq(s.NSXClient.WithCertificateClient, "Create", []gomonkey.OutputCell{{
					Values: gomonkey.Params{mpmodel.PrincipalIdentity{
						IsProtected: &isProtectedTrue,
						Name:        &normalizedClusterName,
						NodeId:      &normalizedClusterName,
						Role:        nil,
						RolesForPaths: []mpmodel.RolesForPath{{
							Path: &readerPath,
							Roles: []mpmodel.Role{{
								Role: &readerRole,
							}},
						}, {
							Path: &vpcPath,
							Roles: []mpmodel.Role{{
								Role: &vpcRole,
							}},
						}},
						Id: &piId,
						Tags: []mpmodel.Tag{{
							Scope: &tagScopeCluster,
							Tag:   &s.NSXConfig.CoeConfig.Cluster,
						}, {
							Scope: &tagScopeNamespace,
							Tag:   &obj.Namespace,
						}, {
							Scope: &tagScopeNSXServiceAccountCRName,
							Tag:   &obj.Name,
						}, {
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid,
						}},
					}, nil},
					Times: 1,
				}})
				nodeId := "clusterId1"
				patches.ApplyMethodSeq(s.NSXClient.ClusterControlPlanesClient, "Update", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.ClusterControlPlane{
						Id:           &normalizedClusterName,
						NodeId:       &nodeId,
						Revision:     &revision1,
						ResourceType: &antreaClusterResourceType,
						Certificate:  nil,
						VhcPath:      &vpcPath,
						Tags: []model.Tag{{
							Scope: &tagScopeCluster,
							Tag:   &s.NSXConfig.CoeConfig.Cluster,
						}, {
							Scope: &tagScopeNamespace,
							Tag:   &obj.Namespace,
						}, {
							Scope: &tagScopeNSXServiceAccountCRName,
							Tag:   &obj.Name,
						}, {
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid,
						}},
					}, nil},
					Times: 1,
				}})
				return patches
			},
			args: args{
				obj: &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: nsxvmwarecomv1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						Phase:       nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []nsxvmwarecomv1alpha1.NSXSecret{{
							Name:      "name1" + SecretSuffix,
							Namespace: "ns1",
						}},
					},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()
			commonService := newFakeCommonService()
			s := &NSXServiceAccountService{Service: commonService}
			s.SetUpStore()
			patches := tt.prepareFunc(t, s, ctx, tt.args.obj)
			if patches != nil {
				defer patches.Reset()
			}

			if err := s.UpdateRealizedNSXServiceAccount(ctx, tt.args.obj); (err != nil) != tt.wantErr {
				t.Errorf("UpdateRealizedNSXServiceAccount() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNSXServiceAccountService_DeleteNSXServiceAccount(t *testing.T) {
	uidScope := common.TagScopeNSXServiceAccountCRUID
	uidTag := "uid1"

	type args struct {
		namespacedName types.NamespacedName
		uid            types.UID
	}
	tests := []struct {
		name                              string
		prepareFunc                       func(*testing.T, *NSXServiceAccountService, context.Context) *gomonkey.Patches
		args                              args
		wantErr                           bool
		wantClusterControlPlaneStoreCount int
		wantPrincipalIdentityStoreCount   int
	}{
		{
			name: "success",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context) *gomonkey.Patches {
				normalizedClusterName := "k8scl-one_test-ns1-name1"
				piId := "piId1"
				certId := "certId1"
				assert.NoError(t, s.ClusterControlPlaneStore.Add(model.ClusterControlPlane{Id: &normalizedClusterName}))
				assert.NoError(t, s.PrincipalIdentityStore.Add(mpmodel.PrincipalIdentity{Name: &normalizedClusterName, Id: &piId, CertificateId: &certId}))
				assert.NoError(t, s.Client.Create(ctx, &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "uid1",
					},
				}))
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.ClusterControlPlanesClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				patches.ApplyMethodSeq(s.NSXClient.PrincipalIdentitiesClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				patches.ApplyMethodSeq(s.NSXClient.CertificatesClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
			args: args{
				namespacedName: types.NamespacedName{
					Namespace: "ns1",
					Name:      "name1",
				},
				uid: "uid1",
			},
			wantErr:                           false,
			wantClusterControlPlaneStoreCount: 0,
			wantPrincipalIdentityStoreCount:   0,
		},
		{
			name: "errorDeletePIDifferentUID",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context) *gomonkey.Patches {
				normalizedClusterName := "k8scl-one_test-ns1-name1"
				piId := "piId1"
				certId := "certId1"
				assert.NoError(t, s.ClusterControlPlaneStore.Add(model.ClusterControlPlane{Id: &normalizedClusterName}))
				assert.NoError(t, s.PrincipalIdentityStore.Add(mpmodel.PrincipalIdentity{Name: &normalizedClusterName, Id: &piId, CertificateId: &certId, Tags: []mpmodel.Tag{{
					Scope: &uidScope,
					Tag:   &uidTag,
				}}}))
				assert.NoError(t, s.Client.Create(ctx, &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "uid2",
					},
				}))

				patches := gomonkey.ApplyMethodSeq(s.NSXClient.PrincipalIdentitiesClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("mock error")},
					Times:  1,
				}})
				return patches
			},
			args: args{
				namespacedName: types.NamespacedName{
					Namespace: "ns1",
					Name:      "name1",
				},
				uid: "uid1",
			},
			wantErr:                           true,
			wantClusterControlPlaneStoreCount: 1,
			wantPrincipalIdentityStoreCount:   1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()
			commonService := newFakeCommonService()
			s := &NSXServiceAccountService{Service: commonService}
			s.SetUpStore()
			patches := tt.prepareFunc(t, s, ctx)
			defer patches.Reset()

			if err := s.DeleteNSXServiceAccount(ctx, tt.args.namespacedName, tt.args.uid); (err != nil) != tt.wantErr {
				t.Errorf("DeleteNSXServiceAccount() error = %v, wantErr %v", err, tt.wantErr)
			}
			assert.Equal(t, tt.wantClusterControlPlaneStoreCount, len(s.ClusterControlPlaneStore.ListKeys()))
			assert.Equal(t, tt.wantPrincipalIdentityStoreCount, len(s.PrincipalIdentityStore.ListKeys()))
		})
	}
}

func TestNSXServiceAccountService_ListNSXServiceAccountRealization(t *testing.T) {
	tests := []struct {
		name    string
		piKeys  []string
		ccpKeys []string
		want    sets.String
	}{
		{
			name:    "standard",
			piKeys:  []string{"ns1-name1", "ns2-name2"},
			ccpKeys: []string{"ns2-name2", "ns3-name3"},
			want:    sets.NewString("ns1-name1-uid", "ns2-name2-uid", "ns3-name3-uid"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commonService := newFakeCommonService()
			s := &NSXServiceAccountService{Service: commonService}
			s.SetUpStore()
			for _, piKey := range tt.piKeys {
				piName := piKey
				piId := piKey + "-id"
				crUID := piKey + "-uid"
				assert.NoError(t, s.PrincipalIdentityStore.Add(mpmodel.PrincipalIdentity{
					Id:   &piId,
					Name: &piName,
					Tags: []mpmodel.Tag{{
						Scope: &tagScopeNSXServiceAccountCRUID,
						Tag:   &crUID,
					}},
				}))
			}
			for _, ccpKey := range tt.ccpKeys {
				ccpId := ccpKey
				crUID := ccpKey + "-uid"
				assert.NoError(t, s.ClusterControlPlaneStore.Add(model.ClusterControlPlane{
					Id: &ccpId,
					Tags: []model.Tag{{
						Scope: &tagScopeNSXServiceAccountCRUID,
						Tag:   &crUID,
					}},
				}))
			}

			if got := s.ListNSXServiceAccountRealization(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ListNSXServiceAccountRealization() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNSXServiceAccountService_GetNSXServiceAccountNameByUID(t *testing.T) {
	type args struct {
		uid string
	}
	tests := []struct {
		name               string
		piKeys             []types.NamespacedName
		ccpKeys            []types.NamespacedName
		args               args
		wantNamespacedName types.NamespacedName
	}{
		{
			name: "ByPI",
			piKeys: []types.NamespacedName{{
				Namespace: "name1",
				Name:      "ns1",
			}},
			ccpKeys: []types.NamespacedName{},
			args: args{
				uid: "name1-ns1-uid",
			},
			wantNamespacedName: types.NamespacedName{
				Namespace: "name1",
				Name:      "ns1",
			},
		},
		{
			name:   "ByCCP",
			piKeys: []types.NamespacedName{},
			ccpKeys: []types.NamespacedName{{
				Namespace: "name1",
				Name:      "ns1",
			}},
			args: args{
				uid: "name1-ns1-uid",
			},
			wantNamespacedName: types.NamespacedName{
				Namespace: "name1",
				Name:      "ns1",
			},
		},
		{
			name:    "Miss",
			piKeys:  []types.NamespacedName{},
			ccpKeys: []types.NamespacedName{},
			args: args{
				uid: "name1-ns1-uid",
			},
			wantNamespacedName: types.NamespacedName{
				Namespace: "",
				Name:      "",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commonService := newFakeCommonService()
			s := &NSXServiceAccountService{Service: commonService}
			s.SetUpStore()
			for _, piKey := range tt.piKeys {
				piName := piKey.Namespace + "-" + piKey.Name
				piId := piName + "-id"
				crUID := piName + "-uid"
				assert.NoError(t, s.PrincipalIdentityStore.Add(mpmodel.PrincipalIdentity{
					Id:   &piId,
					Name: &piName,
					Tags: []mpmodel.Tag{{
						Scope: &tagScopeNamespace,
						Tag:   &piKey.Namespace,
					}, {
						Scope: &tagScopeNSXServiceAccountCRName,
						Tag:   &piKey.Name,
					}, {
						Scope: &tagScopeNSXServiceAccountCRUID,
						Tag:   &crUID,
					}},
				}))
			}
			for _, ccpKey := range tt.ccpKeys {
				ccpId := ccpKey.Namespace + "-" + ccpKey.Name
				crUID := ccpId + "-uid"
				assert.NoError(t, s.ClusterControlPlaneStore.Add(model.ClusterControlPlane{
					Id: &ccpId,
					Tags: []model.Tag{{
						Scope: &tagScopeNamespace,
						Tag:   &ccpKey.Namespace,
					}, {
						Scope: &tagScopeNSXServiceAccountCRName,
						Tag:   &ccpKey.Name,
					}, {
						Scope: &tagScopeNSXServiceAccountCRUID,
						Tag:   &crUID,
					}},
				}))
			}

			if gotNamespacedName := s.GetNSXServiceAccountNameByUID(tt.args.uid); !reflect.DeepEqual(gotNamespacedName, tt.wantNamespacedName) {
				t.Errorf("GetNSXServiceAccountNameByUID() = %v, want %v", gotNamespacedName, tt.wantNamespacedName)
			}
		})
	}
}

func TestNSXServiceAccountService_getProxyEndpoints(t *testing.T) {
	tests := []struct {
		name        string
		prepareFunc func(*testing.T, *NSXServiceAccountService, context.Context)
		want        nsxvmwarecomv1alpha1.NSXProxyEndpoint
		wantErr     assert.ErrorAssertionFunc
	}{
		{
			name: "NoProxy",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, c context.Context) {
				svc := &v1.Service{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "no-label",
						Namespace: "any",
					},
					Spec: v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
					Status: v1.ServiceStatus{
						LoadBalancer: v1.LoadBalancerStatus{
							Ingress: []v1.LoadBalancerIngress{{IP: "1.2.3.4"}},
						},
					},
				}
				assert.NoError(t, s.Client.Create(c, svc))
			},
			want: nsxvmwarecomv1alpha1.NSXProxyEndpoint{
				Addresses: nil,
				Ports:     nil,
			},
			wantErr: assert.NoError,
		},
		{
			name: "Proxy",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, c context.Context) {
				svc := &v1.Service{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "with-label",
						Namespace: "any",
						Labels:    map[string]string{"mgmt-proxy.antrea-nsx.vmware.com": "", "dummy": "dummy"},
					},
					Spec: v1.ServiceSpec{
						Ports: []v1.ServicePort{
							{
								Name:     "rest-api",
								Protocol: "",
								Port:     10000,
							},
							{
								Name:     "nsx-rpc-fwd-proxy",
								Protocol: "TCP",
								Port:     10001,
							},
							{
								Name:     "rest-api",
								Protocol: "UDP",
								Port:     10002,
							},
							{
								Name:     "wrong-rest-api",
								Protocol: "TCP",
								Port:     10003,
							},
						},
						Type: v1.ServiceTypeLoadBalancer,
					},
					Status: v1.ServiceStatus{
						LoadBalancer: v1.LoadBalancerStatus{
							Ingress: []v1.LoadBalancerIngress{{IP: "1.2.3.4"}, {IP: "1.2.3.5"}},
						},
					},
				}
				assert.NoError(t, s.Client.Create(c, svc))
			},
			want: nsxvmwarecomv1alpha1.NSXProxyEndpoint{
				Addresses: []nsxvmwarecomv1alpha1.NSXProxyEndpointAddress{{IP: "1.2.3.4"}, {IP: "1.2.3.5"}},
				Ports: []nsxvmwarecomv1alpha1.NSXProxyEndpointPort{
					{
						Name:     "rest-api",
						Port:     10000,
						Protocol: "TCP",
					},
					{
						Name:     "nsx-rpc-fwd-proxy",
						Port:     10001,
						Protocol: "TCP",
					},
				},
			},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()
			commonService := newFakeCommonService()
			s := &NSXServiceAccountService{Service: commonService}
			s.SetUpStore()
			tt.prepareFunc(t, s, ctx)

			got, err := s.getProxyEndpoints(ctx)
			if !tt.wantErr(t, err, fmt.Sprintf("getProxyEndpoints()")) {
				return
			}
			assert.Equalf(t, tt.want, got, "getProxyEndpoints()")
		})
	}
}
