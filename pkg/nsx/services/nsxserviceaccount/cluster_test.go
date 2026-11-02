/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsxserviceaccount

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/cluster/restore"
	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
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

func (c *fakeClusterControlPlanesClient) List(siteIdParam string, enforcementpointIdParam string, cursorParam *string, includeConflictsParam *bool, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.ClusterControlPlaneListResult, error) {
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

func (c *fakeWithCertificateClient) Get(certificateID string, detail *bool) (model.TlsCertificate, error) {
	fmt.Println(certificateID, detail)
	return model.TlsCertificate{}, nil
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

func (c *fakeCertificatesClient) Get(certIdParam string, detailsParam *bool, fmtParam *string) (mpmodel.Certificate, error) {
	return mpmodel.Certificate{}, nil
}

func (c *fakeCertificatesClient) Importcertificate(trustObjectDataParam mpmodel.TrustObjectData) (mpmodel.CertificateList, error) {
	return mpmodel.CertificateList{}, nil
}

func (c *fakeCertificatesClient) Importtrustedca(aliasParam string, trustObjectDataParam mpmodel.TrustObjectData) error {
	return nil
}

func (c *fakeCertificatesClient) List(cursorParam *string, detailsParam *bool, fmtParam *string, includedFieldsParam *string, nodeIdParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string, type_Param *string) (mpmodel.CertificateList, error) {
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
	scheme := clientgoscheme.Scheme
	v1alpha1.AddToScheme(scheme)
	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.NSXServiceAccount{}).Build()
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
			StatusClient:               restore.NewStatusClient(nil),
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
		prepareFunc func(*testing.T, *NSXServiceAccountService, context.Context, *v1alpha1.NSXServiceAccount) *gomonkey.Patches
		args        args
		wantErr     bool
		wantSecret  bool
		expectedCR  *v1alpha1.NSXServiceAccount
	}{
		{
			name: "GenerateCertificateError",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				patches := gomonkey.ApplyFuncSeq(util.GenerateCertificate, []gomonkey.OutputCell{{
					Values: gomonkey.Params{"", "", fmt.Errorf("mock error")},
					Times:  1,
				}})
				patches.ApplyMethodSeq(s.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{true},
					Times:  1,
				}})
				return patches
			},
			args: args{
				obj: &v1alpha1.NSXServiceAccount{
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
			name: "Success with update PI Cert",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				normalizedClusterName := "k8scl-one_test-ns1-name1"
				vpcPath := "/orgs/default/projects/k8scl-one:test/vpcs/vpc1"
				piId := "Id1"
				uid := "00000000-0000-0000-0000-000000000001"
				s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{
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
				assert.NoError(t, s.Client.Create(ctx, obj))
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
				certId2 := "certId2"
				uidScope := common.TagScopeNSXServiceAccountCRUID
				uidTag := "00000000-0000-0000-0000-000000000001"
				pi2 := mpmodel.PrincipalIdentity{Name: &normalizedClusterName, Id: &piId, CertificateId: &certId2, Tags: []mpmodel.Tag{{
					Scope: &uidScope,
					Tag:   &uidTag,
				}}}
				patches.ApplyMethodSeq(s.NSXClient.CertificatesClient, "Importcertificate", []gomonkey.OutputCell{{
					Values: gomonkey.Params{mpmodel.CertificateList{Results: []mpmodel.Certificate{{Id: &certId2}}}, nil},
					Times:  1,
				}})
				patches.ApplyMethodSeq(s.NSXClient.PrincipalIdentitiesClient, "Updatecertificate", []gomonkey.OutputCell{{
					Values: gomonkey.Params{pi2, nil},
					Times:  1,
				}})
				patches.ApplyMethodSeq(s.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{true},
					Times:  1,
				}})
				return patches
			},
			args: args{
				obj: &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: v1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
				},
			},
			wantErr:    false,
			wantSecret: true,
			expectedCR: &v1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "name1",
					Namespace:       "ns1",
					UID:             "00000000-0000-0000-0000-000000000001",
					ResourceVersion: "2",
				},
				Spec: v1alpha1.NSXServiceAccountSpec{
					VPCName: "vpc1",
				},
				Status: v1alpha1.NSXServiceAccountStatus{
					Phase:  "realized",
					Reason: "Success",
					Conditions: []metav1.Condition{
						{
							Type:    v1alpha1.ConditionTypeRealized,
							Status:  metav1.ConditionTrue,
							Reason:  v1alpha1.ConditionReasonRealizationSuccess,
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
			name: "Success",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
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
				patches.ApplyMethodSeq(s.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{true},
					Times:  1,
				}})
				return patches
			},
			args: args{
				obj: &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: v1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
				},
			},
			wantErr:    false,
			wantSecret: true,
			expectedCR: &v1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "name1",
					Namespace:       "ns1",
					UID:             "00000000-0000-0000-0000-000000000001",
					ResourceVersion: "2",
				},
				Spec: v1alpha1.NSXServiceAccountSpec{
					VPCName: "vpc1",
				},
				Status: v1alpha1.NSXServiceAccountStatus{
					Phase:  "realized",
					Reason: "Success",
					Conditions: []metav1.Condition{
						{
							Type:    v1alpha1.ConditionTypeRealized,
							Status:  metav1.ConditionTrue,
							Reason:  v1alpha1.ConditionReasonRealizationSuccess,
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
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
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
				patches.ApplyMethodSeq(s.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{true},
					Times:  2,
				}})
				return patches
			},
			args: args{
				obj: &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
				},
			},
			wantErr:    false,
			wantSecret: true,
			expectedCR: &v1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "name1",
					Namespace:       "ns1",
					UID:             "00000000-0000-0000-0000-000000000001",
					ResourceVersion: "2",
				},
				Spec: v1alpha1.NSXServiceAccountSpec{},
				Status: v1alpha1.NSXServiceAccountStatus{
					Phase:  "realized",
					Reason: "Success",
					Conditions: []metav1.Condition{
						{
							Type:    v1alpha1.ConditionTypeRealized,
							Status:  metav1.ConditionTrue,
							Reason:  v1alpha1.ConditionReasonRealizationSuccess,
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
			if patches != nil {
				defer patches.Reset()
			}

			if err := s.CreateOrUpdateNSXServiceAccount(ctx, tt.args.obj); (err != nil) != tt.wantErr {
				t.Errorf("CreateOrUpdateNSXServiceAccount() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantSecret {
				secret := &v1.Secret{}
				assert.NoError(t, s.Client.Get(ctx, types.NamespacedName{
					Namespace: tt.args.obj.Namespace,
					Name:      tt.args.obj.Name + SecretSuffix,
				}, secret))
				assert.Equal(t, 3, len(secret.Data))
			}
			actualCR := &v1alpha1.NSXServiceAccount{}
			err := s.Client.Get(ctx, types.NamespacedName{
				Namespace: tt.args.obj.Namespace,
				Name:      tt.args.obj.Name,
			}, actualCR)
			if tt.expectedCR == nil {
				assert.True(t, k8serrors.IsNotFound(err))
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

func newTestServiceAccount() *v1alpha1.NSXServiceAccount {
	return &v1alpha1.NSXServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Name: "name1", Namespace: "ns1", UID: "00000000-0000-0000-0000-000000000001",
	}}
}

func newOwnedTestSecret(obj *v1alpha1.NSXServiceAccount, data map[string][]byte) *v1.Secret {
	return &v1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name: obj.Name + SecretSuffix, Namespace: obj.Namespace, UID: "secret-uid-1",
		OwnerReferences: []metav1.OwnerReference{{APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind: "NSXServiceAccount", Name: obj.Name, UID: obj.UID}},
	}, Data: data}
}

type serviceAccountCertificates struct {
	pi, ccp string
}

// Mock the NSX boundary while retaining the real credential and status handling.
func mockServiceAccountNSX(t *testing.T, s *NSXServiceAccountService, obj *v1alpha1.NSXServiceAccount, existing bool) *serviceAccountCertificates {
	t.Helper()
	name := util.NormalizeId(s.getClusterName(obj.Namespace, obj.Name))
	piID, certID, nodeID := "pi1", "cert1", "clusterId1"
	scope, uid := common.TagScopeNSXServiceAccountCRUID, string(obj.UID)
	pi := mpmodel.PrincipalIdentity{Name: &name, Id: &piID,
		Tags: []mpmodel.Tag{{Scope: &scope, Tag: &uid}}}
	ccp := model.ClusterControlPlane{Id: &name, NodeId: &nodeID,
		Tags: []model.Tag{{Scope: &scope, Tag: &uid}}}
	if existing {
		require.NoError(t, s.PrincipalIdentityStore.Add(&pi))
		require.NoError(t, s.ClusterControlPlaneStore.Add(&ccp))
	}
	captured := &serviceAccountCertificates{}
	patches := gomonkey.ApplyMethod(s.NSXClient, "NSXCheckVersion", func(_ *nsx.Client, _ int) bool { return true })
	t.Cleanup(patches.Reset)
	patches.ApplyMethod(s.NSXClient.WithCertificateClient, "Create", func(_ *fakeWithCertificateClient, request mpmodel.PrincipalIdentityWithCertificate) (mpmodel.PrincipalIdentity, error) {
		captured.pi = *request.CertificatePem
		return pi, nil
	})
	patches.ApplyMethod(s.NSXClient.CertificatesClient, "Importcertificate", func(_ *fakeCertificatesClient, request mpmodel.TrustObjectData) (mpmodel.CertificateList, error) {
		captured.pi = *request.PemEncoded
		return mpmodel.CertificateList{Results: []mpmodel.Certificate{{Id: &certID}}}, nil
	})
	patches.ApplyMethod(s.NSXClient.PrincipalIdentitiesClient, "Updatecertificate", func(_ *fakePrincipalIdentitiesClient, _ mpmodel.UpdatePrincipalIdentityCertificateRequest) (mpmodel.PrincipalIdentity, error) {
		pi.CertificateId = &certID
		return pi, nil
	})
	patches.ApplyMethod(s.NSXClient.ClusterControlPlanesClient, "Update", func(_ *fakeClusterControlPlanesClient, _, _, _ string, request model.ClusterControlPlane) (model.ClusterControlPlane, error) {
		captured.ccp = *request.Certificate
		request.Id, request.NodeId = &name, &nodeID
		return request, nil
	})
	return captured
}

func TestNSXServiceAccountService_CreateOrUpdateNSXServiceAccount_Recovery(t *testing.T) {
	subject := util.DefaultSubject
	subject.CommonName = "k8scl-one_test-ns1-name1"
	cert, key, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	require.NoError(t, err)
	_, otherKey, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	require.NoError(t, err)
	for _, tt := range []struct {
		name                                    string
		data                                    map[string][]byte
		ownerUID                                types.UID
		otherOwner, existingNSX, restore, reuse bool
		wantErr                                 string
	}{
		{name: "InvalidOwnedSecretRegeneratesCredentials", data: map[string][]byte{"old": []byte("old")}},
		{name: "ValidOwnedSecretReusesCredentials", data: map[string][]byte{SecretCertName: []byte(cert), SecretKeyName: []byte(key)}, reuse: true},
		{name: "StaleSecretDeleted", data: map[string][]byte{}, ownerUID: "older-cr", wantErr: "stale secret"},
		{name: "MismatchedKeyRegeneratesCredentials", data: map[string][]byte{SecretCertName: []byte(cert), SecretKeyName: []byte(otherKey)}},
		{name: "PreservesRestoreStatus", restore: true},
		{name: "UnrelatedOwnerRejected", data: map[string][]byte{"unrelated": []byte("data")}, otherOwner: true, wantErr: "not owned"},
		{name: "UpdatesExistingPIAndCCP", existingNSX: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s := &NSXServiceAccountService{Service: newFakeCommonService()}
			s.SetUpStore()
			obj := newTestServiceAccount()
			initial := obj.DeepCopy()
			if tt.restore {
				initial.Status.NSXRestoreStatus = &v1alpha1.NSXRestoreStatus{Status: "SUCCESS"}
			}
			require.NoError(t, s.Client.Create(ctx, initial))
			if tt.data != nil {
				secret := newOwnedTestSecret(obj, tt.data)
				if tt.ownerUID != "" {
					secret.OwnerReferences[0].UID = tt.ownerUID
				}
				if tt.otherOwner {
					secret.OwnerReferences[0] = metav1.OwnerReference{APIVersion: "apps/v1", Kind: "Deployment", Name: "unrelated", UID: "other-uid"}
				}
				require.NoError(t, s.Client.Create(ctx, secret))
			}
			captured := mockServiceAccountNSX(t, s, obj, tt.existingNSX)
			err := s.CreateOrUpdateNSXServiceAccount(ctx, obj)
			secret := &v1.Secret{}
			secretErr := s.Client.Get(ctx, types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name + SecretSuffix}, secret)
			actual := &v1alpha1.NSXServiceAccount{}
			require.NoError(t, s.Client.Get(ctx, client.ObjectKeyFromObject(obj), actual))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Empty(t, captured.pi)
				assert.Empty(t, captured.ccp)
				assert.Equal(t, initial.Status, actual.Status)
				if tt.ownerUID != "" {
					assert.True(t, k8serrors.IsNotFound(secretErr))
				} else {
					require.NoError(t, secretErr)
					assert.Equal(t, tt.data, secret.Data)
				}
				return
			}
			require.NoError(t, err)
			require.NoError(t, secretErr)
			_, err = tls.X509KeyPair(secret.Data[SecretCertName], secret.Data[SecretKeyName])
			require.NoError(t, err)
			assert.Equal(t, string(secret.Data[SecretCertName]), captured.pi)
			assert.Equal(t, captured.pi, captured.ccp)
			assert.Equal(t, s.NSXConfig.GetCACert(), secret.Data[CAName])
			assert.Equal(t, secretOwnedByCurrentCR, evaluateSecretOwnership(secret, obj))
			assert.Equal(t, v1alpha1.NSXServiceAccountPhaseRealized, actual.Status.Phase)
			assert.Equal(t, "Success", actual.Status.Reason)
			assert.Equal(t, "clusterId1", actual.Status.ClusterID)
			assert.Equal(t, subject.CommonName, actual.Status.ClusterName)
			assert.Equal(t, s.NSXConfig.NsxApiManagers, actual.Status.NSXManagers)
			assert.Equal(t, []v1alpha1.NSXSecret{{Name: secret.Name, Namespace: secret.Namespace}}, actual.Status.Secrets)
			assert.Equal(t, initial.Status.NSXRestoreStatus, actual.Status.NSXRestoreStatus)
			if tt.reuse {
				assert.Equal(t, cert, string(secret.Data[SecretCertName]))
				assert.Equal(t, key, string(secret.Data[SecretKeyName]))
			} else if tt.data != nil {
				assert.NotEqual(t, cert, string(secret.Data[SecretCertName]))
			}
		})
	}
}

