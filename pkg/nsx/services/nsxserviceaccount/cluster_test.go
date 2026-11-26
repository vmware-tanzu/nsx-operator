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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
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
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
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
					Cluster: "k8scl-one-test",
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
				Cluster: "k8scl-one-test",
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

// createInvalidOwnedSecret creates an owned Secret with credentials that need regeneration.
func createInvalidOwnedSecret(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) {
	assert.NoError(t, s.Client.Create(ctx, &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      obj.Name + SecretSuffix,
			Namespace: obj.Namespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "nsx.vmware.com/v1alpha1",
				Kind:       "NSXServiceAccount",
				Name:       obj.Name,
				UID:        obj.UID,
			}},
		},
		Data: map[string][]byte{SecretCertName: []byte("old-cert"), SecretKeyName: []byte("old-key"), CAName: []byte("old-ca")},
	}))
}

// verifyRefreshedSecret checks the owned Secret is updated in place with new credentials and its existing CA.
func verifyRefreshedSecret(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) {
	secret := &v1.Secret{}
	assert.NoError(t, s.Client.Get(ctx, types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name + SecretSuffix}, secret))
	assert.NotEqual(t, "old-cert", string(secret.Data[SecretCertName]))
	assert.NotEqual(t, "old-key", string(secret.Data[SecretKeyName]))
	assert.Equal(t, "old-ca", string(secret.Data[CAName]))
	assert.Equal(t, 1, len(secret.OwnerReferences))
	assert.Equal(t, obj.Name, secret.OwnerReferences[0].Name)
	assert.Equal(t, obj.UID, secret.OwnerReferences[0].UID)
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
		verifyFunc  func(*testing.T, *NSXServiceAccountService, context.Context, *v1alpha1.NSXServiceAccount)
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
				normalizedClusterName := "k8scl-one-test_ns1_name1"
				vpcPath := "/orgs/default/projects/k8scl-one-test/vpcs/vpc1"
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
					VPCPath:        "/orgs/default/projects/k8scl-one-test/vpcs/ns1-default-vpc",
					NSXManagers:    []string{"mgr1:443", "mgr2:443"},
					ProxyEndpoints: v1alpha1.NSXProxyEndpoint{},
					ClusterID:      "clusterId1",
					ClusterName:    "k8scl-one-test_ns1_name1",
					Secrets:        []v1alpha1.NSXSecret{{Name: "name1-nsx-cert", Namespace: "ns1"}},
				},
			},
		},
		{
			name: "Success",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				assert.NoError(t, s.Client.Create(ctx, obj))
				normalizedClusterName := "k8scl-one-test_ns1_name1"
				vpcPath := "/orgs/default/projects/k8scl-one-test/vpcs/vpc1"
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
					VPCPath:        "/orgs/default/projects/k8scl-one-test/vpcs/ns1-default-vpc",
					NSXManagers:    []string{"mgr1:443", "mgr2:443"},
					ProxyEndpoints: v1alpha1.NSXProxyEndpoint{},
					ClusterID:      "clusterId1",
					ClusterName:    "k8scl-one-test_ns1_name1",
					Secrets:        []v1alpha1.NSXSecret{{Name: "name1-nsx-cert", Namespace: "ns1"}},
				},
			},
		},
		{
			name: "Success with VMCIProxy",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				assert.NoError(t, s.Client.Create(ctx, obj))
				normalizedClusterName := "k8scl-one-test_ns1_name1"
				vpcPath := "/orgs/default/projects/k8scl-one-test/vpcs/vpc1"
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
						Proxy:   v1alpha1.VMCIProxy,
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
					Proxy:   v1alpha1.VMCIProxy,
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
					VPCPath:     "/orgs/default/projects/k8scl-one-test/vpcs/ns1-default-vpc",
					NSXManagers: []string{"mgr1:443", "mgr2:443"},
					ProxyEndpoints: v1alpha1.NSXProxyEndpoint{
						Addresses: []v1alpha1.NSXProxyEndpointAddress{
							{
								IP: "127.0.0.1",
							},
						},
						Ports: []v1alpha1.NSXProxyEndpointPort{
							{
								Name:     PortRestAPI,
								Port:     PortNumRestAPI,
								Protocol: v1alpha1.NSXProxyProtocolTCP,
							},
							{
								Name:     PortNSXRPCFwdProxy,
								Port:     PortNumNSXRPCFwdProxy,
								Protocol: v1alpha1.NSXProxyProtocolTCP,
							},
						},
					},
					ClusterID:   "clusterId1",
					ClusterName: "k8scl-one-test_ns1_name1",
					Secrets:     []v1alpha1.NSXSecret{{Name: "name1-nsx-cert", Namespace: "ns1"}},
				},
			},
		},
		{
			name: "LongNameSuccess",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				assert.NoError(t, s.Client.Create(ctx, obj))
				s.NSXConfig.CoeConfig.Cluster = "k8scl-one:1234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890"
				normalizedClusterName := "k8scl-one_12345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456-2d5b8ae2"
				vpcPath := "/orgs/default/projects/k8scl-one-test/vpcs/ns1-default-vpc"
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
					ClusterName:    "k8scl-one_12345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456-2d5b8ae2",
					Secrets:        []v1alpha1.NSXSecret{{Name: "name1-nsx-cert", Namespace: "ns1"}},
				},
			},
		},
		{
			name: "Do not change existing clusterName",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				assert.NoError(t, s.Client.Create(ctx, obj))
				normalizedClusterName := "k8scl-one-test-ns1-name1"
				vpcPath := "/orgs/default/projects/k8scl-one-test/vpcs/vpc1"
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
				// Legacy CCP (old "-" delimiter) already exists on NSX; its name must be reused even
				// though Status.ClusterName is set to something else by the user.
				assert.NoError(t, s.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{
					Id: &normalizedClusterName,
					Tags: []model.Tag{{
						Scope: &tagScopeNSXServiceAccountCRUID,
						Tag:   &uid,
					}},
				}))
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
					Status: v1alpha1.NSXServiceAccountStatus{
						ClusterName: "tampered-by-user",
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
					VPCPath:        "/orgs/default/projects/k8scl-one-test/vpcs/ns1-default-vpc",
					NSXManagers:    []string{"mgr1:443", "mgr2:443"},
					ProxyEndpoints: v1alpha1.NSXProxyEndpoint{},
					ClusterID:      "clusterId1",
					ClusterName:    "k8scl-one-test-ns1-name1",
					Secrets:        []v1alpha1.NSXSecret{{Name: "name1-nsx-cert", Namespace: "ns1"}},
				},
			},
		},
		{
			name: "Success with existing Secret",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				assert.NoError(t, s.Client.Create(ctx, obj))
				createInvalidOwnedSecret(t, s, ctx, obj)
				normalizedClusterName := "k8scl-one-test_ns1_name1"
				vpcPath := "/orgs/default/projects/k8scl-one-test/vpcs/ns1-default-vpc"
				piId := "Id1"
				nodeId := "clusterId1"
				uid := "00000000-0000-0000-0000-000000000001"
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.WithCertificateClient, "Create", []gomonkey.OutputCell{{
					Values: gomonkey.Params{mpmodel.PrincipalIdentity{
						Name: &normalizedClusterName,
						Id:   &piId,
						Tags: []mpmodel.Tag{{
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid,
						}},
					}, nil},
					Times: 1,
				}})
				patches.ApplyMethodSeq(s.NSXClient.ClusterControlPlanesClient, "Update", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.ClusterControlPlane{
						Id:      &normalizedClusterName,
						NodeId:  &nodeId,
						VhcPath: &vpcPath,
						Tags: []model.Tag{{
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
					VPCPath:        "/orgs/default/projects/k8scl-one-test/vpcs/ns1-default-vpc",
					NSXManagers:    []string{"mgr1:443", "mgr2:443"},
					ProxyEndpoints: v1alpha1.NSXProxyEndpoint{},
					ClusterID:      "clusterId1",
					ClusterName:    "k8scl-one-test_ns1_name1",
					Secrets:        []v1alpha1.NSXSecret{{Name: "name1-nsx-cert", Namespace: "ns1"}},
				},
			},
			verifyFunc: verifyRefreshedSecret,
		},
		{
			name: "Success with populated status and existing Secret",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				assert.NoError(t, s.Client.Create(ctx, obj))
				createInvalidOwnedSecret(t, s, ctx, obj)
				// Tampered Status.ClusterName/VPCPath (see args) must be ignored; the identity is derived from config.
				clusterName := "k8scl-one-test-ns1-name1"
				vpcPath := "/orgs/default/projects/k8scl-one-test/vpcs/ns1-default-vpc"
				piId := "Id1"
				certId1 := "certId1"
				certId2 := "certId2"
				nodeId := "clusterId1"
				uid := "00000000-0000-0000-0000-000000000001"
				oldCert := "old-cert"
				assert.NoError(t, s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{
					Name:          &clusterName,
					Id:            &piId,
					CertificateId: &certId1,
					Tags: []mpmodel.Tag{{
						Scope: &tagScopeNSXServiceAccountCRUID,
						Tag:   &uid,
					}},
				}))
				assert.NoError(t, s.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{
					Id:          &clusterName,
					NodeId:      &nodeId,
					VhcPath:     &vpcPath,
					Certificate: &oldCert,
					Tags: []model.Tag{{
						Scope: &tagScopeNSXServiceAccountCRUID,
						Tag:   &uid,
					}},
				}))
				// Only the existing PI/CCP certificates are updated, no PI/CCP is created.
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.ClusterControlPlanesClient, "Update", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.ClusterControlPlane{
						Id:      &clusterName,
						NodeId:  &nodeId,
						VhcPath: &vpcPath,
						Tags: []model.Tag{{
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid,
						}},
					}, nil},
					Times: 1,
				}})
				patches.ApplyMethodSeq(s.NSXClient.CertificatesClient, "Importcertificate", []gomonkey.OutputCell{{
					Values: gomonkey.Params{mpmodel.CertificateList{Results: []mpmodel.Certificate{{Id: &certId2}}}, nil},
					Times:  1,
				}})
				patches.ApplyMethodSeq(s.NSXClient.PrincipalIdentitiesClient, "Updatecertificate", []gomonkey.OutputCell{{
					Values: gomonkey.Params{mpmodel.PrincipalIdentity{
						Name:          &clusterName,
						Id:            &piId,
						CertificateId: &certId2,
						Tags: []mpmodel.Tag{{
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
					Status: v1alpha1.NSXServiceAccountStatus{
						Phase:  v1alpha1.NSXServiceAccountPhaseFailed,
						Reason: "Error: mock error",
						Conditions: []metav1.Condition{{
							Type:    v1alpha1.ConditionTypeRealized,
							Status:  metav1.ConditionFalse,
							Reason:  v1alpha1.ConditionReasonRealizationError,
							Message: "Error: mock error",
						}},
						// ClusterName and VPCPath are deliberately tampered; they must be ignored.
						VPCPath:     "/orgs/default/projects/previous-project/vpcs/previous-vpc",
						NSXManagers: []string{"previous-mgr:443"},
						ClusterID:   "clusterId1",
						ClusterName: "previous-cluster-name",
						Secrets:     []v1alpha1.NSXSecret{{Name: "name1-nsx-cert", Namespace: "ns1"}},
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
					VPCPath:        "/orgs/default/projects/k8scl-one-test/vpcs/ns1-default-vpc",
					NSXManagers:    []string{"mgr1:443", "mgr2:443"},
					ProxyEndpoints: v1alpha1.NSXProxyEndpoint{},
					ClusterID:      "clusterId1",
					ClusterName:    "k8scl-one-test-ns1-name1",
					Secrets:        []v1alpha1.NSXSecret{{Name: "name1-nsx-cert", Namespace: "ns1"}},
				},
			},
			verifyFunc: verifyRefreshedSecret,
		},
		{
			// e.g. NSX manager restored to a state without the PI/CCP of a non-realized NSXServiceAccount.
			name: "Recreate missing PI and CCP with the ClusterID recorded in status",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				assert.NoError(t, s.Client.Create(ctx, obj))
				clusterName := "k8scl-one-test_ns1_name1"
				piId := "Id1"
				nodeId := "clusterId1"
				uid := "00000000-0000-0000-0000-000000000001"
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.WithCertificateClient, "Create", []gomonkey.OutputCell{{
					Values: gomonkey.Params{mpmodel.PrincipalIdentity{
						Name: &clusterName,
						Id:   &piId,
						Tags: []mpmodel.Tag{{Scope: &tagScopeNSXServiceAccountCRUID, Tag: &uid}},
					}, nil},
					Times: 1,
				}})
				// The recorded ClusterID must be handed to NSX as the CCP node ID.
				patches.ApplyMethodFunc(s.NSXClient.ClusterControlPlanesClient, "Update",
					func(_, _, id string, ccp model.ClusterControlPlane) (model.ClusterControlPlane, error) {
						assert.Equal(t, clusterName, id)
						if assert.NotNil(t, ccp.NodeId) {
							assert.Equal(t, nodeId, *ccp.NodeId)
						}
						ccp.Id = &id
						return ccp, nil
					})
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
					Status: v1alpha1.NSXServiceAccountStatus{
						Phase:       v1alpha1.NSXServiceAccountPhaseFailed,
						VPCPath:     "/orgs/default/projects/previous-project/vpcs/previous-vpc",
						ClusterID:   "clusterId1",
						ClusterName: "previous-cluster-name",
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
				Status: v1alpha1.NSXServiceAccountStatus{
					Phase:  "realized",
					Reason: "Success",
					Conditions: []metav1.Condition{{
						Type:    v1alpha1.ConditionTypeRealized,
						Status:  metav1.ConditionTrue,
						Reason:  v1alpha1.ConditionReasonRealizationSuccess,
						Message: "Success.",
					}},
					VPCPath:        "/orgs/default/projects/k8scl-one-test/vpcs/ns1-default-vpc",
					NSXManagers:    []string{"mgr1:443", "mgr2:443"},
					ProxyEndpoints: v1alpha1.NSXProxyEndpoint{},
					ClusterID:      "clusterId1",
					ClusterName:    "k8scl-one-test_ns1_name1",
					Secrets:        []v1alpha1.NSXSecret{{Name: "name1-nsx-cert", Namespace: "ns1"}},
				},
			},
		},
		{
			// PI/CCP exist but status has no ClusterID, e.g. they were created and the status update then failed.
			// The case where status keeps its ClusterID is covered by "Success with populated status and existing Secret".
			name: "Existing PI and CCP with empty ClusterID in status reports the existing node ID",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				assert.NoError(t, s.Client.Create(ctx, obj))
				clusterName := "k8scl-one-test-ns1-name1"
				vpcPath := "/orgs/default/projects/k8scl-one-test/vpcs/ns1-default-vpc"
				piId := "Id1"
				certId1 := "certId1"
				certId2 := "certId2"
				nodeId := "clusterId1"
				uid := "00000000-0000-0000-0000-000000000001"
				assert.NoError(t, s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{
					Name:          &clusterName,
					Id:            &piId,
					CertificateId: &certId1,
					Tags:          []mpmodel.Tag{{Scope: &tagScopeNSXServiceAccountCRUID, Tag: &uid}},
				}))
				assert.NoError(t, s.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{
					Id:      &clusterName,
					NodeId:  &nodeId,
					VhcPath: &vpcPath,
					Tags:    []model.Tag{{Scope: &tagScopeNSXServiceAccountCRUID, Tag: &uid}},
				}))
				// The response carries no NodeId, so the stored one must be used.
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.ClusterControlPlanesClient, "Update", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.ClusterControlPlane{
						Id:      &clusterName,
						VhcPath: &vpcPath,
						Tags:    []model.Tag{{Scope: &tagScopeNSXServiceAccountCRUID, Tag: &uid}},
					}, nil},
					Times: 1,
				}})
				patches.ApplyMethodSeq(s.NSXClient.CertificatesClient, "Importcertificate", []gomonkey.OutputCell{{
					Values: gomonkey.Params{mpmodel.CertificateList{Results: []mpmodel.Certificate{{Id: &certId2}}}, nil},
					Times:  1,
				}})
				patches.ApplyMethodSeq(s.NSXClient.PrincipalIdentitiesClient, "Updatecertificate", []gomonkey.OutputCell{{
					Values: gomonkey.Params{mpmodel.PrincipalIdentity{
						Name:          &clusterName,
						Id:            &piId,
						CertificateId: &certId2,
						Tags:          []mpmodel.Tag{{Scope: &tagScopeNSXServiceAccountCRUID, Tag: &uid}},
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
				Status: v1alpha1.NSXServiceAccountStatus{
					Phase:  "realized",
					Reason: "Success",
					Conditions: []metav1.Condition{{
						Type:    v1alpha1.ConditionTypeRealized,
						Status:  metav1.ConditionTrue,
						Reason:  v1alpha1.ConditionReasonRealizationSuccess,
						Message: "Success.",
					}},
					VPCPath:        "/orgs/default/projects/k8scl-one-test/vpcs/ns1-default-vpc",
					NSXManagers:    []string{"mgr1:443", "mgr2:443"},
					ProxyEndpoints: v1alpha1.NSXProxyEndpoint{},
					ClusterID:      "clusterId1",
					ClusterName:    "k8scl-one-test-ns1-name1",
					Secrets:        []v1alpha1.NSXSecret{{Name: "name1-nsx-cert", Namespace: "ns1"}},
				},
			},
			// The node ID missing from the NSX response must be backfilled in the store.
			verifyFunc: func(t *testing.T, s *NSXServiceAccountService, _ context.Context, _ *v1alpha1.NSXServiceAccount) {
				ccp := s.ClusterControlPlaneStore.GetByKey("k8scl-one-test-ns1-name1")
				if assert.NotNil(t, ccp) && assert.NotNil(t, ccp.(*model.ClusterControlPlane).NodeId) {
					assert.Equal(t, "clusterId1", *ccp.(*model.ClusterControlPlane).NodeId)
				}
			},
		},
		{
			// Simulates NSX omitting NodeId from the CCP response on recreation; the recorded ClusterID must
			// still be reported and stored.
			name: "Recreate missing PI and CCP when NSX response has no NodeId",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context, obj *v1alpha1.NSXServiceAccount) *gomonkey.Patches {
				assert.NoError(t, s.Client.Create(ctx, obj))
				clusterName := "k8scl-one-test_ns1_name1"
				piId := "Id1"
				uid := "00000000-0000-0000-0000-000000000001"
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.WithCertificateClient, "Create", []gomonkey.OutputCell{{
					Values: gomonkey.Params{mpmodel.PrincipalIdentity{
						Name: &clusterName,
						Id:   &piId,
						Tags: []mpmodel.Tag{{Scope: &tagScopeNSXServiceAccountCRUID, Tag: &uid}},
					}, nil},
					Times: 1,
				}})
				patches.ApplyMethodFunc(s.NSXClient.ClusterControlPlanesClient, "Update",
					func(_, _, id string, ccp model.ClusterControlPlane) (model.ClusterControlPlane, error) {
						ccp.Id = &id
						ccp.NodeId = nil
						return ccp, nil
					})
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
					Status: v1alpha1.NSXServiceAccountStatus{
						Phase:     v1alpha1.NSXServiceAccountPhaseFailed,
						ClusterID: "clusterId1",
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
				Status: v1alpha1.NSXServiceAccountStatus{
					Phase:  "realized",
					Reason: "Success",
					Conditions: []metav1.Condition{{
						Type:    v1alpha1.ConditionTypeRealized,
						Status:  metav1.ConditionTrue,
						Reason:  v1alpha1.ConditionReasonRealizationSuccess,
						Message: "Success.",
					}},
					VPCPath:        "/orgs/default/projects/k8scl-one-test/vpcs/ns1-default-vpc",
					NSXManagers:    []string{"mgr1:443", "mgr2:443"},
					ProxyEndpoints: v1alpha1.NSXProxyEndpoint{},
					ClusterID:      "clusterId1",
					ClusterName:    "k8scl-one-test_ns1_name1",
					Secrets:        []v1alpha1.NSXSecret{{Name: "name1-nsx-cert", Namespace: "ns1"}},
				},
			},
			verifyFunc: func(t *testing.T, s *NSXServiceAccountService, _ context.Context, _ *v1alpha1.NSXServiceAccount) {
				ccp := s.ClusterControlPlaneStore.GetByKey("k8scl-one-test_ns1_name1")
				if assert.NotNil(t, ccp) && assert.NotNil(t, ccp.(*model.ClusterControlPlane).NodeId) {
					assert.Equal(t, "clusterId1", *ccp.(*model.ClusterControlPlane).NodeId)
				}
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
				expectedKeys := []string{tt.expectedCR.Status.ClusterName}
				assert.Equal(t, expectedKeys, s.PrincipalIdentityStore.ListKeys())
				assert.Equal(t, expectedKeys, s.ClusterControlPlaneStore.ListKeys())
			}
			if tt.verifyFunc != nil {
				tt.verifyFunc(t, s, ctx, tt.args.obj)
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
	name := util.NormalizeId(s.getClusterName(string(obj.UID), obj.Namespace, obj.Name))
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
	subject.CommonName = "k8scl-one-test_ns1_name1"
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
	subject.CommonName = util.NormalizeId(s.getClusterName(string(obj.UID), obj.Namespace, obj.Name))
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
	name := util.NormalizeId(s.getClusterName(string(obj.UID), obj.Namespace, obj.Name))
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
	// The CCP already holds the certificate, and NSX rejects an update carrying it.
	assert.Empty(t, captured.ccp, "the CCP must not be written again with its own certificate")
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
				normalizedClusterName := "k8scl-one-test_ns1_name1"
				vpcPath := "/orgs/default/projects/k8scl-one-test/vpcs/vpc1"
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
						VPCPath:     "/orgs/default/projects/k8scl-one-test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one-test_ns1_name1",
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
				normalizedClusterName := "k8scl-one-test_ns1_name1"
				vpcPath := "/orgs/default/projects/k8scl-one-test/vpcs/vpc1"
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
				subject.CommonName = "k8scl-one-test_ns1_name1"
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
						VPCPath:     "/orgs/default/projects/k8scl-one-test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one-test_ns1_name1",
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
				normalizedClusterName := "k8scl-one-test_ns1_name1"
				vpcPath := "/orgs/default/projects/k8scl-one-test/vpcs/vpc1"
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
						VPCPath:     "/orgs/default/projects/k8scl-one-test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one-test_ns1_name1",
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
						VPCPath:     "/orgs/default/projects/k8scl-one-test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one-test_ns1_name1",
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
						VPCPath:     "/orgs/default/projects/k8scl-one-test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one-test_ns1_name1",
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
						VPCPath:     "/orgs/default/projects/k8scl-one-test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one-test_ns1_name1",
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
				normalizedClusterName := "k8scl-one-test_ns1_name1"
				vpcPath := "/orgs/default/projects/k8scl-one-test/vpcs/vpc1"
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
						VPCPath:     "/orgs/default/projects/k8scl-one-test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one-test_ns1_name1",
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
	subject.CommonName = "k8scl-one-test_ns1_name1"
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
						VPCPath:     "/orgs/default/projects/k8scl-one-test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one-test_ns1_name1",
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
						VPCPath:     "/orgs/default/projects/k8scl-one-test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one-test_ns1_name1",
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
						VPCPath:     "/orgs/default/projects/k8scl-one-test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one-test_ns1_name1",
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

				normalizedClusterName := "k8scl-one-test_ns1_name1"
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
						VPCPath:     "/orgs/default/projects/k8scl-one-test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one-test_ns1_name1",
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
						VPCPath:     "/orgs/default/projects/k8scl-one-test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one-test_ns1_name1",
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
						VPCPath:     "/orgs/default/projects/k8scl-one-test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one-test_ns1_name1",
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
						VPCPath:     "/orgs/default/projects/k8scl-one-test/vpcs/vpc1",
						ClusterID:   "clusterId1",
						ClusterName: "k8scl-one-test_ns1_name1",
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
			s.SetUpStore() // getClusterName now looks up PI/CCP stores by UID
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
	name1 := "name1"
	namespace1 := "ns1"
	uid1 := "uid1"
	piId := "piId1"
	certId := "certId1"

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
				normalizedClusterName := s.getClusterName("", namespace1, name1)
				assert.NoError(t, s.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{
					Id: &normalizedClusterName,
					Tags: []model.Tag{
						{
							Scope: &tagScopeNamespace,
							Tag:   &namespace1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRName,
							Tag:   &name1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid1,
						},
					},
				}))
				assert.NoError(t, s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{
					Name:          &normalizedClusterName,
					Id:            &piId,
					CertificateId: &certId,
					Tags: []mpmodel.Tag{
						{
							Scope: &tagScopeNamespace,
							Tag:   &namespace1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRName,
							Tag:   &name1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid1,
						},
					},
				}))
				assert.NoError(t, s.Client.Create(ctx, &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name1,
						Namespace: namespace1,
						UID:       types.UID(uid1),
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
					Namespace: namespace1,
					Name:      name1,
				},
				uid: types.UID(uid1),
			},
			wantErr:                           false,
			wantClusterControlPlaneStoreCount: 0,
			wantPrincipalIdentityStoreCount:   0,
		},
		{
			name: "success with clusterName set in NSXSA",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context) *gomonkey.Patches {
				clusterName := "k8scl-one-test-ns1-name1"
				normalizedClusterName := clusterName
				assert.NoError(t, s.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{
					Id: &normalizedClusterName,
					Tags: []model.Tag{
						{
							Scope: &tagScopeNamespace,
							Tag:   &namespace1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRName,
							Tag:   &name1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid1,
						},
					},
				}))
				assert.NoError(t, s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{
					Name:          &normalizedClusterName,
					Id:            &piId,
					CertificateId: &certId,
					Tags: []mpmodel.Tag{
						{
							Scope: &tagScopeNamespace,
							Tag:   &namespace1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRName,
							Tag:   &name1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid1,
						},
					},
				}))
				assert.NoError(t, s.Client.Create(ctx, &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name1,
						Namespace: namespace1,
						UID:       types.UID(uid1),
					},
					Status: v1alpha1.NSXServiceAccountStatus{
						ClusterName: clusterName,
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
					Namespace: namespace1,
					Name:      name1,
				},
				uid: types.UID(uid1),
			},
			wantErr:                           false,
			wantClusterControlPlaneStoreCount: 0,
			wantPrincipalIdentityStoreCount:   0,
		},
		{
			name: "errorDeletePIDifferentUID",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context) *gomonkey.Patches {
				normalizedClusterName := s.getClusterName("", namespace1, name1)
				assert.NoError(t, s.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{
					Id: &normalizedClusterName,
					Tags: []model.Tag{
						{
							Scope: &tagScopeNamespace,
							Tag:   &namespace1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRName,
							Tag:   &name1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid1,
						},
					},
				}))
				assert.NoError(t, s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{
					Name:          &normalizedClusterName,
					Id:            &piId,
					CertificateId: &certId,
					Tags: []mpmodel.Tag{
						{
							Scope: &tagScopeNamespace,
							Tag:   &namespace1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRName,
							Tag:   &name1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid1,
						},
					},
				}))
				assert.NoError(t, s.Client.Create(ctx, &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name1,
						Namespace: namespace1,
						UID:       "uid2",
					},
				}))

				patches := gomonkey.ApplyMethodSeq(s.NSXClient.ClusterControlPlanesClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				patches.ApplyMethodSeq(s.NSXClient.PrincipalIdentitiesClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("mock error")},
					Times:  1,
				}})
				return patches
			},
			args: args{
				namespacedName: types.NamespacedName{
					Namespace: namespace1,
					Name:      name1,
				},
				uid: types.UID(uid1),
			},
			wantErr:                           true,
			wantClusterControlPlaneStoreCount: 0,
			wantPrincipalIdentityStoreCount:   1,
		},
		{
			name: "errorDeleteCCPDifferentUID",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context) *gomonkey.Patches {
				normalizedClusterName := s.getClusterName("", namespace1, name1)
				assert.NoError(t, s.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{
					Id: &normalizedClusterName,
					Tags: []model.Tag{
						{
							Scope: &tagScopeNamespace,
							Tag:   &namespace1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRName,
							Tag:   &name1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid1,
						},
					},
				}))
				assert.NoError(t, s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{
					Name:          &normalizedClusterName,
					Id:            &piId,
					CertificateId: &certId,
					Tags: []mpmodel.Tag{
						{
							Scope: &tagScopeNamespace,
							Tag:   &namespace1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRName,
							Tag:   &name1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid1,
						},
					},
				}))
				assert.NoError(t, s.Client.Create(ctx, &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name1,
						Namespace: namespace1,
						UID:       "uid2",
					},
				}))

				patches := gomonkey.ApplyMethodSeq(s.NSXClient.ClusterControlPlanesClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("mock error")},
					Times:  1,
				}})
				return patches
			},
			args: args{
				namespacedName: types.NamespacedName{
					Namespace: namespace1,
					Name:      name1,
				},
				uid: types.UID(uid1),
			},
			wantErr:                           true,
			wantClusterControlPlaneStoreCount: 1,
			wantPrincipalIdentityStoreCount:   1,
		},
		{
			name: "errorGetCCPByUID",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context) *gomonkey.Patches {
				normalizedClusterName := s.getClusterName("", namespace1, name1)
				assert.NoError(t, s.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{
					Id: &normalizedClusterName,
					Tags: []model.Tag{
						{
							Scope: &tagScopeNamespace,
							Tag:   &namespace1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRName,
							Tag:   &name1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid1,
						},
					},
				}))
				assert.NoError(t, s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{
					Name:          &normalizedClusterName,
					Id:            &piId,
					CertificateId: &certId,
					Tags: []mpmodel.Tag{
						{
							Scope: &tagScopeNamespace,
							Tag:   &namespace1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRName,
							Tag:   &name1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid1,
						},
					},
				}))
				assert.NoError(t, s.Client.Create(ctx, &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name1,
						Namespace: namespace1,
						UID:       "uid2",
					},
				}))

				patches := gomonkey.ApplyPrivateMethod(s, "getClusterControlPlaneByNSXServiceAccountUID", func(uid string) (*model.ClusterControlPlane, error) {
					return nil, fmt.Errorf("mock error")
				})
				return patches
			},
			args: args{
				namespacedName: types.NamespacedName{
					Namespace: namespace1,
					Name:      name1,
				},
				uid: types.UID(uid1),
			},
			wantErr:                           true,
			wantClusterControlPlaneStoreCount: 1,
			wantPrincipalIdentityStoreCount:   1,
		},
		{
			name: "errorGetPIByUID",
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService, ctx context.Context) *gomonkey.Patches {
				normalizedClusterName := s.getClusterName("", namespace1, name1)
				assert.NoError(t, s.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{
					Id: &normalizedClusterName,
					Tags: []model.Tag{
						{
							Scope: &tagScopeNamespace,
							Tag:   &namespace1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRName,
							Tag:   &name1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid1,
						},
					},
				}))
				assert.NoError(t, s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{
					Name:          &normalizedClusterName,
					Id:            &piId,
					CertificateId: &certId,
					Tags: []mpmodel.Tag{
						{
							Scope: &tagScopeNamespace,
							Tag:   &namespace1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRName,
							Tag:   &name1,
						},
						{
							Scope: &tagScopeNSXServiceAccountCRUID,
							Tag:   &uid1,
						},
					},
				}))
				assert.NoError(t, s.Client.Create(ctx, &v1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name1,
						Namespace: namespace1,
						UID:       "uid2",
					},
				}))

				patches := gomonkey.ApplyPrivateMethod(s, "getPrincipalIdentityByNSXServiceAccountUID", func(uid string) (*mpmodel.PrincipalIdentity, error) {
					return nil, fmt.Errorf("mock error")
				})
				return patches
			},
			args: args{
				namespacedName: types.NamespacedName{
					Namespace: namespace1,
					Name:      name1,
				},
				uid: types.UID(uid1),
			},
			wantErr:                           true,
			wantClusterControlPlaneStoreCount: 0,
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
		prepareFunc        func(*testing.T, *NSXServiceAccountService) *gomonkey.Patches
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
		{
			name: "ErrorGetPI",
			piKeys: []types.NamespacedName{{
				Namespace: "ns1",
				Name:      "name1",
			}},
			ccpKeys: []types.NamespacedName{},
			args: args{
				uid: "ns1-name1-uid",
			},
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(s, "getPrincipalIdentityByNSXServiceAccountUID", func(uid string) (*mpmodel.PrincipalIdentity, error) {
					return nil, fmt.Errorf("mock error")
				})
				return patches
			},
			wantNamespacedName: types.NamespacedName{
				Namespace: "",
				Name:      "",
			},
		},
		{
			name:   "ErrorGetCCP",
			piKeys: []types.NamespacedName{},
			ccpKeys: []types.NamespacedName{{
				Namespace: "ns1",
				Name:      "name1",
			}},
			args: args{
				uid: "ns1-name1-uid",
			},
			prepareFunc: func(t *testing.T, s *NSXServiceAccountService) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(s, "getClusterControlPlaneByNSXServiceAccountUID", func(uid string) (*model.ClusterControlPlane, error) {
					return nil, fmt.Errorf("mock error")
				})
				return patches
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
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc(t, s)
				defer patches.Reset()
			}
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

func TestNSXServiceAccountService_getClusterName(t *testing.T) {
	uid := "uid-1"
	piNodeId := "legacy-pi-node"
	ccpId := "legacy-ccp-id"
	newPI := func(nodeId *string) *mpmodel.PrincipalIdentity {
		piId := "pi-id"
		return &mpmodel.PrincipalIdentity{
			Id:     &piId,
			Name:   &piId, // PI store is keyed by Name
			NodeId: nodeId,
			Tags:   []mpmodel.Tag{{Scope: &tagScopeNSXServiceAccountCRUID, Tag: &uid}},
		}
	}
	newCCP := func() *model.ClusterControlPlane {
		return &model.ClusterControlPlane{
			Id:   &ccpId,
			Tags: []model.Tag{{Scope: &tagScopeNSXServiceAccountCRUID, Tag: &uid}},
		}
	}
	generated := "k8scl-one_ns1_name1"

	tests := []struct {
		name            string
		pi              *mpmodel.PrincipalIdentity
		ccp             *model.ClusterControlPlane
		uid             string
		wantByUID       string
		wantClusterName string
	}{
		{name: "neither PI nor CCP", uid: uid, wantByUID: "", wantClusterName: generated},
		{name: "PI only", pi: newPI(&piNodeId), uid: uid, wantByUID: piNodeId, wantClusterName: piNodeId},
		{name: "CCP only", ccp: newCCP(), uid: uid, wantByUID: ccpId, wantClusterName: ccpId},
		{name: "PI preferred over CCP", pi: newPI(&piNodeId), ccp: newCCP(), uid: uid, wantByUID: piNodeId, wantClusterName: piNodeId},
		{name: "PI without NodeId falls back to CCP", pi: newPI(nil), ccp: newCCP(), uid: uid, wantByUID: ccpId, wantClusterName: ccpId},
		{name: "other UID is ignored", pi: newPI(&piNodeId), ccp: newCCP(), uid: "other", wantByUID: "", wantClusterName: generated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &NSXServiceAccountService{Service: newFakeCommonService()}
			s.NSXConfig = &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "k8scl-one"}}
			s.SetUpStore()
			if tt.pi != nil {
				assert.NoError(t, s.PrincipalIdentityStore.Add(tt.pi))
			}
			if tt.ccp != nil {
				assert.NoError(t, s.ClusterControlPlaneStore.Add(tt.ccp))
			}
			assert.Equal(t, tt.wantByUID, s.getNormalizedClusterNameByUID(tt.uid))
			assert.Equal(t, tt.wantClusterName, s.getClusterName(tt.uid, "ns1", "name1"))
		})
	}
}