func TestNSXServiceAccountService_CreateOrUpdateNSXServiceAccount_CacheMissAlreadyExists(t *testing.T) {
	ctx := context.Background()
	s := &NSXServiceAccountService{Service: newFakeCommonService()}
	s.SetUpStore()
	obj := newTestServiceAccount()
	require.NoError(t, s.Client.Create(ctx, obj))
	subject := util.DefaultSubject
	subject.CommonName = util.NormalizeId(s.getClusterName(obj.Namespace, obj.Name))
	cert, key, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	require.NoError(t, err)
	secret := newOwnedTestSecret(obj, map[string][]byte{
		SecretCertName: []byte(cert), SecretKeyName: []byte(key), CAName: s.NSXConfig.GetCACert(),
	})
	require.NoError(t, s.Client.Create(ctx, secret))
	base := s.Client
	missed := false
	s.Client = interceptor.NewClient(base.(client.WithWatch), interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, k client.ObjectKey, o client.Object, opts ...client.GetOption) error {
			if k == client.ObjectKeyFromObject(secret) && !missed {
				missed = true
				return k8serrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, k.Name)
			}
			return c.Get(ctx, k, o, opts...)
		},
	})
	captured := mockServiceAccountNSX(t, s, obj, false)
	require.True(t, k8serrors.IsAlreadyExists(s.CreateOrUpdateNSXServiceAccount(ctx, obj)))
	assert.Empty(t, captured.pi)
	assert.Empty(t, captured.ccp)
	stored := &v1.Secret{}
	require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(secret), stored))
	assert.Equal(t, secret.Data, stored.Data)

	// Once the cache catches up, reuse the same credentials and complete creation.
	require.NoError(t, s.CreateOrUpdateNSXServiceAccount(ctx, obj))
	assert.Equal(t, cert, captured.pi)
	assert.Equal(t, cert, captured.ccp)
	actual := &v1alpha1.NSXServiceAccount{}
	require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(obj), actual))
	assert.Equal(t, v1alpha1.NSXServiceAccountPhaseRealized, actual.Status.Phase)
	require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(secret), stored))
	assert.Equal(t, secret.Data, stored.Data)
}