func TestNSXServiceAccountService_getProxyEndpoints(t *testing.T) {
	tests := []struct {
		name        string
		obj         *v1alpha1.NSXServiceAccount
		prepareFunc func(*testing.T, *NSXServiceAccountService, context.Context)
		want        v1alpha1.NSXProxyEndpoint
		wantErr     assert.ErrorAssertionFunc
	}{
		{
			name: "NoProxy",
			obj: &v1alpha1.NSXServiceAccount{
				Spec: v1alpha1.NSXServiceAccountSpec{},
			},
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
			name: "SupervisorManagementProxy",
			obj: &v1alpha1.NSXServiceAccount{
				Spec: v1alpha1.NSXServiceAccountSpec{
					Proxy: v1alpha1.SupervisorManagementProxy,
				},
			},
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
								Name:     PortRestAPI,
								Protocol: "",
								Port:     PortNumRestAPI,
							},
							{
								Name:     PortNSXRPCFwdProxy,
								Protocol: v1.ProtocolTCP,
								Port:     PortNumNSXRPCFwdProxy,
							},
							{
								Name:     PortRestAPI,
								Protocol: v1.ProtocolUDP,
								Port:     10002,
							},
							{
								Name:     "wrong-rest-api",
								Protocol: v1.ProtocolTCP,
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
						Name:     PortRestAPI,
						Port:     PortNumRestAPI,
						Protocol: v1alpha1.NSXProxyProtocolTCP,
					},
					{
						Name:     PortNSXRPCFwdProxy,
						Port:     PortNumNSXRPCFwdProxy,
						Protocol: v1alpha1.NSXProxyProtocolTCP,
					},
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "VMCIProxy",
			obj: &v1alpha1.NSXServiceAccount{
				Spec: v1alpha1.NSXServiceAccountSpec{
					Proxy: v1alpha1.VMCIProxy,
				},
			},
			want: v1alpha1.NSXProxyEndpoint{
				Addresses: []v1alpha1.NSXProxyEndpointAddress{{IP: "127.0.0.1"}},
				Ports: []v1alpha1.NSXProxyEndpointPort{
					{
						Name:     PortRestAPI,
						Port:     PortNumRestAPI,
						Protocol: v1alpha1.NSXProxyProtocolTCP,
					},
					{
						Name:     PortNSXRPCFwdProxy,
						Port:     PortNumNSXRPCFwdProxy,
						Protocol: v1alpha1.NSXProxyProtocolTCP,
					},
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "DefaultToSupervisorManagementProxy",
			obj: &v1alpha1.NSXServiceAccount{
				Spec: v1alpha1.NSXServiceAccountSpec{},
			},
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
								Name:     PortRestAPI,
								Protocol: v1.ProtocolTCP,
								Port:     PortNumRestAPI,
							},
							{
								Name:     PortNSXRPCFwdProxy,
								Protocol: v1.ProtocolTCP,
								Port:     PortNumNSXRPCFwdProxy,
							},
						},
						Type: v1.ServiceTypeLoadBalancer,
					},
					Status: v1.ServiceStatus{
						LoadBalancer: v1.LoadBalancerStatus{
							Ingress: []v1.LoadBalancerIngress{{IP: "1.2.3.4"}},
						},
					},
				}
				assert.NoError(t, s.Client.Create(c, svc))
			},
			want: v1alpha1.NSXProxyEndpoint{
				Addresses: []v1alpha1.NSXProxyEndpointAddress{{IP: "1.2.3.4"}},
				Ports: []v1alpha1.NSXProxyEndpointPort{
					{
						Name:     PortRestAPI,
						Port:     PortNumRestAPI,
						Protocol: v1alpha1.NSXProxyProtocolTCP,
					},
					{
						Name:     PortNSXRPCFwdProxy,
						Port:     PortNumNSXRPCFwdProxy,
						Protocol: v1alpha1.NSXProxyProtocolTCP,
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
			if tt.prepareFunc != nil {
				tt.prepareFunc(t, s, ctx)
			}

			got, err := s.getProxyEndpoints(ctx, tt.obj)
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
				Spec: v1alpha1.NSXServiceAccountSpec{
					Proxy: v1alpha1.SupervisorManagementProxy,
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
								Name:     PortRestAPI,
								Port:     PortNumRestAPI,
								Protocol: v1alpha1.NSXProxyProtocolTCP,
							},
							{
								Name:     PortNSXRPCFwdProxy,
								Protocol: v1alpha1.NSXProxyProtocolTCP,
								Port:     PortNumNSXRPCFwdProxy,
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
								Name:     PortRestAPI,
								Protocol: v1.ProtocolTCP,
								Port:     PortNumRestAPI,
							},
							{
								Name:     PortNSXRPCFwdProxy,
								Protocol: v1.ProtocolTCP,
								Port:     PortNumNSXRPCFwdProxy,
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
						Name:     PortRestAPI,
						Port:     PortNumRestAPI,
						Protocol: v1alpha1.NSXProxyProtocolTCP,
					},
					{
						Name:     PortNSXRPCFwdProxy,
						Protocol: v1alpha1.NSXProxyProtocolTCP,
						Port:     PortNumNSXRPCFwdProxy,
					},
				},
			},
		},
		{
			name: "update nsx service account with VMCIProxy endpoints",
			nsxServiceAccount: &v1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nsxsa1",
					Namespace: "ns1",
				},
				Spec: v1alpha1.NSXServiceAccountSpec{
					Proxy: v1alpha1.VMCIProxy,
				},
				Status: v1alpha1.NSXServiceAccountStatus{
					Phase: v1alpha1.NSXServiceAccountPhaseRealized,
					ProxyEndpoints: v1alpha1.NSXProxyEndpoint{
						Addresses: []v1alpha1.NSXProxyEndpointAddress{
							{
								IP: "1.2.3.4",
							},
						},
						Ports: []v1alpha1.NSXProxyEndpointPort{
							{
								Name:     PortRestAPI,
								Port:     PortNumRestAPI,
								Protocol: v1alpha1.NSXProxyProtocolTCP,
							},
							{
								Name:     PortNSXRPCFwdProxy,
								Protocol: v1alpha1.NSXProxyProtocolTCP,
								Port:     PortNumNSXRPCFwdProxy,
							},
						},
					},
				},
			},
			expectedProxyEndpoints: v1alpha1.NSXProxyEndpoint{
				Addresses: []v1alpha1.NSXProxyEndpointAddress{
					{
						IP: "127.0.0.1",
					},
				},
				Ports: []v1alpha1.NSXProxyEndpointPort{
					{
						Name:     PortRestAPI,
						Port:     PortNumRestAPI,
						Protocol: v1alpha1.NSXProxyProtocolTCP,
					},
					{
						Name:     PortNSXRPCFwdProxy,
						Protocol: v1alpha1.NSXProxyProtocolTCP,
						Port:     PortNumNSXRPCFwdProxy,
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

func TestCCPCapacityBackoffBlocksCreation(t *testing.T) {
	ClearCCPConnectionCapacityFull()
	t.Cleanup(ClearCCPConnectionCapacityFull)
	s := &NSXServiceAccountService{Service: newFakeCommonService()}
	s.SetUpStore()
	obj := &v1alpha1.NSXServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "pending", Namespace: "ns1", UID: "pending-uid"}}
	calls := 0
	patches := gomonkey.ApplyMethod(s.NSXClient.WithCertificateClient, "Create", func(_ *fakeWithCertificateClient, _ mpmodel.PrincipalIdentityWithCertificate) (mpmodel.PrincipalIdentity, error) {
		calls++
		return mpmodel.PrincipalIdentity{}, fmt.Errorf("probe reached NSX")
	})
	patches.ApplyMethod(s.NSXClient.ClusterControlPlanesClient, "Update", func(_ *fakeClusterControlPlanesClient, _, _, _ string, _ model.ClusterControlPlane) (model.ClusterControlPlane, error) {
		calls++
		return model.ClusterControlPlane{}, fmt.Errorf("rotation reached NSX")
	})
	defer patches.Reset()

	MarkCCPConnectionCapacityFull()
	_, err := s.createPIAndCCP("pending", "/vpc", "cert", nil, obj)
	_, deferred := IsCCPBackoffError(err)
	require.True(t, deferred)
	assert.Zero(t, calls)

	// After expiry, a pending account can probe NSX again without restarting.
	_, skip := CCPConnectionCapacityFullBackoffRequeue(time.Now().Add(6 * time.Minute))
	require.False(t, skip)
	_, err = s.createPIAndCCP("pending", "/vpc", "cert", nil, obj)
	require.ErrorContains(t, err, "probe reached NSX")
	assert.Equal(t, 1, calls)
}

func TestCCPCapacityBackoffAllowsExistingAccountUpdates(t *testing.T) {
	for _, operation := range []string{"rotate", "retry-failed-account"} {
		t.Run(operation, func(t *testing.T) {
			ClearCCPConnectionCapacityFull()
			t.Cleanup(ClearCCPConnectionCapacityFull)
			ctx := context.Background()
			s := &NSXServiceAccountService{Service: newFakeCommonService()}
			s.SetUpStore()
			obj := newTestServiceAccount()
			obj.Status.Phase = v1alpha1.NSXServiceAccountPhaseFailed
			require.NoError(t, s.Client.Create(ctx, obj))
			captured := mockServiceAccountNSX(t, s, obj, true)
			name := util.NormalizeId(s.getClusterName(string(obj.UID), obj.Namespace, obj.Name))
			MarkCCPConnectionCapacityFull()
			if operation == "rotate" {
				require.NoError(t, s.updatePIAndCCPCert(name, string(obj.UID), "rotated-cert"))
				assert.Equal(t, "rotated-cert", captured.ccp)
			} else {
				require.NoError(t, s.CreateOrUpdateNSXServiceAccount(ctx, obj))
				assert.True(t, IsNSXServiceAccountRealized(&obj.Status))
			}
			assert.NotEmpty(t, captured.ccp)
			assert.Equal(t, captured.pi, captured.ccp)
			_, active := CCPConnectionCapacityFullBackoffRequeue(time.Now())
			assert.True(t, active, "updating an existing CCP must not clear the creation backoff")
		})
	}
}

func TestCCPCreationDefersWhenCapacityFillsDuringPICreation(t *testing.T) {
	ClearCCPConnectionCapacityFull()
	t.Cleanup(ClearCCPConnectionCapacityFull)
	s := &NSXServiceAccountService{Service: newFakeCommonService()}
	s.SetUpStore()
	obj := newTestServiceAccount()
	patches := gomonkey.ApplyMethod(s.NSXClient.WithCertificateClient, "Create", func(_ *fakeWithCertificateClient, request mpmodel.PrincipalIdentityWithCertificate) (mpmodel.PrincipalIdentity, error) {
		MarkCCPConnectionCapacityFull()
		return mpmodel.PrincipalIdentity{Name: request.Name, Tags: request.Tags}, nil
	})
	defer patches.Reset()
	patches.ApplyMethod(s.NSXClient.ClusterControlPlanesClient, "Update", func(_ *fakeClusterControlPlanesClient, _, _, _ string, _ model.ClusterControlPlane) (model.ClusterControlPlane, error) {
		t.Fatal("new CCP creation must wait after another worker detects full capacity")
		return model.ClusterControlPlane{}, nil
	})
	_, err := s.createPIAndCCP("account", "/vpc", "cert", nil, obj)
	_, deferred := IsCCPBackoffError(err)
	require.True(t, deferred)
}

func TestCCPWritesPreserveNewCapacityError(t *testing.T) {
	for _, operation := range []string{"create", "retry", "rotate"} {
		t.Run(operation, func(t *testing.T) {
			ClearCCPConnectionCapacityFull()
			t.Cleanup(ClearCCPConnectionCapacityFull)
			s := &NSXServiceAccountService{Service: newFakeCommonService()}
			s.SetUpStore()
			obj := newTestServiceAccount()
			mockServiceAccountNSX(t, s, obj, operation != "create")
			name := util.NormalizeId(s.getClusterName(string(obj.UID), obj.Namespace, obj.Name))
			nodeID := "cluster-id"
			patches := gomonkey.ApplyMethod(s.NSXClient.ClusterControlPlanesClient, "Update", func(_ *fakeClusterControlPlanesClient, _, _, _ string, request model.ClusterControlPlane) (model.ClusterControlPlane, error) {
				// Another worker discovers full capacity while this request is in flight.
				MarkCCPConnectionCapacityFull()
				request.Id, request.NodeId = &name, &nodeID
				return request, nil
			})
			defer patches.Reset()
			if operation == "rotate" {
				require.NoError(t, s.updatePIAndCCPCert(name, string(obj.UID), "cert"))
			} else {
				_, err := s.createPIAndCCP(name, "/vpc", "cert", nil, obj)
				require.NoError(t, err)
			}
			_, skip := CCPConnectionCapacityFullBackoffRequeue(time.Now())
			assert.True(t, skip, "a successful request must not clear a newer capacity error")
		})
	}
}

func TestNSXServiceAccountRejectsMissingCCPNodeID(t *testing.T) {
	empty := ""
	for name, nodeID := range map[string]*string{"nil": nil, "empty": &empty} {
		t.Run(name, func(t *testing.T) {
			ClearCCPConnectionCapacityFull()
			t.Cleanup(ClearCCPConnectionCapacityFull)
			ctx := context.Background()
			s := &NSXServiceAccountService{Service: newFakeCommonService()}
			s.SetUpStore()
			obj := newTestServiceAccount()
			require.NoError(t, s.Client.Create(ctx, obj))
			mockServiceAccountNSX(t, s, obj, false)
			patches := gomonkey.ApplyMethod(s.NSXClient.ClusterControlPlanesClient, "Update", func(_ *fakeClusterControlPlanesClient, _, _, name string, request model.ClusterControlPlane) (model.ClusterControlPlane, error) {
				request.Id, request.NodeId = &name, nodeID
				return request, nil
			})
			defer patches.Reset()
			// Both initial creation and a retry must reject the incomplete NSX response.
			for attempt := 0; attempt < 2; attempt++ {
				require.ErrorContains(t, s.CreateOrUpdateNSXServiceAccount(ctx, obj), "has no node ID")
				require.NoError(t, s.Client.Get(ctx, client.ObjectKeyFromObject(obj), obj))
				assert.False(t, IsNSXServiceAccountRealized(&obj.Status))
			}
		})
	}
}

func TestNSXServiceAccountRecoversAfterCapacityReturns(t *testing.T) {
	ClearCCPConnectionCapacityFull()
	t.Cleanup(ClearCCPConnectionCapacityFull)
	ctx := context.Background()
	s := &NSXServiceAccountService{Service: newFakeCommonService()}
	s.SetUpStore()
	obj := newTestServiceAccount()
	require.NoError(t, s.Client.Create(ctx, obj))
	captured := mockServiceAccountNSX(t, s, obj, false)
	full, requests := true, 0
	code := int64(nsxutil.CCPConnectionCapacityFullErrorCode)
	capacityErr := nsxutil.NewNSXApiError(&model.ApiError{ErrorCode: &code}, vapierrors.ErrorType_INVALID_REQUEST)
	nodeID := "recovered-cluster"
	patches := gomonkey.ApplyMethod(s.NSXClient.ClusterControlPlanesClient, "Update", func(_ *fakeClusterControlPlanesClient, _, _, name string, request model.ClusterControlPlane) (model.ClusterControlPlane, error) {
		requests++
		if full {
			return model.ClusterControlPlane{}, capacityErr
		}
		captured.ccp = *request.Certificate
		request.Id, request.NodeId = &name, &nodeID
		return request, nil
	})
	defer patches.Reset()

	require.True(t, nsxutil.IsCCPConnectionCapacityFullError(s.CreateOrUpdateNSXServiceAccount(ctx, obj)))
	secretKey := types.NamespacedName{Name: obj.Name + SecretSuffix, Namespace: obj.Namespace}
	original := &v1.Secret{}
	require.NoError(t, s.Client.Get(ctx, secretKey, original))
	_, deferred := IsCCPBackoffError(s.CreateOrUpdateNSXServiceAccount(ctx, obj))
	require.True(t, deferred)
	assert.Equal(t, 1, requests)
	stored := &v1.Secret{}
	require.NoError(t, s.Client.Get(ctx, secretKey, stored))
	assert.Equal(t, original.Data, stored.Data)

	// A later probe succeeds after capacity has returned and cooldown expires.
	full = false
	_, skip := CCPConnectionCapacityFullBackoffRequeue(time.Now().Add(6 * time.Minute))
	require.False(t, skip)
	retried := &v1alpha1.NSXServiceAccount{}
	require.NoError(t, s.Client.Get(ctx, client.ObjectKeyFromObject(obj), retried))
	require.NoError(t, s.CreateOrUpdateNSXServiceAccount(ctx, retried))
	require.NoError(t, s.Client.Get(ctx, secretKey, stored))
	assert.Equal(t, original.Data, stored.Data)
	assert.Equal(t, string(original.Data[SecretCertName]), captured.pi)
	assert.Equal(t, captured.pi, captured.ccp)
	assert.Equal(t, 2, requests)
	assert.Equal(t, nodeID, retried.Status.ClusterID)
	assert.True(t, IsNSXServiceAccountRealized(&retried.Status))
}

func TestRestoreRealizedNSXServiceAccountRetriesPartialRestore(t *testing.T) {
	code := int64(nsxutil.CCPConnectionCapacityFullErrorCode)
	for _, failure := range []struct {
		name string
		err  error
	}{
		{name: "capacity", err: nsxutil.NewNSXApiError(&model.ApiError{ErrorCode: &code}, vapierrors.ErrorType_INVALID_REQUEST)},
		{name: "transient", err: errors.New("CCP update failed")},
	} {
		for _, restart := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/restart=%t", failure.name, restart), func(t *testing.T) {
				ClearCCPConnectionCapacityFull()
				t.Cleanup(ClearCCPConnectionCapacityFull)
				ctx := context.Background()
				s := &NSXServiceAccountService{Service: newFakeCommonService()}
				s.SetUpStore()
				obj := newTestServiceAccount()
				name := util.NormalizeId(s.getClusterName(string(obj.UID), obj.Namespace, obj.Name))
				obj.Status = v1alpha1.NSXServiceAccountStatus{
					Phase: v1alpha1.NSXServiceAccountPhaseRealized, ClusterName: name,
					ClusterID: "original-cluster-id", VPCPath: "/orgs/default/projects/project/vpcs/vpc",
					Secrets: []v1alpha1.NSXSecret{{Name: obj.Name + SecretSuffix, Namespace: obj.Namespace}},
				}
				secret := newOwnedTestSecret(obj, map[string][]byte{SecretCertName: []byte("persisted-cert"), SecretKeyName: []byte("persisted-key")})
				require.NoError(t, s.Client.Create(ctx, secret))
				captured := mockServiceAccountNSX(t, s, obj, false)
				creates, updates, gets := 0, 0, 0
				var persistedPI mpmodel.PrincipalIdentity
				patches := gomonkey.ApplyMethod(s.NSXClient.WithCertificateClient, "Create", func(_ *fakeWithCertificateClient, request mpmodel.PrincipalIdentityWithCertificate) (mpmodel.PrincipalIdentity, error) {
					creates++
					captured.pi = *request.CertificatePem
					id, certID := "pi-id", "cert-id"
					persistedPI = mpmodel.PrincipalIdentity{Name: request.Name, Id: &id, CertificateId: &certID, Tags: request.Tags}
					return persistedPI, nil
				})
				defer patches.Reset()
				patches.ApplyMethod(s.NSXClient.ClusterControlPlanesClient, "Get", func(_ *fakeClusterControlPlanesClient, _, _, _ string) (model.ClusterControlPlane, error) {
					gets++
					return model.ClusterControlPlane{}, nsxutil.NewNSXApiError(nil, vapierrors.ErrorType_NOT_FOUND)
				})
				patches.ApplyMethod(s.NSXClient.ClusterControlPlanesClient, "Update", func(_ *fakeClusterControlPlanesClient, _, _, id string, request model.ClusterControlPlane) (model.ClusterControlPlane, error) {
					updates++
					if updates == 1 {
						return model.ClusterControlPlane{}, failure.err
					}
					require.Equal(t, obj.Status.ClusterID, *request.NodeId)
					require.Equal(t, obj.Status.VPCPath, *request.VhcPath)
					captured.ccp = *request.Certificate
					request.Id = &id
					return request, nil
				})
				require.ErrorIs(t, s.RestoreRealizedNSXServiceAccount(ctx, obj), failure.err)
				require.NotNil(t, s.PrincipalIdentityStore.GetByKey(name))
				require.Nil(t, s.ClusterControlPlaneStore.GetByKey(name))
				if failure.name == "capacity" {
					_, deferred := IsCCPBackoffError(s.RestoreRealizedNSXServiceAccount(ctx, obj))
					require.True(t, deferred)
					assert.Equal(t, 1, gets)
					assert.Equal(t, 1, updates)
				}
				if restart {
					// Reload the persisted PI into fresh stores, as startup does.
					s = &NSXServiceAccountService{Service: s.Service}
					s.SetUpStore()
					require.NoError(t, s.PrincipalIdentityStore.Add(&persistedPI))
					ClearCCPConnectionCapacityFull()
				} else {
					_, skip := CCPConnectionCapacityFullBackoffRequeue(time.Now().Add(6 * time.Minute))
					require.False(t, skip)
				}
				require.NoError(t, s.RestoreRealizedNSXServiceAccount(ctx, obj))
				assert.Equal(t, 1, creates, "retry must reuse the PI")
				assert.Equal(t, 2, updates)
				require.NotNil(t, s.ClusterControlPlaneStore.GetByKey(name))
				assert.Equal(t, "persisted-cert", captured.pi)
				assert.Equal(t, captured.pi, captured.ccp)
				storedSecret := &v1.Secret{}
				require.NoError(t, s.Client.Get(ctx, client.ObjectKeyFromObject(secret), storedSecret))
				assert.Equal(t, secret.Data, storedSecret.Data)
				assert.True(t, IsNSXServiceAccountRealized(&obj.Status))
			})
		}
	}
}

func TestRestoreRealizedNSXServiceAccountPartialRestoreGuards(t *testing.T) {
	for _, test := range []struct {
		name                  string
		piUID                 types.UID
		otherOwnedPI, ccpOnly bool
		ccpExistsOnNSX        bool
	}{
		{name: "PI owned by another account", piUID: "other-uid"},
		{name: "conflicting PI with another owned PI", piUID: "other-uid", otherOwnedPI: true},
		{name: "owned PI under a different name", otherOwnedPI: true},
		{name: "CCP without PI", ccpOnly: true},
		{name: "CCP missing from cache only", ccpExistsOnNSX: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ClearCCPConnectionCapacityFull()
			t.Cleanup(ClearCCPConnectionCapacityFull)
			ctx := context.Background()
			s := &NSXServiceAccountService{Service: newFakeCommonService()}
			s.SetUpStore()
			obj := newTestServiceAccount()
			name := "realized-account"
			obj.Status = v1alpha1.NSXServiceAccountStatus{
				ClusterName: name,
				Secrets:     []v1alpha1.NSXSecret{{Name: obj.Name + SecretSuffix, Namespace: obj.Namespace}},
			}
			require.NoError(t, s.Client.Create(ctx, newOwnedTestSecret(obj, map[string][]byte{SecretCertName: []byte("persisted-cert")})))
			ownedTags := s.buildBasicTags(obj)
			if test.ccpOnly {
				require.NoError(t, s.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{Id: &name, Tags: ownedTags}))
			} else if test.piUID != "" || test.ccpExistsOnNSX {
				piOwner := obj.DeepCopy()
				if test.piUID != "" {
					piOwner.UID = test.piUID
				}
				require.NoError(t, s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{Name: &name, Tags: common.ConvertTagsToMPTags(s.buildBasicTags(piOwner))}))
			}
			if test.otherOwnedPI {
				otherName := "other-account-name"
				require.NoError(t, s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{Name: &otherName, Tags: common.ConvertTagsToMPTags(ownedTags)}))
			}
			// The default NSX fake reports an existing CCP. In the cache-only
			// case, restore must detect it before changing the PI or CCP.
			err := s.RestoreRealizedNSXServiceAccount(ctx, obj)
			if test.ccpExistsOnNSX {
				require.EqualError(t, err, "CCP store is not synchronized")
			} else {
				require.EqualError(t, err, "PI/CCP doesn't match")
			}
		})
	}
}