func TestNSXServiceAccountService_CreateOrUpdateNSXServiceAccount_StatusFailureRecovery(t *testing.T) {
	ctx := context.Background()
	s := &NSXServiceAccountService{Service: newFakeCommonService()}
	s.SetUpStore()
	obj := newTestServiceAccount()
	require.NoError(t, s.Client.Create(ctx, obj))
	captured := mockServiceAccountNSX(t, s, obj, false)
	base := s.Client
	failStatus := true
	statusWrites := 0
	s.Client = interceptor.NewClient(base.(client.WithWatch), interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, c client.Client, subresource string, o client.Object, opts ...client.SubResourceUpdateOption) error {
			if subresource == "status" && client.ObjectKeyFromObject(o) == client.ObjectKeyFromObject(obj) {
				statusWrites++
				if failStatus {
					return k8serrors.NewConflict(schema.GroupResource{Group: "nsx.vmware.com", Resource: "nsxserviceaccounts"}, o.GetName(), fmt.Errorf("injected status conflict"))
				}
			}
			return c.SubResource(subresource).Update(ctx, o, opts...)
		},
	})

	// Provisioning succeeds, but status conflicts outlast the in-call retries.
	require.True(t, k8serrors.IsConflict(s.CreateOrUpdateNSXServiceAccount(ctx, obj)))
	assert.Greater(t, statusWrites, 1)
	secretKey := client.ObjectKey{Namespace: obj.Namespace, Name: obj.Name + SecretSuffix}
	originalSecret := &v1.Secret{}
	require.NoError(t, base.Get(ctx, secretKey, originalSecret))
	cert := string(originalSecret.Data[SecretCertName])
	require.NotEmpty(t, cert)
	assert.Equal(t, cert, captured.pi)
	assert.Equal(t, cert, captured.ccp)
	name := util.NormalizeId(s.getClusterName(obj.Namespace, obj.Name))
	ccp, ok := s.ClusterControlPlaneStore.GetByKey(name).(*model.ClusterControlPlane)
	require.True(t, ok)
	require.NotNil(t, ccp.NodeId)
	clusterID := *ccp.NodeId
	require.NotEmpty(t, clusterID)

	// Retry from persisted state, rather than the object used by the failed call.
	retried := &v1alpha1.NSXServiceAccount{}
	require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(obj), retried))
	assert.False(t, IsNSXServiceAccountRealized(&retried.Status))
	failStatus = false
	captured.pi, captured.ccp = "", ""
	require.NoError(t, s.CreateOrUpdateNSXServiceAccount(ctx, retried))

	storedSecret := &v1.Secret{}
	require.NoError(t, base.Get(ctx, secretKey, storedSecret))
	assert.Equal(t, originalSecret.Data, storedSecret.Data)
	assert.Equal(t, cert, captured.pi)
	assert.Equal(t, cert, captured.ccp)
	realized := &v1alpha1.NSXServiceAccount{}
	require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(obj), realized))
	assert.Equal(t, v1alpha1.NSXServiceAccountPhaseRealized, realized.Status.Phase)
	assert.True(t, IsNSXServiceAccountRealized(&realized.Status))
	assert.Equal(t, clusterID, realized.Status.ClusterID)
	assert.Equal(t, []v1alpha1.NSXSecret{{Name: secretKey.Name, Namespace: secretKey.Namespace}}, realized.Status.Secrets)
}