// newAPIErrorData builds the ApiError payload NSX attaches to a vAPI error, which
// TransNSXApiError needs in order to translate the error.
func newAPIErrorData(t *testing.T, code int64) *data.StructValue {
	t.Helper()
	dv, errs := bindings.NewTypeConverter().ConvertToVapi(model.ApiError{ErrorCode: &code}, model.ApiErrorBindingType())
	require.Empty(t, errs)
	return dv.(*data.StructValue)
}

// newMP2038Error builds the vAPI error the NSX SDK returns for MP2038 ("Certificate already exists").
func newMP2038Error(t *testing.T) error {
	t.Helper()
	errType := vapierrors.ErrorType_INVALID_REQUEST
	return vapierrors.InvalidRequest{Data: newAPIErrorData(t, nsxutil.CertificateAlreadyExistsErrorCode), ErrorType: &errType}
}

// newNotFoundError builds the vAPI error the NSX SDK returns for a missing object.
func newNotFoundError(t *testing.T) error {
	t.Helper()
	errType := vapierrors.ErrorType_NOT_FOUND
	return vapierrors.NotFound{Data: newAPIErrorData(t, 202), ErrorType: &errType}
}

func TestSamePEMCertificate(t *testing.T) {
	subject := util.DefaultSubject
	subject.CommonName = "same-pem-test"
	certA, _, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	require.NoError(t, err)
	certB, _, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	require.NoError(t, err)

	assert.True(t, samePEMCertificate(certA, certA))
	// Formatting differences must not matter, only the certificate itself.
	assert.True(t, samePEMCertificate(certA, strings.ReplaceAll(certA, "\n", "\r\n")))
	assert.True(t, samePEMCertificate("\n"+certA+"\n\n", certA))
	assert.False(t, samePEMCertificate(certA, certB))
	// Unparsable input never counts as the same certificate, even when equal.
	assert.False(t, samePEMCertificate("cert", "cert"))
	assert.False(t, samePEMCertificate("", ""))
	assert.False(t, samePEMCertificate(certA, "cert"))
}

// A reconcile that re-enters CreateOrUpdate after a successful one reuses the Secret's
// certificate while the PI already holds it. It must succeed without importing the
// certificate again (NSX rejects that with MP2038).
func TestNSXServiceAccountService_CreateOrUpdateNSXServiceAccount_RepeatedReconcileWithExistingPI(t *testing.T) {
	ClearCCPConnectionCapacityFull()
	t.Cleanup(ClearCCPConnectionCapacityFull)
	ctx := context.Background()
	s := &NSXServiceAccountService{Service: newFakeCommonService()}
	s.SetUpStore()
	obj := newTestServiceAccount()
	require.NoError(t, s.Client.Create(ctx, obj))

	name := util.NormalizeId(s.getClusterName(string(obj.UID), obj.Namespace, obj.Name))
	piID, certID, nodeID := "pi1", "cert1", "clusterId1"
	// attached is the certificate NSX holds for the PI, as NSX would return it.
	var attached string
	imports := 0
	patches := gomonkey.ApplyMethod(s.NSXClient, "NSXCheckVersion", func(_ *nsx.Client, _ int) bool { return true })
	defer patches.Reset()
	patches.ApplyMethod(s.NSXClient.WithCertificateClient, "Create", func(_ *fakeWithCertificateClient, request mpmodel.PrincipalIdentityWithCertificate) (mpmodel.PrincipalIdentity, error) {
		attached = *request.CertificatePem
		return mpmodel.PrincipalIdentity{Name: request.Name, Id: &piID, CertificateId: &certID, Tags: request.Tags}, nil
	})
	patches.ApplyMethod(s.NSXClient.CertificatesClient, "Get", func(_ *fakeCertificatesClient, _ string, _ *bool, _ *string) (mpmodel.Certificate, error) {
		return mpmodel.Certificate{PemEncoded: &attached}, nil
	})
	patches.ApplyMethod(s.NSXClient.CertificatesClient, "Importcertificate", func(_ *fakeCertificatesClient, request mpmodel.TrustObjectData) (mpmodel.CertificateList, error) {
		imports++
		if *request.PemEncoded == attached {
			return mpmodel.CertificateList{}, newMP2038Error(t)
		}
		attached = *request.PemEncoded
		return mpmodel.CertificateList{Results: []mpmodel.Certificate{{Id: &certID}}}, nil
	})
	// NSX rejects a CCP update that carries the certificate the CCP already has
	// (HTTP 500, error code 100), so the fake does too.
	ccpCert := ""
	ccpUpdates := 0
	patches.ApplyMethod(s.NSXClient.ClusterControlPlanesClient, "Update", func(_ *fakeClusterControlPlanesClient, _, _, _ string, request model.ClusterControlPlane) (model.ClusterControlPlane, error) {
		ccpUpdates++
		if samePEMCertificate(ccpCert, *request.Certificate) {
			return model.ClusterControlPlane{}, errors.New("nsx error code: 100, message: General error has occurred.")
		}
		ccpCert = *request.Certificate
		request.Id, request.NodeId = &name, &nodeID
		return request, nil
	})

	// First reconcile creates the PI, CCP and Secret.
	require.NoError(t, s.CreateOrUpdateNSXServiceAccount(ctx, obj))
	require.NotEmpty(t, attached)
	// A stale reconcile runs the create/update path again with the same Secret.
	for i := 0; i < 2; i++ {
		require.NoError(t, s.CreateOrUpdateNSXServiceAccount(ctx, obj))
	}
	assert.Zero(t, imports, "the PI's own certificate must not be imported again")
	assert.Equal(t, 1, ccpUpdates, "the CCP must not be updated again with its own certificate")
	assert.True(t, IsNSXServiceAccountRealized(&obj.Status))
}

func TestNSXServiceAccountService_updatePICert_AlreadyExists(t *testing.T) {
	subject := util.DefaultSubject
	subject.CommonName = "update-pi-cert-test"
	cert, _, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	require.NoError(t, err)
	oldCert, _, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	require.NoError(t, err)
	otherCert, _, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	require.NoError(t, err)

	// searchHit builds a search result the way NSX returns a certificate.
	searchHit := func(t *testing.T, id, pemData string) *data.StructValue {
		t.Helper()
		v, errs := common.NewConverter().ConvertToVapi(mpmodel.Certificate{Id: &id, PemEncoded: &pemData}, mpmodel.CertificateBindingType())
		require.Empty(t, errs)
		return v.(*data.StructValue)
	}

	const oldID, newID = "old-cert", "new-cert"
	tests := []struct {
		name string
		// nsxPICertID is the certificate the PI holds in NSX, as opposed to the cached PI,
		// which always still holds oldID.
		nsxPICertID string
		piGetErr    error
		// pems maps certificate IDs to the PEM NSX holds for them.
		pems       map[string]string
		searchPEM  string // PEM of the search hit, "" for no hit
		searchErr  error
		wantErr    string
		wantUpdate bool // the PI is switched to newID
		wantDelete []string
	}{
		{
			name:        "stale cache, PI in NSX already has the certificate",
			nsxPICertID: newID,
			pems:        map[string]string{oldID: oldCert, newID: cert},
		},
		{
			name:        "interrupted earlier attempt, existing certificate is found and used",
			nsxPICertID: oldID,
			pems:        map[string]string{oldID: oldCert, newID: cert},
			searchPEM:   cert,
			wantUpdate:  true,
			wantDelete:  []string{oldID},
		},
		{
			name:        "certificate not found by search yet is retried",
			nsxPICertID: oldID,
			pems:        map[string]string{oldID: oldCert},
			wantErr:     "will retry",
		},
		{
			name:        "search hit with a different certificate is not used",
			nsxPICertID: oldID,
			pems:        map[string]string{oldID: oldCert},
			searchPEM:   otherCert,
			wantErr:     "will retry",
		},
		{
			name:        "search failure is returned",
			nsxPICertID: oldID,
			pems:        map[string]string{oldID: oldCert},
			searchErr:   errors.New("search failed"),
			wantErr:     "search failed",
		},
		{
			name:     "PI cannot be refreshed",
			piGetErr: errors.New("get failed"),
			pems:     map[string]string{oldID: oldCert},
			wantErr:  "get failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &NSXServiceAccountService{Service: newFakeCommonService()}
			s.SetUpStore()
			name, piID, oldCertID := "pi-name", "pi1", oldID
			pi := &mpmodel.PrincipalIdentity{Name: &name, Id: &piID, CertificateId: &oldCertID}
			var updatedTo string
			var deleted []string
			patches := gomonkey.ApplyMethod(s.NSXClient.CertificatesClient, "Importcertificate", func(_ *fakeCertificatesClient, _ mpmodel.TrustObjectData) (mpmodel.CertificateList, error) {
				return mpmodel.CertificateList{}, newMP2038Error(t)
			})
			defer patches.Reset()
			patches.ApplyMethod(s.NSXClient.PrincipalIdentitiesClient, "Get", func(_ *fakePrincipalIdentitiesClient, _ string) (mpmodel.PrincipalIdentity, error) {
				certID := tt.nsxPICertID
				return mpmodel.PrincipalIdentity{Name: &name, Id: &piID, CertificateId: &certID}, tt.piGetErr
			})
			patches.ApplyMethod(s.NSXClient.CertificatesClient, "Get", func(_ *fakeCertificatesClient, id string, _ *bool, _ *string) (mpmodel.Certificate, error) {
				pemData := tt.pems[id]
				return mpmodel.Certificate{PemEncoded: &pemData}, nil
			})
			patches.ApplyMethod(s.NSXClient.MPQueryClient, "List", func(_ *fakeMPQueryClient, query string, _ *string, _ *string, _ *int64, _ *bool, _ *string) (mpmodel.SearchResponse, error) {
				assert.Contains(t, query, "leaf_certificate_sha_256_thumbprint:\""+certificateThumbprint(cert)+"\"")
				assert.NotContains(t, query, "resource_type", "NSX indexes an imported self-signed certificate as certificate_ca")
				if tt.searchPEM == "" {
					return mpmodel.SearchResponse{}, tt.searchErr
				}
				return mpmodel.SearchResponse{Results: []*data.StructValue{searchHit(t, newID, tt.searchPEM)}}, tt.searchErr
			})
			patches.ApplyMethod(s.NSXClient.PrincipalIdentitiesClient, "Updatecertificate", func(_ *fakePrincipalIdentitiesClient, req mpmodel.UpdatePrincipalIdentityCertificateRequest) (mpmodel.PrincipalIdentity, error) {
				updatedTo = *req.CertificateId
				return mpmodel.PrincipalIdentity{Name: &name, Id: &piID, CertificateId: req.CertificateId}, nil
			})
			patches.ApplyMethod(s.NSXClient.CertificatesClient, "Delete", func(_ *fakeCertificatesClient, id string) error {
				deleted = append(deleted, id)
				return nil
			})

			err := s.updatePICert(pi, name, cert)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			if tt.wantUpdate {
				assert.Equal(t, newID, updatedTo)
			} else {
				assert.Empty(t, updatedTo, "the PI must not be switched")
			}
			assert.Equal(t, tt.wantDelete, deleted)
			if tt.piGetErr == nil {
				// The cache must reflect NSX after the call, not the stale PI.
				cached := s.PrincipalIdentityStore.GetByKey(name).(*mpmodel.PrincipalIdentity)
				want := tt.nsxPICertID
				if tt.wantUpdate {
					want = newID
				}
				assert.Equal(t, want, *cached.CertificateId)
			}
		})
	}
}

// A PI that could not be switched to a freshly imported certificate must not leave that
// certificate behind, unless NSX may have applied the switch.
func TestNSXServiceAccountService_updatePICert_UpdateFails(t *testing.T) {
	const oldID, newID = "old-cert", "new-cert"
	tests := []struct {
		name        string
		nsxPICertID string
		piGetErr    error
		wantErr     bool
		wantDelete  []string
	}{
		{name: "update not applied deletes the imported certificate", nsxPICertID: oldID, wantErr: true, wantDelete: []string{newID}},
		{name: "update applied with a lost response succeeds", nsxPICertID: newID, wantDelete: []string{oldID}},
		{name: "unknown PI state keeps the imported certificate", piGetErr: errors.New("get failed"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &NSXServiceAccountService{Service: newFakeCommonService()}
			s.SetUpStore()
			name, piID, oldCertID, newCertID := "pi-name", "pi1", oldID, newID
			pi := &mpmodel.PrincipalIdentity{Name: &name, Id: &piID, CertificateId: &oldCertID}
			var deleted []string
			patches := gomonkey.ApplyMethod(s.NSXClient.CertificatesClient, "Importcertificate", func(_ *fakeCertificatesClient, _ mpmodel.TrustObjectData) (mpmodel.CertificateList, error) {
				return mpmodel.CertificateList{Results: []mpmodel.Certificate{{Id: &newCertID}}}, nil
			})
			defer patches.Reset()
			patches.ApplyMethod(s.NSXClient.PrincipalIdentitiesClient, "Updatecertificate", func(_ *fakePrincipalIdentitiesClient, _ mpmodel.UpdatePrincipalIdentityCertificateRequest) (mpmodel.PrincipalIdentity, error) {
				return mpmodel.PrincipalIdentity{}, errors.New("timeout")
			})
			patches.ApplyMethod(s.NSXClient.PrincipalIdentitiesClient, "Get", func(_ *fakePrincipalIdentitiesClient, _ string) (mpmodel.PrincipalIdentity, error) {
				certID := tt.nsxPICertID
				return mpmodel.PrincipalIdentity{Name: &name, Id: &piID, CertificateId: &certID}, tt.piGetErr
			})
			patches.ApplyMethod(s.NSXClient.CertificatesClient, "Delete", func(_ *fakeCertificatesClient, id string) error {
				deleted = append(deleted, id)
				return nil
			})

			err := s.updatePICert(pi, name, "cert")
			if tt.wantErr {
				require.ErrorContains(t, err, "timeout")
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantDelete, deleted)
		})
	}
}

func TestCertificateThumbprint(t *testing.T) {
	subject := util.DefaultSubject
	subject.CommonName = "thumbprint-test"
	cert, _, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	require.NoError(t, err)
	other, _, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	require.NoError(t, err)

	t.Run("NSX format", func(t *testing.T) {
		// NSX stores the thumbprint as upper case hex pairs separated by colons.
		assert.Regexp(t, `^([0-9A-F]{2}:){31}[0-9A-F]{2}$`, certificateThumbprint(cert))
	})
	t.Run("surrounding whitespace is ignored", func(t *testing.T) {
		assert.Equal(t, certificateThumbprint(cert), certificateThumbprint(cert+"\n"))
	})
	t.Run("first certificate of a chain is used", func(t *testing.T) {
		assert.Equal(t, certificateThumbprint(cert), certificateThumbprint(cert+other))
		assert.NotEqual(t, certificateThumbprint(cert), certificateThumbprint(other))
	})
	t.Run("invalid PEM", func(t *testing.T) {
		assert.Empty(t, certificateThumbprint("not a pem"))
	})
}

// lockAccount must serialize users of the same account and let different accounts run in parallel.
func TestNSXServiceAccountService_lockAccount(t *testing.T) {
	t.Run("same account waits, other account does not", func(t *testing.T) {
		s := &NSXServiceAccountService{}
		unlock := s.lockAccount("a")

		acquired := make(chan struct{})
		go func() {
			defer s.lockAccount("a")()
			close(acquired)
		}()
		select {
		case <-acquired:
			t.Fatal("the same account must be locked")
		case <-time.After(50 * time.Millisecond):
		}

		// Another account is not blocked.
		s.lockAccount("b")()

		unlock()
		select {
		case <-acquired:
		case <-time.After(time.Second):
			t.Fatal("the lock was not released")
		}
	})

	// Unsynchronized increments would be flagged by -race and lose updates.
	t.Run("mutual exclusion", func(t *testing.T) {
		s := &NSXServiceAccountService{}
		counter := 0
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 200; j++ {
					func() {
						defer s.lockAccount("a")()
						counter++
					}()
				}
			}()
		}
		wg.Wait()
		assert.Equal(t, 400, counter)
	})
}