func TestNSXServiceAccountService_RestoreRealizedNSXServiceAccount(t *testing.T) {
	type args struct {
		obj *v1alpha1.NSXServiceAccount
	}
	notFoundErr := vapierrors.NewNotFound()
	notFoundErr.Data = data.NewStructValue("test", nil)
	tests := []struct {
		name        string
		prepareFunc func(*testing.T, *NSXServiceAccountService, context.Context, *v1alpha1.NSXServiceAccount) *gomonkey.Patches
		args        args
		wantErr     bool
	}{
		{
			name: "Skip",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				normalizedClusterName := "k8scl-one_test-ns1-name1"
				vpcPath := "/orgs/default/projects/k8scl-one:test/vpcs/vpc1"
				piId := "Id1"
				uid := "00000000-0000-0000-0000-000000000001"
				certificateID := "cert"
				s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{
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
					Id:            &piId,
					CertificateId: &certificateID,
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
				s.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{
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
				secretName := obj.Status.Secrets[0].Name
				secretNamespace := obj.Status.Secrets[0].Namespace
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
					Type:      "",
				}))
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.WithCertificateClient, "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						model.TlsCertificate{},
						nil,
					},
					Times: 1,
				}})
				return patches
			},
			args: args{
				obj: &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: v1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
					Status: v1alpha1.NSXServiceAccountStatus{
						Phase:       v1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []v1alpha1.NSXSecret{{
							Name:      "name1" + SecretSuffix,
							Namespace: "ns1",
						}},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "update cert",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				normalizedClusterName := "k8scl-one_test-ns1-name1"
				vpcPath := "/orgs/default/projects/k8scl-one:test/vpcs/vpc1"
				piId := "Id1"
				uid := "00000000-0000-0000-0000-000000000001"
				certificateID := "cert"
				s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{
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
					Id:            &piId,
					CertificateId: &certificateID,
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
				subject := util.DefaultSubject
				subject.CommonName = "k8scl-one_test-ns1-name1"
				cert, _, _ := util.GenerateCertificate(&subject, 5)
				s.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{
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
				secretName := obj.Status.Secrets[0].Name
				secretNamespace := obj.Status.Secrets[0].Namespace
				key := "fakekey"
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
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.WithCertificateClient, "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						model.TlsCertificate{},
						nil,
					},
					Times: 1,
				}})
				certId2 := "certId2"
				uidScope := common.TagScopeNSXServiceAccountCRUID
				uidTag := "00000000-0000-0000-0000-000000000001"
				pi2 := mpmodel.PrincipalIdentity{Name: &normalizedClusterName, Id: &piId, CertificateId: &certId2, Tags: []mpmodel.Tag{{
					Scope: &uidScope,
					Tag:   &uidTag,
				}}}
				patches.ApplyMethodSeq(s.NSXClient.CertificatesClient, "Importcertificate", []gomonkey.OutputCell{{
					Values: gomonkey.Params{mpmodel.CertificateList{Results: []mpmodel.Certificate{{Id: &certId2}}}, nil},
					Times:  1,
				}})
				patches.ApplyMethodSeq(s.NSXClient.PrincipalIdentitiesClient, "Updatecertificate", []gomonkey.OutputCell{{
					Values: gomonkey.Params{pi2, nil},
					Times:  1,
				}})
				return patches
			},
			args: args{
				obj: &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: v1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
					Status: v1alpha1.NSXServiceAccountStatus{
						Phase:       v1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []v1alpha1.NSXSecret{{
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
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				normalizedClusterName := "k8scl-one_test-ns1-name1"
				vpcPath := "/orgs/default/projects/k8scl-one:test/vpcs/vpc1"
				piId := "Id1"
				uid := "00000000-0000-0000-0000-000000000001"
				certificateID := "cert"
				s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{
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
					Id:            &piId,
					CertificateId: &certificateID,
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
				secretName := obj.Status.Secrets[0].Name
				secretNamespace := obj.Status.Secrets[0].Namespace
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
					Type:      "",
				}))
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.WithCertificateClient, "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						model.TlsCertificate{},
						nil,
					},
					Times: 1,
				}})
				return patches
			},
			args: args{
				obj: &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: v1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
					Status: v1alpha1.NSXServiceAccountStatus{
						Phase:       v1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []v1alpha1.NSXSecret{{
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
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				secretName := obj.Status.Secrets[0].Name
				secretNamespace := obj.Status.Secrets[0].Namespace
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
					Type:      "",
				}))
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.WithCertificateClient, "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						model.TlsCertificate{},
						nil,
					},
					Times: 1,
				}})
				patches.ApplyMethodSeq(s.NSXClient.ClusterControlPlanesClient, "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.ClusterControlPlane{}, nil},
					Times:  1,
				}})
				return patches
			},
			args: args{
				obj: &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: v1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
					Status: v1alpha1.NSXServiceAccountStatus{
						Phase:       v1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []v1alpha1.NSXSecret{{
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
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.ClusterControlPlanesClient, "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.ClusterControlPlane{}, fmt.Errorf("mock error")},
					Times:  1,
				}})
				return patches
			},
			args: args{
				obj: &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: v1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
					Status: v1alpha1.NSXServiceAccountStatus{
						Phase:       v1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []v1alpha1.NSXSecret{{
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
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.ClusterControlPlanesClient, "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.ClusterControlPlane{}, *notFoundErr},
					Times:  1,
				}})
				return patches
			},
			args: args{
				obj: &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: v1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
					Status: v1alpha1.NSXServiceAccountStatus{
						Phase:       v1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []v1alpha1.NSXSecret{{
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
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				normalizedClusterName := "k8scl-one_test-ns1-name1"
				vpcPath := "/orgs/default/projects/k8scl-one:test/vpcs/vpc1"
				piId := "Id1"
				uid := "00000000-0000-0000-0000-000000000001"
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.ClusterControlPlanesClient, "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.ClusterControlPlane{}, *notFoundErr},
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
				obj: &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: v1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
					Status: v1alpha1.NSXServiceAccountStatus{
						Phase:       v1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []v1alpha1.NSXSecret{{
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
			if err := s.RestoreRealizedNSXServiceAccount(ctx, tt.args.obj); (err != nil) != tt.wantErr {
				t.Errorf("RestoreRealizedNSXServiceAccount() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNSXServiceAccountService_ValidateAndUpdateRealizedNSXServiceAccount(t *testing.T) {
	fakeId := "fakeBackupId"
	fakeId2 := "fakeBackupId2"
	fakeRestoreEndTime := int64(12345678)
	fakeRestoreEndTime2 := int64(123456789)
	restoreStatusValueSuccess := mpmodel.GlobalRestoreStatus_VALUE_SUCCESS
	restoreStatusValueRunning := mpmodel.GlobalRestoreStatus_VALUE_RUNNING

	type args struct {
		obj              *v1alpha1.NSXServiceAccount
		ca               []byte
		nsxRestoreStatus *v1alpha1.NSXRestoreStatus
	}
	subject := util.DefaultSubject
	subject.CommonName = "nsx1"
	ca, _, _ := util.GenerateCertificate(&subject, util.DefaultValidDaysWithRotation)
	subject = util.DefaultSubject
	subject.CommonName = "k8scl-one_test-ns1-name1"
	cert, _, _ := util.GenerateCertificate(&subject, 5)
	uidScope := common.TagScopeNSXServiceAccountCRUID
	uidTag := "00000000-0000-0000-0000-000000000001"
	tests := []struct {
		name                     string
		prepareFunc              func(*testing.T, *NSXServiceAccountService, context.Context, *v1alpha1.NSXServiceAccount) *gomonkey.Patches
		args                     args
		wantNewCA                bool
		wantNewCert              bool
		wantErr                  bool
		expectedNSXRestoreStatus *v1alpha1.NSXRestoreStatus
	}{
		{
			name: "Skip",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{false},
					Times:  1,
				}})
				return patches
			},
			args: args{
				obj: &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: v1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
					Status: v1alpha1.NSXServiceAccountStatus{
						Phase:       v1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []v1alpha1.NSXSecret{{
							Name:      "name1" + SecretSuffix,
							Namespace: "ns1",
						}},
					},
				},
				ca: nil,
			},
			wantNewCA:   false,
			wantNewCert: false,
			wantErr:     false,
		},
		{
			name: "GetSecretError",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{true},
					Times:  1,
				}})
				return patches
			},
			args: args{
				obj: &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: v1alpha1.NSXServiceAccountSpec{
						VPCName:            "vpc1",
						EnableCertRotation: true,
					},
					Status: v1alpha1.NSXServiceAccountStatus{
						Phase:       v1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []v1alpha1.NSXSecret{{
							Name:      "name1" + SecretSuffix,
							Namespace: "ns1",
						}},
					},
				},
				ca: nil,
			},
			wantNewCA:   false,
			wantNewCert: false,
			wantErr:     true,
		},
		{
			name: "UpdateCA",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
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
				patches := gomonkey.ApplyMethodSeq(s.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{false},
					Times:  1,
				}})
				return patches
			},
			args: args{
				obj: &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: v1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
					Status: v1alpha1.NSXServiceAccountStatus{
						Phase:       v1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []v1alpha1.NSXSecret{{
							Name:      "name1" + SecretSuffix,
							Namespace: "ns1",
						}},
					},
				},
				ca: []byte(ca),
			},
			wantNewCA:   true,
			wantNewCert: false,
			wantErr:     false,
		},
		{
			name: "UpdateCert",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				secretName := obj.Status.Secrets[0].Name
				secretNamespace := obj.Status.Secrets[0].Namespace
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

				normalizedClusterName := "k8scl-one_test-ns1-name1"
				piId := "piId1"
				certId := "certId1"
				certId2 := "certId2"
				ccp := model.ClusterControlPlane{Id: &normalizedClusterName, Tags: []model.Tag{{
					Scope: &uidScope,
					Tag:   &uidTag,
				}}}
				pi := mpmodel.PrincipalIdentity{Name: &normalizedClusterName, Id: &piId, CertificateId: &certId, Tags: []mpmodel.Tag{{
					Scope: &uidScope,
					Tag:   &uidTag,
				}}}
				pi2 := mpmodel.PrincipalIdentity{Name: &normalizedClusterName, Id: &piId, CertificateId: &certId2, Tags: []mpmodel.Tag{{
					Scope: &uidScope,
					Tag:   &uidTag,
				}}}
				assert.NoError(t, s.ClusterControlPlaneStore.Add(&ccp))
				assert.NoError(t, s.PrincipalIdentityStore.Add(&pi))

				patches := gomonkey.ApplyMethodSeq(s.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{true},
					Times:  1,
				}})
				patches.ApplyMethodSeq(s.NSXClient.ClusterControlPlanesClient, "Update", []gomonkey.OutputCell{{
					Values: gomonkey.Params{ccp, nil},
					Times:  1,
				}})
				patches.ApplyMethodSeq(s.NSXClient.CertificatesClient, "Importcertificate", []gomonkey.OutputCell{{
					Values: gomonkey.Params{mpmodel.CertificateList{Results: []mpmodel.Certificate{{Id: &certId2}}}, nil},
					Times:  1,
				}})
				patches.ApplyMethodSeq(s.NSXClient.PrincipalIdentitiesClient, "Updatecertificate", []gomonkey.OutputCell{{
					Values: gomonkey.Params{pi2, nil},
					Times:  1,
				}})
				patches.ApplyMethodSeq(s.NSXClient.CertificatesClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
			args: args{
				obj: &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: v1alpha1.NSXServiceAccountSpec{
						VPCName:            "vpc1",
						EnableCertRotation: true,
					},
					Status: v1alpha1.NSXServiceAccountStatus{
						Phase:       v1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []v1alpha1.NSXSecret{{
							Name:      "name1" + SecretSuffix,
							Namespace: "ns1",
						}},
					},
				},
				ca: nil,
			},
			wantNewCA:   false,
			wantNewCert: true,
			wantErr:     false,
		},
		{
			name: "AddNSXRestoreStatus",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{false},
					Times:  1,
				}})
				return patches
			},
			args: args{
				obj: &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: v1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
					Status: v1alpha1.NSXServiceAccountStatus{
						Phase:       v1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []v1alpha1.NSXSecret{{
							Name:      "name1" + SecretSuffix,
							Namespace: "ns1",
						}},
					},
				},
				ca: nil,
				nsxRestoreStatus: &v1alpha1.NSXRestoreStatus{
					Id:             fakeId,
					Status:         restoreStatusValueSuccess,
					RestoreEndTime: fakeRestoreEndTime,
				},
			},
			expectedNSXRestoreStatus: &v1alpha1.NSXRestoreStatus{
				Id:             fakeId,
				Status:         restoreStatusValueSuccess,
				RestoreEndTime: fakeRestoreEndTime,
			},
			wantNewCA:   false,
			wantNewCert: false,
			wantErr:     false,
		},
		{
			name: "UpdateNSXRestoreStatus",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{false},
					Times:  1,
				}})
				return patches
			},
			args: args{
				obj: &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: v1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
					Status: v1alpha1.NSXServiceAccountStatus{
						Phase:       v1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []v1alpha1.NSXSecret{{
							Name:      "name1" + SecretSuffix,
							Namespace: "ns1",
						}},
						NSXRestoreStatus: &v1alpha1.NSXRestoreStatus{
							Id:             fakeId,
							Status:         restoreStatusValueSuccess,
							RestoreEndTime: fakeRestoreEndTime,
						},
					},
				},
				ca: nil,
				nsxRestoreStatus: &v1alpha1.NSXRestoreStatus{
					Id:             fakeId2,
					Status:         restoreStatusValueSuccess,
					RestoreEndTime: fakeRestoreEndTime2,
				},
			},
			expectedNSXRestoreStatus: &v1alpha1.NSXRestoreStatus{
				Id:             fakeId2,
				Status:         restoreStatusValueSuccess,
				RestoreEndTime: fakeRestoreEndTime2,
			},
			wantNewCA:   false,
			wantNewCert: false,
			wantErr:     false,
		},
		{
			name: "DoNotUpdateNSXRestoreStatus",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{false},
					Times:  1,
				}})
				return patches
			},
			args: args{
				obj: &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
						UID:       "00000000-0000-0000-0000-000000000001",
					},
					Spec: v1alpha1.NSXServiceAccountSpec{
						VPCName: "vpc1",
					},
					Status: v1alpha1.NSXServiceAccountStatus{
						Phase:       v1alpha1.NSXServiceAccountPhaseRealized,
						VPCPath:     "/orgs/default/projects/k8scl-one:test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one_test-ns1-name1",
						Secrets: []v1alpha1.NSXSecret{{
							Name:      "name1" + SecretSuffix,
							Namespace: "ns1",
						}},
						NSXRestoreStatus: &v1alpha1.NSXRestoreStatus{
							Id:             fakeId,
							Status:         restoreStatusValueSuccess,
							RestoreEndTime: fakeRestoreEndTime,
						},
					},
				},
				ca: nil,
				nsxRestoreStatus: &v1alpha1.NSXRestoreStatus{
					Id:     fakeId2,
					Status: restoreStatusValueRunning,
				},
			},
			expectedNSXRestoreStatus: &v1alpha1.NSXRestoreStatus{
				Id:             fakeId,
				Status:         restoreStatusValueSuccess,
				RestoreEndTime: fakeRestoreEndTime,
			},
			wantNewCA:   false,
			wantNewCert: false,
			wantErr:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()
			commonService := newFakeCommonService()
			s := &NSXServiceAccountService{Service: commonService}
			s.SetUpStore()
			assert.NoError(t, s.Client.Create(ctx, tt.args.obj))
			patches := tt.prepareFunc(t, s, ctx, tt.args.obj)
			if patches != nil {
				defer patches.Reset()
			}

			if err := s.ValidateAndUpdateRealizedNSXServiceAccount(ctx, tt.args.obj, tt.args.ca, tt.args.nsxRestoreStatus); (err != nil) != tt.wantErr {
				t.Errorf("ValidateAndUpdateRealizedNSXServiceAccount() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantNewCA {
				secret := &v1.Secret{}
				assert.NoError(t, s.Client.Get(ctx, types.NamespacedName{
					Namespace: tt.args.obj.Namespace,
					Name:      tt.args.obj.Name + SecretSuffix,
				}, secret))
				assert.Equal(t, tt.args.ca, secret.Data[CAName])
			}
			if tt.wantNewCert {
				secret := &v1.Secret{}
				assert.NoError(t, s.Client.Get(ctx, types.NamespacedName{
					Namespace: tt.args.obj.Namespace,
					Name:      tt.args.obj.Name + SecretSuffix,
				}, secret))
				certBlock, _ := pem.Decode(secret.Data[SecretCertName])
				certObj, err := x509.ParseCertificate(certBlock.Bytes)
				require.NoError(t, err, "Wrong secret: %+v,\n err: %+v", secret, err)
				assert.True(t, time.Now().AddDate(0, 0, util.DefaultValidDaysWithRotation).After(certObj.NotAfter))
				assert.True(t, time.Now().AddDate(0, 0, util.DefaultValidDaysWithRotation-util.DefaultRotateDays).Before(certObj.NotAfter))
				assert.True(t, time.Now().After(certObj.NotBefore))
			}

			nsxsa := &v1alpha1.NSXServiceAccount{}
			assert.NoError(t, s.Client.Get(ctx, types.NamespacedName{
				Namespace: tt.args.obj.Namespace,
				Name:      tt.args.obj.Name,
			}, nsxsa))
			assert.Equal(t, nsxsa.Status.NSXRestoreStatus, tt.expectedNSXRestoreStatus)
		})
	}
}