// Errors of the import other than MP2038 must be returned as they are, without looking at the PI.
func TestNSXServiceAccountService_updatePICert_ImportErrors(t *testing.T) {
	tests := []struct {
		name       string
		importResp mpmodel.CertificateList
		importErr  error
		wantErr    string
	}{
		{name: "other import error is returned unchanged", importErr: errors.New("import failed"), wantErr: "import failed"},
		{name: "import without a certificate in the response", wantErr: "returned no certificate ID"},
		{name: "import with a certificate without ID", importResp: mpmodel.CertificateList{Results: []mpmodel.Certificate{{}}}, wantErr: "returned no certificate ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &NSXServiceAccountService{Service: newFakeCommonService()}
			s.SetUpStore()
			name, piID, certID := "pi-name", "pi1", "old-cert"
			pi := &mpmodel.PrincipalIdentity{Name: &name, Id: &piID, CertificateId: &certID}
			patches := gomonkey.ApplyMethod(s.NSXClient.CertificatesClient, "Importcertificate", func(_ *fakeCertificatesClient, _ mpmodel.TrustObjectData) (mpmodel.CertificateList, error) {
				return tt.importResp, tt.importErr
			})
			defer patches.Reset()
			patches.ApplyMethod(s.NSXClient.PrincipalIdentitiesClient, "Get", func(_ *fakePrincipalIdentitiesClient, _ string) (mpmodel.PrincipalIdentity, error) {
				t.Error("the PI must not be read when the import did not fail with MP2038")
				return mpmodel.PrincipalIdentity{}, nil
			})
			patches.ApplyMethod(s.NSXClient.PrincipalIdentitiesClient, "Updatecertificate", func(_ *fakePrincipalIdentitiesClient, _ mpmodel.UpdatePrincipalIdentityCertificateRequest) (mpmodel.PrincipalIdentity, error) {
				t.Error("the PI must not be updated")
				return mpmodel.PrincipalIdentity{}, nil
			})

			require.ErrorContains(t, s.updatePICert(pi, name, "cert"), tt.wantErr)
		})
	}
}

// A certificate that was already in NSX (found after MP2038) was not imported by this call,
// so it must never be deleted, even when the PI cannot be switched to it. The previous
// certificate must go once the PI uses the new one, but never when it is the same one.
func TestNSXServiceAccountService_updatePICert_DiscoveredCertificate(t *testing.T) {
	subject := util.DefaultSubject
	subject.CommonName = "discovered-cert-test"
	cert, _, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	require.NoError(t, err)
	oldCert, _, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	require.NoError(t, err)

	const oldID, foundID = "old-cert", "found-cert"
	tests := []struct {
		name       string
		updateErr  error
		foundID    string // ID the search reports for the certificate
		pems       map[string]string
		wantErr    bool
		wantDelete []string
	}{
		{
			name:       "update fails, discovered certificate is kept",
			updateErr:  errors.New("update failed"),
			foundID:    foundID,
			pems:       map[string]string{oldID: oldCert},
			wantErr:    true,
			wantDelete: nil,
		},
		{
			name:       "update succeeds, previous certificate is deleted",
			foundID:    foundID,
			pems:       map[string]string{oldID: oldCert},
			wantDelete: []string{oldID},
		},
		{
			// The PI's certificate cannot be read as the one in use, yet the search reports
			// the PI's own ID: the certificate in use must not be deleted.
			name:       "discovered certificate is the PI's current one",
			foundID:    oldID,
			pems:       map[string]string{oldID: oldCert},
			wantDelete: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &NSXServiceAccountService{Service: newFakeCommonService()}
			s.SetUpStore()
			name, piID, certID := "pi-name", "pi1", oldID
			pi := &mpmodel.PrincipalIdentity{Name: &name, Id: &piID, CertificateId: &certID}
			var deleted []string
			patches := gomonkey.ApplyMethod(s.NSXClient.CertificatesClient, "Importcertificate", func(_ *fakeCertificatesClient, _ mpmodel.TrustObjectData) (mpmodel.CertificateList, error) {
				return mpmodel.CertificateList{}, newMP2038Error(t)
			})
			defer patches.Reset()
			patches.ApplyMethod(s.NSXClient.PrincipalIdentitiesClient, "Get", func(_ *fakePrincipalIdentitiesClient, _ string) (mpmodel.PrincipalIdentity, error) {
				id := oldID // the PI stays on the previous certificate
				return mpmodel.PrincipalIdentity{Name: &name, Id: &piID, CertificateId: &id}, nil
			})
			patches.ApplyMethod(s.NSXClient.CertificatesClient, "Get", func(_ *fakeCertificatesClient, id string, _ *bool, _ *string) (mpmodel.Certificate, error) {
				pemData := tt.pems[id]
				return mpmodel.Certificate{PemEncoded: &pemData}, nil
			})
			patches.ApplyMethod(s.NSXClient.MPQueryClient, "List", func(_ *fakeMPQueryClient, _ string, _ *string, _ *string, _ *int64, _ *bool, _ *string) (mpmodel.SearchResponse, error) {
				id, pemData := tt.foundID, cert
				v, errs := common.NewConverter().ConvertToVapi(mpmodel.Certificate{Id: &id, PemEncoded: &pemData}, mpmodel.CertificateBindingType())
				require.Empty(t, errs)
				return mpmodel.SearchResponse{Results: []*data.StructValue{v.(*data.StructValue)}}, nil
			})
			patches.ApplyMethod(s.NSXClient.PrincipalIdentitiesClient, "Updatecertificate", func(_ *fakePrincipalIdentitiesClient, req mpmodel.UpdatePrincipalIdentityCertificateRequest) (mpmodel.PrincipalIdentity, error) {
				if tt.updateErr != nil {
					return mpmodel.PrincipalIdentity{}, tt.updateErr
				}
				return mpmodel.PrincipalIdentity{Name: &name, Id: &piID, CertificateId: req.CertificateId}, nil
			})
			patches.ApplyMethod(s.NSXClient.CertificatesClient, "Delete", func(_ *fakeCertificatesClient, id string) error {
				deleted = append(deleted, id)
				return nil
			})

			err := s.updatePICert(pi, name, cert)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantDelete, deleted)
		})
	}
}

// Failing to delete the previous certificate is only logged: the PI already uses the new one.
func TestNSXServiceAccountService_updatePICert_OldCertificateDeleteFails(t *testing.T) {
	s := &NSXServiceAccountService{Service: newFakeCommonService()}
	s.SetUpStore()
	name, piID, oldID, newID := "pi-name", "pi1", "old-cert", "new-cert"
	pi := &mpmodel.PrincipalIdentity{Name: &name, Id: &piID, CertificateId: &oldID}
	patches := gomonkey.ApplyMethod(s.NSXClient.CertificatesClient, "Importcertificate", func(_ *fakeCertificatesClient, _ mpmodel.TrustObjectData) (mpmodel.CertificateList, error) {
		return mpmodel.CertificateList{Results: []mpmodel.Certificate{{Id: &newID}}}, nil
	})
	defer patches.Reset()
	patches.ApplyMethod(s.NSXClient.PrincipalIdentitiesClient, "Updatecertificate", func(_ *fakePrincipalIdentitiesClient, req mpmodel.UpdatePrincipalIdentityCertificateRequest) (mpmodel.PrincipalIdentity, error) {
		return mpmodel.PrincipalIdentity{Name: &name, Id: &piID, CertificateId: req.CertificateId}, nil
	})
	patches.ApplyMethod(s.NSXClient.CertificatesClient, "Delete", func(_ *fakeCertificatesClient, _ string) error {
		return errors.New("delete failed")
	})

	require.NoError(t, s.updatePICert(pi, name, "cert"))
	assert.Equal(t, newID, *s.PrincipalIdentityStore.GetByKey(name).(*mpmodel.PrincipalIdentity).CertificateId)
}

func TestNSXServiceAccountService_findCertificateID(t *testing.T) {
	subject := util.DefaultSubject
	subject.CommonName = "find-cert-test"
	cert, _, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	require.NoError(t, err)
	otherCert, _, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	require.NoError(t, err)

	hit := func(t *testing.T, id *string, pemData *string) *data.StructValue {
		t.Helper()
		v, errs := common.NewConverter().ConvertToVapi(mpmodel.Certificate{Id: id, PemEncoded: pemData}, mpmodel.CertificateBindingType())
		require.Empty(t, errs)
		return v.(*data.StructValue)
	}
	str := func(v string) *string { return &v }
	// An entity that cannot be converted to a Certificate.
	invalid := data.NewStructValue("x", map[string]data.DataValue{"id": data.NewIntegerValue(1)})

	tests := []struct {
		name    string
		cert    string
		pages   []mpmodel.SearchResponse // returned in order, one per List call
		wantID  string
		wantErr string
		calls   int
	}{
		{name: "found on the first page", cert: cert, calls: 1, wantID: "id1",
			pages: []mpmodel.SearchResponse{{Results: []*data.StructValue{hit(t, str("id1"), &cert)}}}},
		{name: "found on a later page", cert: cert, calls: 2, wantID: "id2",
			pages: []mpmodel.SearchResponse{
				{Results: []*data.StructValue{hit(t, str("other"), &otherCert)}, Cursor: str("1")},
				{Results: []*data.StructValue{hit(t, str("id2"), &cert)}},
			}},
		{name: "empty cursor ends the search", cert: cert, calls: 1,
			pages: []mpmodel.SearchResponse{{Cursor: str("")}}},
		{name: "no results", cert: cert, calls: 1, pages: []mpmodel.SearchResponse{{}}},
		{name: "hits without id or PEM are skipped", cert: cert, calls: 1, wantID: "id3",
			pages: []mpmodel.SearchResponse{{Results: []*data.StructValue{
				hit(t, nil, &cert), hit(t, str("no-pem"), nil), hit(t, str("id3"), &cert)}}}},
		{name: "an unreadable hit does not hide a matching one", cert: cert, calls: 1, wantID: "id4",
			pages: []mpmodel.SearchResponse{{Results: []*data.StructValue{invalid, hit(t, str("id4"), &cert)}}}},
		{name: "same thumbprint but a different certificate is rejected", cert: cert, calls: 1,
			pages: []mpmodel.SearchResponse{{Results: []*data.StructValue{hit(t, str("id5"), &otherCert)}}}},
		{name: "invalid PEM", cert: "not a pem", wantErr: "invalid certificate PEM"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &NSXServiceAccountService{Service: newFakeCommonService()}
			s.SetUpStore()
			calls := 0
			patches := gomonkey.ApplyMethod(s.NSXClient.MPQueryClient, "List", func(_ *fakeMPQueryClient, _ string, cursor *string, _ *string, _ *int64, _ *bool, _ *string) (mpmodel.SearchResponse, error) {
				resp := tt.pages[calls]
				if calls > 0 {
					assert.NotNil(t, cursor, "the next page must be requested with the cursor")
				}
				calls++
				return resp, nil
			})
			defer patches.Reset()

			id, err := s.findCertificateID(tt.cert)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantID, id)
			assert.Equal(t, tt.calls, calls)
		})
	}

	t.Run("search failure is returned", func(t *testing.T) {
		s := &NSXServiceAccountService{Service: newFakeCommonService()}
		s.SetUpStore()
		patches := gomonkey.ApplyMethod(s.NSXClient.MPQueryClient, "List", func(_ *fakeMPQueryClient, _ string, _ *string, _ *string, _ *int64, _ *bool, _ *string) (mpmodel.SearchResponse, error) {
			return mpmodel.SearchResponse{}, errors.New("search failed")
		})
		defer patches.Reset()
		_, err := s.findCertificateID(cert)
		require.ErrorContains(t, err, "search failed")
	})
}

func TestNSXServiceAccountService_refreshPI(t *testing.T) {
	t.Run("PI without ID", func(t *testing.T) {
		s := &NSXServiceAccountService{Service: newFakeCommonService()}
		s.SetUpStore()
		_, err := s.refreshPI(&mpmodel.PrincipalIdentity{})
		require.ErrorContains(t, err, "no ID")
	})
	t.Run("response without name keeps the cached name", func(t *testing.T) {
		s := &NSXServiceAccountService{Service: newFakeCommonService()}
		s.SetUpStore()
		name, piID, certID := "pi-name", "pi1", "new-cert"
		patches := gomonkey.ApplyMethod(s.NSXClient.PrincipalIdentitiesClient, "Get", func(_ *fakePrincipalIdentitiesClient, _ string) (mpmodel.PrincipalIdentity, error) {
			return mpmodel.PrincipalIdentity{Id: &piID, CertificateId: &certID}, nil
		})
		defer patches.Reset()

		fresh, err := s.refreshPI(&mpmodel.PrincipalIdentity{Name: &name, Id: &piID})
		require.NoError(t, err)
		assert.Equal(t, name, *fresh.Name)
		assert.Equal(t, certID, *s.PrincipalIdentityStore.GetByKey(name).(*mpmodel.PrincipalIdentity).CertificateId)
	})
	t.Run("Get failure is returned and the store is untouched", func(t *testing.T) {
		s := &NSXServiceAccountService{Service: newFakeCommonService()}
		s.SetUpStore()
		name, piID := "pi-name", "pi1"
		patches := gomonkey.ApplyMethod(s.NSXClient.PrincipalIdentitiesClient, "Get", func(_ *fakePrincipalIdentitiesClient, _ string) (mpmodel.PrincipalIdentity, error) {
			return mpmodel.PrincipalIdentity{}, errors.New("get failed")
		})
		defer patches.Reset()

		_, err := s.refreshPI(&mpmodel.PrincipalIdentity{Name: &name, Id: &piID})
		require.ErrorContains(t, err, "get failed")
		assert.Nil(t, s.PrincipalIdentityStore.GetByKey(name))
	})
}

// The recovery must also work when entered through createPIAndCCP, the real caller: the
// cached PI still holds the previous certificate although NSX already switched it.
func TestNSXServiceAccountService_createPIAndCCP_StaleCachedPI(t *testing.T) {
	ClearCCPConnectionCapacityFull()
	t.Cleanup(ClearCCPConnectionCapacityFull)
	subject := util.DefaultSubject
	subject.CommonName = "stale-pi-test"
	cert, _, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	require.NoError(t, err)
	oldCert, _, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	require.NoError(t, err)

	s := &NSXServiceAccountService{Service: newFakeCommonService()}
	s.SetUpStore()
	obj := newTestServiceAccount()
	name := util.NormalizeId(s.getClusterName(string(obj.UID), obj.Namespace, obj.Name))
	piID, nodeID, oldID, newID := "pi1", "clusterId1", "old-cert", "new-cert"
	scope, uid := common.TagScopeNSXServiceAccountCRUID, string(obj.UID)
	require.NoError(t, s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{Name: &name, Id: &piID, CertificateId: &oldID,
		Tags: []mpmodel.Tag{{Scope: &scope, Tag: &uid}}}))
	// The CCP already has the certificate, so only the PI is in question.
	require.NoError(t, s.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{Id: &name, NodeId: &nodeID, Certificate: &cert,
		Tags: []model.Tag{{Scope: &scope, Tag: &uid}}}))

	patches := gomonkey.ApplyMethod(s.NSXClient.CertificatesClient, "Importcertificate", func(_ *fakeCertificatesClient, _ mpmodel.TrustObjectData) (mpmodel.CertificateList, error) {
		return mpmodel.CertificateList{}, newMP2038Error(t)
	})
	defer patches.Reset()
	patches.ApplyMethod(s.NSXClient.CertificatesClient, "Get", func(_ *fakeCertificatesClient, id string, _ *bool, _ *string) (mpmodel.Certificate, error) {
		pemData := oldCert
		if id == newID {
			pemData = cert
		}
		return mpmodel.Certificate{PemEncoded: &pemData}, nil
	})
	patches.ApplyMethod(s.NSXClient.PrincipalIdentitiesClient, "Get", func(_ *fakePrincipalIdentitiesClient, _ string) (mpmodel.PrincipalIdentity, error) {
		id := newID
		return mpmodel.PrincipalIdentity{Name: &name, Id: &piID, CertificateId: &id, Tags: []mpmodel.Tag{{Scope: &scope, Tag: &uid}}}, nil
	})
	patches.ApplyMethod(s.NSXClient.PrincipalIdentitiesClient, "Updatecertificate", func(_ *fakePrincipalIdentitiesClient, _ mpmodel.UpdatePrincipalIdentityCertificateRequest) (mpmodel.PrincipalIdentity, error) {
		t.Error("the PI already has the certificate in NSX and must not be updated")
		return mpmodel.PrincipalIdentity{}, nil
	})

	gotNodeID, err := s.createPIAndCCP(name, "/vpc", cert, &nodeID, obj)
	require.NoError(t, err)
	assert.Equal(t, nodeID, gotNodeID)
	assert.Equal(t, newID, *s.PrincipalIdentityStore.GetByKey(name).(*mpmodel.PrincipalIdentity).CertificateId, "the cache must be refreshed from NSX")
}

// Reconcile, restore and cert rotation of one NSXServiceAccount must wait for each other.
func TestNSXServiceAccountService_EntryPointsHoldAccountLock(t *testing.T) {
	obj := newTestServiceAccount()
	run := map[string]func(s *NSXServiceAccountService, obj *v1alpha1.NSXServiceAccount){
		"CreateOrUpdateNSXServiceAccount": func(s *NSXServiceAccountService, obj *v1alpha1.NSXServiceAccount) {
			_ = s.CreateOrUpdateNSXServiceAccount(context.Background(), obj)
		},
		"RestoreRealizedNSXServiceAccount": func(s *NSXServiceAccountService, obj *v1alpha1.NSXServiceAccount) {
			_ = s.RestoreRealizedNSXServiceAccount(context.Background(), obj)
		},
		"ValidateAndUpdateRealizedNSXServiceAccount": func(s *NSXServiceAccountService, obj *v1alpha1.NSXServiceAccount) {
			_ = s.ValidateAndUpdateRealizedNSXServiceAccount(context.Background(), obj, nil, nil)
		},
	}
	for name, call := range run {
		t.Run(name, func(t *testing.T) {
			s := &NSXServiceAccountService{Service: newFakeCommonService()}
			s.SetUpStore()
			patches := gomonkey.ApplyMethod(s.NSXClient, "NSXCheckVersion", func(_ *nsx.Client, _ int) bool { return false })
			defer patches.Reset()
			// Once unblocked, CreateOrUpdate must stop at the first NSX write instead of
			// running on into the fakes, which return empty objects.
			patches.ApplyMethod(s.NSXClient.WithCertificateClient, "Create", func(_ *fakeWithCertificateClient, _ mpmodel.PrincipalIdentityWithCertificate) (mpmodel.PrincipalIdentity, error) {
				return mpmodel.PrincipalIdentity{}, errors.New("stop here")
			})
			clusterName := util.NormalizeId(s.getClusterName(string(obj.UID), obj.Namespace, obj.Name))
			account := obj.DeepCopy()
			account.Status.ClusterName = clusterName

			unlock := s.lockAccount(clusterName)
			done := make(chan struct{})
			go func() {
				defer close(done)
				call(s, account)
			}()
			select {
			case <-done:
				unlock()
				t.Fatal("the call must wait while the account is locked")
			case <-time.After(100 * time.Millisecond):
			}
			unlock()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("the call did not continue after the account was unlocked")
			}
		})
	}
}

// When the PI's current certificate cannot be read from NSX, createPIAndCCP must not
// guess: a failed lookup is retried, while a certificate that is gone from NSX is not the
// Secret's and is imported as before.
func TestNSXServiceAccountService_createPIAndCCP_PICertificateLookup(t *testing.T) {
	tests := []struct {
		name    string
		getErr  func(t *testing.T) error
		wantErr string
	}{
		{
			name:    "lookup failure is returned for retry",
			getErr:  func(*testing.T) error { return errors.New("get failed") },
			wantErr: "get failed",
		},
		{
			// A cached CertificateId goes stale e.g. after an NSX restore.
			name:   "stale CertificateId imports the certificate",
			getErr: newNotFoundError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ClearCCPConnectionCapacityFull()
			t.Cleanup(ClearCCPConnectionCapacityFull)
			s := &NSXServiceAccountService{Service: newFakeCommonService()}
			s.SetUpStore()
			obj := newTestServiceAccount()
			captured := mockServiceAccountNSX(t, s, obj, true)
			name := util.NormalizeId(s.getClusterName(string(obj.UID), obj.Namespace, obj.Name))
			// Give the stored PI a certificate so that it has to be looked up in NSX.
			certID := "cert1"
			s.PrincipalIdentityStore.GetByKey(name).(*mpmodel.PrincipalIdentity).CertificateId = &certID
			patches := gomonkey.ApplyMethod(s.NSXClient.CertificatesClient, "Get", func(_ *fakeCertificatesClient, _ string, _ *bool, _ *string) (mpmodel.Certificate, error) {
				return mpmodel.Certificate{}, tt.getErr(t)
			})
			defer patches.Reset()

			_, err := s.createPIAndCCP(name, "/vpc", "cert", nil, obj)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				assert.Empty(t, captured.pi, "a failed lookup must not turn into a blind import")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "cert", captured.pi, "the certificate must still be imported for the PI")
			assert.Equal(t, "cert", captured.ccp)
		})
	}
}

// createPIAndCCP must only write the CCP when its certificate changes: NSX rejects an
// update carrying the certificate the CCP already has.
func TestNSXServiceAccountService_createPIAndCCP_CCPCertificate(t *testing.T) {
	subject := util.DefaultSubject
	subject.CommonName = "ccp-cert-test"
	cert, _, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	require.NoError(t, err)
	otherCert, _, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	require.NoError(t, err)

	reformattedCert := "\n" + strings.ReplaceAll(cert, "\n", "\r\n")

	tests := []struct {
		name       string
		storedCert *string
		wantUpdate bool
	}{
		{name: "same certificate skips the update", storedCert: &cert},
		{name: "same certificate in different formatting skips the update", storedCert: &reformattedCert},
		{name: "different certificate updates the CCP", storedCert: &otherCert, wantUpdate: true},
		{name: "unknown certificate updates the CCP", wantUpdate: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ClearCCPConnectionCapacityFull()
			t.Cleanup(ClearCCPConnectionCapacityFull)
			s := &NSXServiceAccountService{Service: newFakeCommonService()}
			s.SetUpStore()
			obj := newTestServiceAccount()
			name := util.NormalizeId(s.getClusterName(string(obj.UID), obj.Namespace, obj.Name))
			piID, certID, nodeID := "pi1", "cert1", "clusterId1"
			scope, uid := common.TagScopeNSXServiceAccountCRUID, string(obj.UID)
			// The PI already holds the certificate, so only the CCP is under test.
			require.NoError(t, s.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{Name: &name, Id: &piID, CertificateId: &certID,
				Tags: []mpmodel.Tag{{Scope: &scope, Tag: &uid}}}))
			require.NoError(t, s.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{Id: &name, NodeId: &nodeID, Certificate: tt.storedCert,
				Tags: []model.Tag{{Scope: &scope, Tag: &uid}}}))
			patches := gomonkey.ApplyMethod(s.NSXClient.CertificatesClient, "Get", func(_ *fakeCertificatesClient, _ string, _ *bool, _ *string) (mpmodel.Certificate, error) {
				return mpmodel.Certificate{PemEncoded: &cert}, nil
			})
			defer patches.Reset()
			updates := 0
			patches.ApplyMethod(s.NSXClient.ClusterControlPlanesClient, "Update", func(_ *fakeClusterControlPlanesClient, _, _, _ string, request model.ClusterControlPlane) (model.ClusterControlPlane, error) {
				updates++
				// NSX may omit the certificate from the response.
				request.Certificate = nil
				return request, nil
			})

			gotNodeID, err := s.createPIAndCCP(name, "/vpc", cert, nil, obj)
			require.NoError(t, err)
			assert.Equal(t, nodeID, gotNodeID)
			assert.Equal(t, tt.wantUpdate, updates == 1)

			// Whatever happened, the cache must now know the CCP has the certificate,
			// so the next reconcile does not write it again.
			stored := s.ClusterControlPlaneStore.GetByKey(name).(*model.ClusterControlPlane)
			require.NotNil(t, stored.Certificate)
			assert.True(t, samePEMCertificate(*stored.Certificate, cert))
			_, err = s.createPIAndCCP(name, "/vpc", cert, nil, obj)
			require.NoError(t, err)
			assert.Equal(t, tt.wantUpdate, updates == 1, "a repeated call must not update the CCP again")
		})
	}
}

// Every CCP write, including a rotation, must leave the written certificate in the cache
// even when NSX omits it from the response, so that a later reconcile skips rewriting it.
func TestNSXServiceAccountService_updateClusterControlPlane_CachesCertificate(t *testing.T) {
	s := &NSXServiceAccountService{Service: newFakeCommonService()}
	s.SetUpStore()
	name, cert := "ccp1", "written-cert"
	patches := gomonkey.ApplyMethod(s.NSXClient.ClusterControlPlanesClient, "Update", func(_ *fakeClusterControlPlanesClient, _, _, _ string, request model.ClusterControlPlane) (model.ClusterControlPlane, error) {
		request.Certificate = nil
		return request, nil
	})
	defer patches.Reset()

	updated, err := s.updateClusterControlPlane(name, model.ClusterControlPlane{Id: &name, Certificate: &cert})
	require.NoError(t, err)
	require.NotNil(t, updated.Certificate)
	assert.Equal(t, cert, *updated.Certificate)
	stored, ok := s.ClusterControlPlaneStore.GetByKey(name).(*model.ClusterControlPlane)
	require.True(t, ok)
	require.NotNil(t, stored.Certificate)
	assert.Equal(t, cert, *stored.Certificate)
}