func TestNSXServiceAccountService_ValidateAndUpdateRealizedNSXServiceAccount_ConcurrencyAndOwnership(t *testing.T) {
	subject := util.DefaultSubject
	subject.CommonName = "ca1"
	oldCA, _, err := util.GenerateCertificate(&subject, 100)
	require.NoError(t, err)
	subject.CommonName = "ca2"
	newCA, _, err := util.GenerateCertificate(&subject, 100)
	require.NoError(t, err)

	for _, tt := range []struct {
		name         string
		mutateSecret func(*v1.Secret)
		wantErr      string
	}{
		{name: "CAOnlyUpdatePreservesLatestCertAndKey", mutateSecret: func(secret *v1.Secret) {
			secret.Data[SecretCertName] = []byte("newerCert")
			secret.Data[SecretKeyName] = []byte("newerKey")
		}},
		{name: "ReplacedSecretUIDAbortsUpdate", mutateSecret: func(secret *v1.Secret) {
			secret.UID = "replaced-secret-uid-2"
		}, wantErr: "was replaced concurrently"},
		{name: "SecretOwnershipLostAbortsUpdate", mutateSecret: func(secret *v1.Secret) {
			secret.OwnerReferences = nil
		}, wantErr: "no longer owned by current NSXServiceAccount"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s := &NSXServiceAccountService{Service: newFakeCommonService()}
			patches := gomonkey.ApplyMethod(s.NSXClient, "NSXCheckVersion", func(_ *nsx.Client, _ int) bool { return false })
			defer patches.Reset()
			obj := newTestServiceAccount()
			obj.Status.Phase = v1alpha1.NSXServiceAccountPhaseRealized
			require.NoError(t, s.Client.Create(ctx, obj))
			secret := newOwnedTestSecret(obj, map[string][]byte{
				SecretCertName: []byte("originalCert"), SecretKeyName: []byte("originalKey"), CAName: []byte(oldCA),
			})
			require.NoError(t, s.Client.Create(ctx, secret))
			base := s.Client
			reads := 0
			s.Client = interceptor.NewClient(base.(client.WithWatch), interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, o client.Object, opts ...client.GetOption) error {
					if err := c.Get(ctx, key, o, opts...); err != nil {
						return err
					}
					if key == client.ObjectKeyFromObject(secret) {
						reads++
						if reads > 1 {
							tt.mutateSecret(o.(*v1.Secret))
						}
					}
					return nil
				},
			})
			err := s.ValidateAndUpdateRealizedNSXServiceAccount(ctx, obj, []byte(newCA), nil)
			stored := &v1.Secret{}
			require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(secret), stored))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Equal(t, secret.Data, stored.Data)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, []byte(newCA), stored.Data[CAName])
			assert.Equal(t, []byte("newerCert"), stored.Data[SecretCertName])
			assert.Equal(t, []byte("newerKey"), stored.Data[SecretKeyName])
		})
	}
}

func TestNSXServiceAccountService_GetNSXRestoreStatus(t *testing.T) {
	fakeId := "fakeBackupId"
	fakeRestoreEndTime := int64(12345678)
	restoreStatusValueSuccess := mpmodel.GlobalRestoreStatus_VALUE_SUCCESS
	restoreStatusValueRunning := mpmodel.GlobalRestoreStatus_VALUE_RUNNING
	restoreStatusSuccess := mpmodel.GlobalRestoreStatus{
		Value: &restoreStatusValueSuccess,
	}
	restoreStatusRunning := mpmodel.GlobalRestoreStatus{
		Value: &restoreStatusValueRunning,
	}
	tests := []struct {
		name                     string
		prepareFunc              func(*testing.T, *NSXServiceAccountService) *gomonkey.Patches
		expectedNsxRestoreStatus *v1alpha1.NSXRestoreStatus
		expectedErr              error
	}{
		{
			name: "get nsxRestoreStatus success",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.StatusClient, "Get", []gomonkey.OutputCell{{
					Times: 1,
					Values: gomonkey.Params{
						mpmodel.ClusterRestoreStatus{
							Id:             &fakeId,
							Status:         &restoreStatusSuccess,
							RestoreEndTime: &fakeRestoreEndTime,
						},
						nil,
					},
				}})
				return patches
			},
			expectedNsxRestoreStatus: &v1alpha1.NSXRestoreStatus{
				Id:             fakeId,
				Status:         restoreStatusValueSuccess,
				RestoreEndTime: fakeRestoreEndTime,
			},
		},
		{
			name: "get nsxRestoreStatus running",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.StatusClient, "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						mpmodel.ClusterRestoreStatus{
							Id:     &fakeId,
							Status: &restoreStatusRunning,
						},
						nil,
					},
					Times: 1,
				}})
				return patches
			},
			expectedNsxRestoreStatus: &v1alpha1.NSXRestoreStatus{
				Id:     fakeId,
				Status: restoreStatusValueRunning,
			},
		},
		{
			name: "get nsxRestoreStatus error",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.StatusClient, "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						mpmodel.ClusterRestoreStatus{},
						errors.New("fake error getting nsx restore status"),
					},
					Times: 1,
				}})
				return patches
			},
			expectedErr: errors.New("fake error getting nsx restore status"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commonService := newFakeCommonService()
			s := &NSXServiceAccountService{Service: commonService}
			tt.prepareFunc(t, s)
			actualNsxRestoreStatus, err := s.GetNSXRestoreStatus()
			assert.Equal(t, tt.expectedErr, err)
			assert.Equal(t, tt.expectedNsxRestoreStatus, actualNsxRestoreStatus)
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
				assert.NoError(t, s.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{Id: &normalizedClusterName}))
				assert.NoError(t, s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{Name: &normalizedClusterName, Id: &piId, CertificateId: &certId}))
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
				assert.NoError(t, s.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{Id: &normalizedClusterName}))
				assert.NoError(t, s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{Name: &normalizedClusterName, Id: &piId, CertificateId: &certId, Tags: []mpmodel.Tag{{
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
		want    sets.Set[string]
	}{
		{
			name:    "standard",
			piKeys:  []string{"ns1-name1", "ns2-name2"},
			ccpKeys: []string{"ns2-name2", "ns3-name3"},
			want:    sets.New[string]("ns1-name1-uid", "ns2-name2-uid", "ns3-name3-uid"),
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
				assert.NoError(t, s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{
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
				assert.NoError(t, s.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{
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
				assert.NoError(t, s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{
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
				assert.NoError(t, s.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{
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
		want        v1alpha1.NSXProxyEndpoint
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
			want: v1alpha1.NSXProxyEndpoint{
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
			want: v1alpha1.NSXProxyEndpoint{
				Addresses: []v1alpha1.NSXProxyEndpointAddress{{IP: "1.2.3.4"}, {IP: "1.2.3.5"}},
				Ports: []v1alpha1.NSXProxyEndpointPort{
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
			if !tt.wantErr(t, err, "getProxyEndpoints()") {
				return
			}
			assert.Equalf(t, tt.want, got, "getProxyEndpoints()")
		})
	}
}

func TestGenerateNSXServiceAccountConditions(t *testing.T) {
	type args struct {
		existingConditions []metav1.Condition
		generation         int64
		realizedStatus     metav1.ConditionStatus
		realizedReason     string
		message            string
	}
	tests := []struct {
		name string
		args args
		want []metav1.Condition
	}{
		{
			name: "KeepTime",
			args: args{
				existingConditions: []metav1.Condition{{
					Type:               v1alpha1.ConditionTypeRealized,
					Status:             metav1.ConditionTrue,
					ObservedGeneration: 0,
					LastTransitionTime: metav1.Date(2023, time.January, 1, 0, 0, 0, 0, time.UTC),
					Reason:             v1alpha1.ConditionReasonRealizationError,
					Message:            "error",
				}, {
					Type:               "dummy",
					Status:             metav1.ConditionTrue,
					ObservedGeneration: 0,
					LastTransitionTime: metav1.Date(2023, time.January, 1, 0, 0, 1, 0, time.UTC),
					Reason:             "dummy",
					Message:            "dummy",
				}, {
					Type:               "dummy2",
					Status:             metav1.ConditionTrue,
					ObservedGeneration: 0,
					LastTransitionTime: metav1.Date(2023, time.January, 1, 0, 0, 2, 0, time.UTC),
					Reason:             "dummy2",
					Message:            "dummy2",
				}},
				generation:     3,
				realizedStatus: metav1.ConditionTrue,
				realizedReason: v1alpha1.ConditionReasonRealizationSuccess,
				message:        "Success.",
			},
			want: []metav1.Condition{{
				Type:               "dummy",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 0,
				LastTransitionTime: metav1.Date(2023, time.January, 1, 0, 0, 1, 0, time.UTC),
				Reason:             "dummy",
				Message:            "dummy",
			}, {
				Type:               "dummy2",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 0,
				LastTransitionTime: metav1.Date(2023, time.January, 1, 0, 0, 2, 0, time.UTC),
				Reason:             "dummy2",
				Message:            "dummy2",
			}, {
				Type:               v1alpha1.ConditionTypeRealized,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 3,
				LastTransitionTime: metav1.Date(2023, time.January, 1, 0, 0, 0, 0, time.UTC),
				Reason:             v1alpha1.ConditionReasonRealizationSuccess,
				Message:            "Success.",
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, GenerateNSXServiceAccountConditions(tt.args.existingConditions, tt.args.generation, tt.args.realizedStatus, tt.args.realizedReason, tt.args.message), "GenerateNSXServiceAccountConditions(%v, %v, %v, %v, %v)", tt.args.existingConditions, tt.args.generation, tt.args.realizedStatus, tt.args.realizedReason, tt.args.message)
		})
	}
}

func TestIsNSXServiceAccountRealized(t *testing.T) {
	type args struct {
		status *v1alpha1.NSXServiceAccountStatus
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "False",
			args: args{
				status: &v1alpha1.NSXServiceAccountStatus{},
			},
			want: false,
		},
		{
			name: "TrueDeprecated",
			args: args{
				status: &v1alpha1.NSXServiceAccountStatus{
					Phase:      v1alpha1.NSXServiceAccountPhaseRealized,
					Conditions: nil,
				},
			},
			want: true,
		},
		{
			name: "True",
			args: args{
				status: &v1alpha1.NSXServiceAccountStatus{
					Phase: "",
					Conditions: []metav1.Condition{{
						Type:   v1alpha1.ConditionTypeRealized,
						Status: metav1.ConditionTrue,
					}},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, IsNSXServiceAccountRealized(tt.args.status), "IsNSXServiceAccountRealized(%v)", tt.args.status)
		})
	}
}

func TestUpdateConnectionInfoIfNeeded(t *testing.T) {
	tests := []struct {
		name                   string
		nsxServiceAccount      *v1alpha1.NSXServiceAccount
		prepareFunc            func(*testing.T, *NSXServiceAccountService, context.Context)
		expectedProxyEndpoints v1alpha1.NSXProxyEndpoint
		expectNoStatusUpdate   bool
	}{
		{
			name: "update nsx service account with proxy endpoints",
			nsxServiceAccount: &v1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nsxsa1",
					Namespace: "ns1",
				},
				Status: v1alpha1.NSXServiceAccountStatus{
					Phase:       v1alpha1.NSXServiceAccountPhaseRealized,
					NSXManagers: []string{"mgr1:443", "mgr2:443"},
					ProxyEndpoints: v1alpha1.NSXProxyEndpoint{
						Addresses: []v1alpha1.NSXProxyEndpointAddress{
							{
								IP: "1.2.3.4",
							},
						},
						Ports: []v1alpha1.NSXProxyEndpointPort{
							{
								Name:     "rest-api",
								Port:     10081,
								Protocol: "TCP",
							},
							{
								Name:     "nsx-rpc-fwd-proxy",
								Protocol: "TCP",
								Port:     10082,
							},
						},
					},
				},
			},
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, c context.Context) {
				svc := &v1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "with-label",
						Namespace: "any",
						Labels:    map[string]string{"mgmt-proxy.antrea-nsx.vmware.com": "", "dummy": "dummy"},
					},
					Spec: v1.ServiceSpec{
						Ports: []v1.ServicePort{
							{
								Name:     "rest-api",
								Protocol: "TCP",
								Port:     10081,
							},
							{
								Name:     "nsx-rpc-fwd-proxy",
								Protocol: "TCP",
								Port:     10082,
							},
						},
						Type: v1.ServiceTypeLoadBalancer,
					},
					Status: v1.ServiceStatus{
						LoadBalancer: v1.LoadBalancerStatus{
							Ingress: []v1.LoadBalancerIngress{{IP: "1.2.3.5"}},
						},
					},
				}
				assert.NoError(t, s.Client.Create(c, svc))
			},
			expectedProxyEndpoints: v1alpha1.NSXProxyEndpoint{
				Addresses: []v1alpha1.NSXProxyEndpointAddress{
					{
						IP: "1.2.3.5",
					},
				},
				Ports: []v1alpha1.NSXProxyEndpointPort{
					{
						Name:     "rest-api",
						Port:     10081,
						Protocol: "TCP",
					},
					{
						Name:     "nsx-rpc-fwd-proxy",
						Protocol: "TCP",
						Port:     10082,
					},
				},
			},
		},
		{
			name: "update stale manager addresses",
			nsxServiceAccount: &v1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "account",
					Namespace: "ns1",
				},
				Status: v1alpha1.NSXServiceAccountStatus{
					Phase:       v1alpha1.NSXServiceAccountPhaseRealized,
					NSXManagers: []string{"old-manager:443"},
					ClusterID:   "existing-cluster",
					Secrets:     []v1alpha1.NSXSecret{{Name: "existing-secret", Namespace: "ns1"}},
				},
			},
		},
		{
			name:                 "keep unchanged manager addresses",
			expectNoStatusUpdate: true,
			nsxServiceAccount: &v1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "account",
					Namespace: "ns1",
				},
				Status: v1alpha1.NSXServiceAccountStatus{
					Phase:       v1alpha1.NSXServiceAccountPhaseRealized,
					NSXManagers: []string{"mgr1:443", "mgr2:443"},
					ClusterID:   "existing-cluster",
					Secrets:     []v1alpha1.NSXSecret{{Name: "existing-secret", Namespace: "ns1"}},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()
			commonService := newFakeCommonService()
			s := &NSXServiceAccountService{Service: commonService}
			require.NoError(t, s.Client.Create(ctx, tt.nsxServiceAccount))
			if tt.prepareFunc != nil {
				tt.prepareFunc(t, s, ctx)
			}
			expectedStatus := tt.nsxServiceAccount.DeepCopy().Status
			expectedStatus.NSXManagers = s.NSXConfig.NsxApiManagers
			expectedStatus.ProxyEndpoints = tt.expectedProxyEndpoints

			resourceVersion := tt.nsxServiceAccount.ResourceVersion
			require.NoError(t, s.UpdateConnectionInfoIfNeeded(ctx, tt.nsxServiceAccount))
			actual := &v1alpha1.NSXServiceAccount{}
			key := types.NamespacedName{Name: tt.nsxServiceAccount.Name, Namespace: tt.nsxServiceAccount.Namespace}
			require.NoError(t, s.Client.Get(ctx, key, actual))
			assert.Equal(t, expectedStatus, actual.Status)
			if tt.expectNoStatusUpdate {
				assert.Equal(t, resourceVersion, actual.ResourceVersion)
			}
		})
	}
}

func TestCleanupBeforeVPCDeletion(t *testing.T) {
	tests := []struct {
		name        string
		prepareFunc func(*testing.T, *NSXServiceAccountService, context.Context) *gomonkey.Patches
		expectedErr string
	}{
		{
			name: "Cleanup with no cluster control plane found",
		},
		{
			name: "Cleanup with no errors",
			prepareFunc: func(t *testing.T, nsxsaService *NSXServiceAccountService, ctx context.Context) *gomonkey.Patches {
				ccp1ID := "antreacluster"
				ccp2ID := "kindcluster"
				assert.NoError(t, nsxsaService.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{Id: &ccp1ID}))
				assert.NoError(t, nsxsaService.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{Id: &ccp2ID}))
				patches := gomonkey.ApplyMethodSeq(nsxsaService.NSXClient.ClusterControlPlanesClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  2,
				}})
				return patches
			},
		},
		{
			name: "Cleanup with cluster control plane deletion error",
			prepareFunc: func(t *testing.T, nsxsaService *NSXServiceAccountService, ctx context.Context) *gomonkey.Patches {
				ccpID := "antreacluster"
				assert.NoError(t, nsxsaService.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{Id: &ccpID}))
				patches := gomonkey.ApplyMethodSeq(nsxsaService.NSXClient.ClusterControlPlanesClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{errors.New("ccp deletion error")},
					Times:  1,
				}})
				return patches
			},
			expectedErr: "ccp deletion error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commonService := newFakeCommonService()
			nsxsaService := &NSXServiceAccountService{Service: commonService}
			nsxsaService.SetUpStore()
			ctx := context.TODO()
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc(t, nsxsaService, ctx)
				defer patches.Reset()
			}

			err := nsxsaService.CleanupBeforeVPCDeletion(ctx)
			if tt.expectedErr == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.expectedErr)
			}
		})
	}
}
