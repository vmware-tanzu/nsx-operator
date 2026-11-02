/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsxserviceaccount

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"reflect"
	"sync"
	"time"

	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

const (
	siteId             = "default"
	enforcementpointId = "default"
	PortRestAPI        = "rest-api"
	PortNSXRPCFwdProxy = "nsx-rpc-fwd-proxy"
	// #nosec G101: false positive triggered by variable name which includes "secret"
	SecretSuffix   = "-nsx-cert"
	SecretCAName   = "ca.crt"
	SecretCertName = "tls.crt"
	SecretKeyName  = "tls.key"
	CAName         = "ca.crt"
)

var (
	log = logger.Log

	isProtectedTrue = true
	vpcRole         = "ccp_internal_operator"
	readerPath      = "/"
	readerRole      = "cluster_info_reader"

	antreaClusterResourceType = "AntreaClusterControlPlane"
	revision1                 = int64(1)

	proxyLabels = map[string]string{"mgmt-proxy.antrea-nsx.vmware.com": ""}
)

type NSXServiceAccountService struct {
	common.Service
	PrincipalIdentityStore   *PrincipalIdentityStore
	ClusterControlPlaneStore *ClusterControlPlaneStore
}

// InitializeNSXServiceAccount sync NSX resources
func InitializeNSXServiceAccount(service common.Service) (*NSXServiceAccountService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(2)
	nsxServiceAccountService := &NSXServiceAccountService{Service: service}

	nsxServiceAccountService.SetUpStore()
	go nsxServiceAccountService.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypePrincipalIdentity, nil, nsxServiceAccountService.PrincipalIdentityStore)
	go nsxServiceAccountService.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeClusterControlPlane, nil, nsxServiceAccountService.ClusterControlPlaneStore)
	go func() {
		wg.Wait()
		close(wgDone)
	}()

	select {
	case <-wgDone:
		break
	case err := <-fatalErrors:
		return nsxServiceAccountService, err
	}

	return nsxServiceAccountService, nil
}

func (s *NSXServiceAccountService) SetUpStore() {
	s.PrincipalIdentityStore = &PrincipalIdentityStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeNSXServiceAccountCRUID: indexFunc}),
		BindingType: mpmodel.PrincipalIdentityBindingType(),
	}}
	s.ClusterControlPlaneStore = &ClusterControlPlaneStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeNSXServiceAccountCRUID: indexFunc}),
		BindingType: model.ClusterControlPlaneBindingType(),
	}}
}

type secretOwnership int

const (
	secretOwnedByCurrentCR secretOwnership = iota
	secretOwnedByOlderCR
	secretOwnedByOther
	secretUnowned
)

func evaluateSecretOwnership(secret *v1.Secret, obj *v1alpha1.NSXServiceAccount) secretOwnership {
	if len(secret.OwnerReferences) == 0 {
		return secretUnowned
	}
	kind := obj.Kind
	if kind == "" {
		kind = "NSXServiceAccount"
	}
	for _, ref := range secret.OwnerReferences {
		if ref.UID == obj.UID {
			return secretOwnedByCurrentCR
		}
		if (ref.Kind == kind || ref.Kind == "NSXServiceAccount") && ref.Name == obj.Name {
			return secretOwnedByOlderCR
		}
	}
	return secretOwnedByOther
}

// validateServiceAccountSecretIdentity checks that credentials still belong to this account.
func validateServiceAccountSecretIdentity(secret *v1.Secret, obj *v1alpha1.NSXServiceAccount, expectedUID types.UID) error {
	if expectedUID != "" && secret.UID != expectedUID {
		return fmt.Errorf("secret %s/%s was replaced concurrently (UID changed from %s to %s)", secret.Namespace, secret.Name, expectedUID, secret.UID)
	}
	if !secret.DeletionTimestamp.IsZero() {
		return fmt.Errorf("secret %s/%s is being deleted, retrying later", secret.Namespace, secret.Name)
	}
	if evaluateSecretOwnership(secret, obj) != secretOwnedByCurrentCR {
		return fmt.Errorf("secret %s/%s is no longer owned by current NSXServiceAccount %s (UID %s)", secret.Namespace, secret.Name, obj.Name, obj.UID)
	}
	return nil
}

// reusableServiceAccountCertificate validates credentials before reusing them on a retry.
func reusableServiceAccountCertificate(secret *v1.Secret, rotate bool, now time.Time) bool {
	pair, err := tls.X509KeyPair(secret.Data[SecretCertName], secret.Data[SecretKeyName])
	if err != nil || len(pair.Certificate) == 0 {
		return false
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return false
	}
	return !rotate || !now.AddDate(0, 0, util.DefaultRotateDays).After(cert.NotAfter)
}

// ensureServiceAccountSecret persists usable credentials before NSX is updated.
func (s *NSXServiceAccountService) ensureServiceAccountSecret(ctx context.Context, obj *v1alpha1.NSXServiceAccount, normalizedClusterName string) (*v1.Secret, error) {
	secretName := obj.Name + SecretSuffix
	secretNamespace := obj.Namespace

	kind := obj.Kind
	if kind == "" {
		kind = "NSXServiceAccount"
	}
	apiVersion := obj.APIVersion
	if apiVersion == "" {
		apiVersion = v1alpha1.SchemeGroupVersion.String()
	}

	// 1. Inspect existing Secret and validate ownership.
	// Reuse existing certificate and key across retries if already owned by this CR.
	var cert, key string
	existingSecret := &v1.Secret{}
	foundExistingSecret := false
	err := s.Client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: secretNamespace}, existingSecret)
	if err == nil {
		foundExistingSecret = true
		if !existingSecret.DeletionTimestamp.IsZero() {
			return nil, fmt.Errorf("secret %s/%s is being deleted, retrying later", secretNamespace, secretName)
		}
		ownership := evaluateSecretOwnership(existingSecret, obj)
		switch ownership {
		case secretOwnedByCurrentCR:
			rotate := obj.Spec.EnableCertRotation && s.NSXClient.NSXCheckVersion(nsx.ServiceAccountCertRotation)
			if reusableServiceAccountCertificate(existingSecret, rotate, time.Now()) {
				cert = string(existingSecret.Data[SecretCertName])
				key = string(existingSecret.Data[SecretKeyName])
			}
		case secretOwnedByOlderCR:
			log.Info("Deleting stale secret belonging to older NSXServiceAccount CR", "secret", secretName, "namespace", secretNamespace)
			if err := s.Client.Delete(ctx, existingSecret, client.Preconditions{UID: &existingSecret.UID}); err != nil && !errors.IsNotFound(err) {
				return nil, fmt.Errorf("failed to delete stale secret %s/%s: %w", secretNamespace, secretName, err)
			}
			return nil, fmt.Errorf("stale secret %s/%s was deleted, retrying reconcile", secretNamespace, secretName)
		default:
			return nil, fmt.Errorf("secret %s/%s already exists but is not owned by current NSXServiceAccount %s (UID %s)", secretNamespace, secretName, obj.Name, obj.UID)
		}
	} else if !errors.IsNotFound(err) {
		return nil, err
	}

	// 2. Generate certificate if not reused from existing owned secret
	if cert == "" || key == "" {
		subject := util.DefaultSubject
		subject.CommonName = normalizedClusterName
		validDays := util.DefaultValidDays
		if s.NSXClient.NSXCheckVersion(nsx.ServiceAccountCertRotation) && obj.Spec.EnableCertRotation {
			validDays = util.DefaultValidDaysWithRotation
		}
		cert, key, err = util.GenerateCertificate(&subject, validDays)
		if err != nil {
			return nil, err
		}
	}

	// 3. Persist Secret to Kubernetes first so credentials exist and can be recovered across retries
	desiredSecret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: secretNamespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: apiVersion,
				Kind:       kind,
				Name:       obj.Name,
				UID:        obj.UID,
			}},
		},
		Data: map[string][]byte{SecretCertName: []byte(cert), SecretKeyName: []byte(key), CAName: s.NSXConfig.GetCACert()},
	}
	if foundExistingSecret {
		if !reflect.DeepEqual(existingSecret.Data, desiredSecret.Data) || !reflect.DeepEqual(existingSecret.OwnerReferences, desiredSecret.OwnerReferences) {
			err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
				latestSecret := &v1.Secret{}
				if err := s.Client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: secretNamespace}, latestSecret); err != nil {
					return err
				}
				if err := validateServiceAccountSecretIdentity(latestSecret, obj, existingSecret.UID); err != nil {
					return err
				}
				latestSecret.OwnerReferences = desiredSecret.OwnerReferences
				latestSecret.Data = desiredSecret.Data
				if err := s.Client.Update(ctx, latestSecret); err != nil {
					return err
				}
				desiredSecret = latestSecret
				return nil
			})
			if err != nil {
				return nil, err
			}
		} else {
			return existingSecret, nil
		}
	} else if err := s.Client.Create(ctx, desiredSecret); err != nil {
		// AlreadyExists can follow a cache miss. Requeue without overwriting the
		// existing credentials; the next reconcile will inspect and reuse them.
		return nil, err
	}

	return desiredSecret, nil
}

func (s *NSXServiceAccountService) CreateOrUpdateNSXServiceAccount(ctx context.Context, obj *v1alpha1.NSXServiceAccount) error {
	clusterName := s.getClusterName(obj.Namespace, obj.Name)
	normalizedClusterName := util.NormalizeId(clusterName)
	// TODO: Use WCPConfig.NSXTProject as project when WCPConfig.EnableWCPVPCNetwork is true
	project := s.NSXConfig.CoeConfig.Cluster
	vpcName := obj.Namespace + "-default-vpc"
	vpcPath := fmt.Sprintf("/orgs/default/projects/%s/vpcs/%s", util.NormalizeId(project), vpcName)

	// get proxy
	proxyEndpoints, err := s.getProxyEndpoints(ctx)
	if err != nil {
		return err
	}

	secret, err := s.ensureServiceAccountSecret(ctx, obj, normalizedClusterName)
	if err != nil {
		return err
	}

	// Apply the persisted certificate to NSX before publishing realized status.
	clusterId, err := s.createPIAndCCP(normalizedClusterName, vpcPath, string(secret.Data[SecretCertName]), nil, obj)
	if err != nil {
		return err
	}

	// Preserve status fields maintained by other operations.
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latestObj := &v1alpha1.NSXServiceAccount{}
		if err := s.Client.Get(ctx, types.NamespacedName{Name: obj.Name, Namespace: obj.Namespace}, latestObj); err != nil {
			return err
		}
		latestObj.Status.Phase = v1alpha1.NSXServiceAccountPhaseRealized
		latestObj.Status.Reason = "Success"
		latestObj.Status.Conditions = GenerateNSXServiceAccountConditions(
			latestObj.Status.Conditions, latestObj.Generation, metav1.ConditionTrue,
			v1alpha1.ConditionReasonRealizationSuccess, "Success.")
		latestObj.Status.NSXManagers = s.NSXConfig.NsxApiManagers
		latestObj.Status.ClusterID = clusterId
		latestObj.Status.ClusterName = normalizedClusterName
		latestObj.Status.Secrets = []v1alpha1.NSXSecret{{Name: secret.Name, Namespace: secret.Namespace}}
		latestObj.Status.VPCPath = vpcPath
		latestObj.Status.ProxyEndpoints = proxyEndpoints
		if err := s.Client.Status().Update(ctx, latestObj); err != nil {
			return err
		}
		obj.ResourceVersion = latestObj.ResourceVersion
		obj.Status = latestObj.Status
		return nil
	})
}

// RestoreRealizedNSXServiceAccount checks if PI/CCP is created on NSXT for a realized NSXServiceAccount. If both PI/CCP
// is missing, restore PI/CCP from realized NSXServiceAccount and Secret.
func (s *NSXServiceAccountService) RestoreRealizedNSXServiceAccount(ctx context.Context, obj *v1alpha1.NSXServiceAccount) error {
	normalizedClusterName := obj.Status.ClusterName

	// check PI and CCP is missing
	hasPI := len(s.PrincipalIdentityStore.GetByIndex(common.TagScopeNSXServiceAccountCRUID, string(obj.UID))) > 0
	hasCCP := len(s.ClusterControlPlaneStore.GetByIndex(common.TagScopeNSXServiceAccountCRUID, string(obj.UID))) > 0
	piObj := s.PrincipalIdentityStore.GetByKey(normalizedClusterName)
	ccpObj := s.ClusterControlPlaneStore.GetByKey(normalizedClusterName)
	var pi *mpmodel.PrincipalIdentity
	var certificate mpmodel.Certificate
	detail := true
	if piObj != nil {
		pi = piObj.(*mpmodel.PrincipalIdentity)
		certificate, _ = s.NSXClient.CertificatesClient.Get(*(pi.CertificateId), &detail, nil)
	}
	// read Secret
	if len(obj.Status.Secrets) == 0 {
		return fmt.Errorf("realized NSXServiceAccount %s/%s has no secret in status", obj.Namespace, obj.Name)
	}
	secretName := obj.Status.Secrets[0].Name
	secretNamespace := obj.Status.Secrets[0].Namespace
	secret := &v1.Secret{}
	if err := s.Client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: secretNamespace}, secret); err != nil {
		return err
	}
	cert := secret.Data[SecretCertName]

	log.Debug("RestoreRealizedNSXServiceAccount: ",
		"certificate.PemEncoded==nil", certificate.PemEncoded == nil,
		"certificate.PemEncoded", certificate.PemEncoded,
		"cert", string(cert))
	if hasPI && hasCCP && piObj != nil && ccpObj != nil {
		if string(cert) != "" && (certificate.PemEncoded == nil || *(certificate.PemEncoded) != string(cert)) {
			return s.updatePICert(pi, normalizedClusterName, string(cert))
		}
		return nil
	} else if hasPI || hasCCP || (piObj != nil) || (ccpObj != nil) {
		return fmt.Errorf("PI/CCP doesn't match")
	}
	_, err := s.NSXClient.ClusterControlPlanesClient.Get(siteId, enforcementpointId, normalizedClusterName)
	log.Debug("RestoreRealizedNSXServiceAccount s.NSXClient.ClusterControlPlanesClient.Get", "err", err)
	err = nsxutil.TransNSXApiError(err)
	if err == nil {
		return fmt.Errorf("CCP store is not synchronized")
	}
	if nsxErr, ok := err.(*nsxutil.NSXApiError); ok {
		if nsxErr.Type() != vapierrors.ErrorType_NOT_FOUND {
			return err
		}
	} else {
		return err
	}

	log.Info("Start to restore realized resource", "nsxserviceaccount", types.NamespacedName{Name: obj.Name, Namespace: obj.Namespace})

	vpcPath := obj.Status.VPCPath
	existingClusterId := obj.Status.ClusterID

	// restore PI and CCP
	_, err = s.createPIAndCCP(normalizedClusterName, vpcPath, string(cert), &existingClusterId, obj)
	return err
}

func (s *NSXServiceAccountService) createPIAndCCP(normalizedClusterName string, vpcPath string, cert string, existingClusterId *string, obj *v1alpha1.NSXServiceAccount) (string, error) {
	// create PI
	log.Debug("createPIAndCCP")
	hasPI := len(s.PrincipalIdentityStore.GetByIndex(common.TagScopeNSXServiceAccountCRUID, string(obj.UID))) > 0
	if piObj := s.PrincipalIdentityStore.GetByKey(normalizedClusterName); !hasPI && piObj == nil {
		log.Debug("create PI")
		pi, err := s.NSXClient.WithCertificateClient.Create(mpmodel.PrincipalIdentityWithCertificate{
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
			CertificatePem: &cert,
			Tags:           common.ConvertTagsToMPTags(s.buildBasicTags(obj)),
		})
		err = nsxutil.TransNSXApiError(err)
		if err != nil {
			return "", err
		}
		s.PrincipalIdentityStore.Add(&pi)
	} else if !hasPI != (piObj == nil) {
		log.Error(fmt.Errorf("conflicting old PI exists"), "Failed to create PI and CCPN", "!hasPI", !hasPI, "piObj == nil", piObj == nil)
		return "", fmt.Errorf("old PI exists")
	} else if hasPI && (piObj != nil) {
		log.Debug("update PI with new cert")
		pi := piObj.(*mpmodel.PrincipalIdentity)
		err := s.updatePICert(pi, normalizedClusterName, cert)
		if err != nil {
			return "", err
		}
	}

	// create ClusterControlPlane
	hasCCP := len(s.ClusterControlPlaneStore.GetByIndex(common.TagScopeNSXServiceAccountCRUID, string(obj.UID))) > 0
	clusterId := ""
	if ccpObj := s.ClusterControlPlaneStore.GetByKey(normalizedClusterName); !hasCCP && ccpObj == nil {
		ccp, err := s.NSXClient.ClusterControlPlanesClient.Update(siteId, enforcementpointId, normalizedClusterName, model.ClusterControlPlane{
			Revision:     &revision1,
			ResourceType: &antreaClusterResourceType,
			Certificate:  &cert,
			VhcPath:      &vpcPath,
			NodeId:       existingClusterId,
			Tags:         s.buildBasicTags(obj),
		})
		err = nsxutil.TransNSXApiError(err)
		if err != nil {
			return "", err
		}
		s.ClusterControlPlaneStore.Add(&ccp)
		clusterId = *ccp.NodeId
	} else if hasCCP && (ccpObj != nil) {
		log.Debug("update CCP with new cert")
		ccp := ccpObj.(*model.ClusterControlPlane)
		ccp2, err := s.updateCCPCertificate(normalizedClusterName, *ccp, cert)
		if err != nil {
			return "", err
		}
		if ccp2.NodeId != nil {
			clusterId = *ccp2.NodeId
		} else if ccp.NodeId != nil {
			clusterId = *ccp.NodeId
		}
	} else if !hasCCP != (ccpObj == nil) {
		return "", fmt.Errorf("old CCP exists")
	}
	if clusterId == "" && existingClusterId != nil {
		clusterId = *existingClusterId
	}
	return clusterId, nil
}

func (s *NSXServiceAccountService) getProxyEndpoints(ctx context.Context) (v1alpha1.NSXProxyEndpoint, error) {
	proxyEndpoints := v1alpha1.NSXProxyEndpoint{}
	proxies := &v1.ServiceList{}
	if err := s.Client.List(ctx, proxies, client.MatchingLabels(proxyLabels)); err != nil {
		return v1alpha1.NSXProxyEndpoint{}, err
	}
	for _, proxy := range proxies.Items {
		if proxy.Spec.Type == v1.ServiceTypeLoadBalancer {
			for _, ingress := range proxy.Status.LoadBalancer.Ingress {
				proxyEndpoints.Addresses = append(proxyEndpoints.Addresses, v1alpha1.NSXProxyEndpointAddress{IP: ingress.IP})
			}
			for _, port := range proxy.Spec.Ports {
				switch port.Name {
				case PortRestAPI, PortNSXRPCFwdProxy:
					switch port.Protocol {
					case "", v1.ProtocolTCP:
						proxyEndpoints.Ports = append(proxyEndpoints.Ports, v1alpha1.NSXProxyEndpointPort{
							Name:     port.Name,
							Port:     uint16(port.Port),
							Protocol: v1alpha1.NSXProxyProtocolTCP,
						})
					}
				}
			}
			break
		}
	}
	return proxyEndpoints, nil
}

func (s *NSXServiceAccountService) DeleteNSXServiceAccount(ctx context.Context, namespacedName types.NamespacedName, uid types.UID) error {
	isDeleteSecret := false
	nsxsa := &v1alpha1.NSXServiceAccount{}
	if err := s.Client.Get(ctx, namespacedName, nsxsa); err != nil {
		isDeleteSecret = true
	} else if uid == nsxsa.UID {
		isDeleteSecret = true
	}

	clusterName := s.getClusterName(namespacedName.Namespace, namespacedName.Name)
	normalizedClusterName := util.NormalizeId(clusterName)
	// delete Secret
	if isDeleteSecret {
		secretName := namespacedName.Name + SecretSuffix
		secretNamespace := namespacedName.Namespace
		existingSecret := &v1.Secret{}
		if err := s.Client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: secretNamespace}, existingSecret); err == nil {
			isOwnedByThisUID := false
			for _, ref := range existingSecret.OwnerReferences {
				if ref.UID == uid {
					isOwnedByThisUID = true
					break
				}
			}
			if isOwnedByThisUID || len(existingSecret.OwnerReferences) == 0 {
				if err := s.Client.Delete(ctx, existingSecret, client.Preconditions{UID: &existingSecret.UID}); err != nil && !errors.IsNotFound(err) {
					log.Error(err, "failed to delete", "secret", secretName, "namespace", secretNamespace)
					return err
				}
			}
		} else if !errors.IsNotFound(err) {
			log.Error(err, "failed to get", "secret", secretName, "namespace", secretNamespace)
			return err
		}
	}

	isDeleteCCP := true
	isDeletePI := true
	if !isDeleteSecret {
		isDeletePI = len(s.PrincipalIdentityStore.GetByIndex(common.TagScopeNSXServiceAccountCRUID, string(uid))) > 0
		isDeleteCCP = len(s.ClusterControlPlaneStore.GetByIndex(common.TagScopeNSXServiceAccountCRUID, string(uid))) > 0
	}
	// delete ClusterControlPlane
	if isDeleteCCP {
		if err := s.DeleteClusterControlPlane(ctx, normalizedClusterName); err != nil {
			err = nsxutil.TransNSXApiError(err)
			log.Error(err, "failed to delete", "ClusterControlPlane", normalizedClusterName)
			return err
		}
		s.ClusterControlPlaneStore.Delete(&model.ClusterControlPlane{Id: &normalizedClusterName})
	}

	// delete PI
	if piobj := s.PrincipalIdentityStore.GetByKey(normalizedClusterName); isDeletePI && (piobj != nil) {
		pi := piobj.(*mpmodel.PrincipalIdentity)
		if err := s.NSXClient.PrincipalIdentitiesClient.Delete(*pi.Id); err != nil {
			err = nsxutil.TransNSXApiError(err)
			log.Error(err, "failed to delete", "PrincipalIdentity", *pi.Name)
			return err
		}
		if pi.CertificateId != nil && *pi.CertificateId != "" {
			if err := s.NSXClient.CertificatesClient.Delete(*pi.CertificateId); err != nil {
				err = nsxutil.TransNSXApiError(err)
				log.Error(err, "failed to delete", "PrincipalIdentity", *pi.Name, "Certificate", *pi.CertificateId)
				return err
			}
		}
		s.PrincipalIdentityStore.Delete(pi)
	}
	return nil
}

// ValidateAndUpdateRealizedNSXServiceAccount checks CA is up-to-date and client cert needs rotation
// ca is nil means no need to update CA
// Client cert rotation requires NSXT 4.1.3
// It also updates NSXServiceAccount.Status.NSXRestoreStatus
func (s *NSXServiceAccountService) ValidateAndUpdateRealizedNSXServiceAccount(ctx context.Context, obj *v1alpha1.NSXServiceAccount, ca []byte,
	nsxRestoreStatus *v1alpha1.NSXRestoreStatus) error {

	clusterName := s.getClusterName(obj.Namespace, obj.Name)
	normalizedClusterName := util.NormalizeId(clusterName)
	secretName := obj.Name + SecretSuffix
	secretNamespace := obj.Namespace
	secret := &v1.Secret{}
	isCheckCert := s.NSXClient.NSXCheckVersion(nsx.ServiceAccountCertRotation) && obj.Spec.EnableCertRotation
	if len(ca) > 0 || isCheckCert {
		if err := s.Client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: secretNamespace}, secret); err != nil {
			return err
		}
		if err := validateServiceAccountSecretIdentity(secret, obj, ""); err != nil {
			return err
		}
	}

	var caUpdated, certRotated bool
	var newCert, newKey []byte

	// check CA is up-to-date
	if len(ca) > 0 {
		oldCA := secret.Data[CAName]
		oldCAPool := x509.NewCertPool()
		oldCAPool.AppendCertsFromPEM(oldCA)
		caPool := x509.NewCertPool()
		caPool.AppendCertsFromPEM(ca)
		if !caPool.Equal(oldCAPool) {
			caUpdated = true
		}
	}

	// check client cert need rotation
	if isCheckCert {
		oldCert := secret.Data[SecretCertName]
		certBlock, _ := pem.Decode(oldCert)
		if certBlock == nil {
			return fmt.Errorf("missing client cert")
		}
		if oldCertObj, err := x509.ParseCertificate(certBlock.Bytes); err != nil {
			return err
		} else if time.Now().AddDate(0, 0, util.DefaultRotateDays).After(oldCertObj.NotAfter) {
			// generate certificate
			subject := util.DefaultSubject
			subject.CommonName = normalizedClusterName
			cert, key, err := util.GenerateCertificate(&subject, util.DefaultValidDaysWithRotation)
			if err != nil {
				return err
			}
			// update PI and CCP cert
			if err = s.updatePIAndCCPCert(normalizedClusterName, string(obj.UID), cert); err != nil {
				return err
			}
			certRotated = true
			newCert = []byte(cert)
			newKey = []byte(key)
		}
	}

	if caUpdated || certRotated {
		log.Info("Update realized NSXServiceAccount", "namespace", obj.Namespace, "name", obj.Name)
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latestSecret := &v1.Secret{}
			if err := s.Client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: secretNamespace}, latestSecret); err != nil {
				return err
			}
			if err := validateServiceAccountSecretIdentity(latestSecret, obj, secret.UID); err != nil {
				return err
			}
			if latestSecret.Data == nil {
				latestSecret.Data = make(map[string][]byte)
			}
			if caUpdated {
				latestSecret.Data[CAName] = ca
			}
			if certRotated {
				latestSecret.Data[SecretCertName] = newCert
				latestSecret.Data[SecretKeyName] = newKey
			}
			return s.Client.Update(ctx, latestSecret)
		})
		if err != nil {
			return err
		}
	}

	// update NSX Restore Status
	if nsxRestoreStatus != nil && nsxRestoreStatus.Status == mpmodel.GlobalRestoreStatus_VALUE_SUCCESS &&
		!reflect.DeepEqual(nsxRestoreStatus, obj.Status.NSXRestoreStatus) {

		log.Info("Updating NSX restore status to NSXServiceAccount", "namespace", obj.Namespace, "name", obj.Name, "nsxRestoreStatus", nsxRestoreStatus)
		oldNSXRestoreStatus := obj.Status.NSXRestoreStatus
		obj.Status.NSXRestoreStatus = nsxRestoreStatus
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latestObj := &v1alpha1.NSXServiceAccount{}
			if err := s.Client.Get(ctx, types.NamespacedName{Name: obj.Name, Namespace: obj.Namespace}, latestObj); err != nil {
				return err
			}
			latestObj.Status.NSXRestoreStatus = nsxRestoreStatus
			if err := s.Client.Status().Update(ctx, latestObj); err != nil {
				return err
			}
			obj.ResourceVersion = latestObj.ResourceVersion
			obj.Status = latestObj.Status
			return nil
		})
		if err != nil {
			obj.Status.NSXRestoreStatus = oldNSXRestoreStatus
			log.Error(err, "failed to update NSXServiceAccount with nsxRestoreStatus", "namespace", obj.Namespace, "name", obj.Name, "nsxRestoreStatus", nsxRestoreStatus)
			return err
		}
	}
	return nil
}

// updateCCPCertificate applies a certificate without mutating the cached CCP on failure.
func (s *NSXServiceAccountService) updateCCPCertificate(name string, ccp model.ClusterControlPlane, cert string) (model.ClusterControlPlane, error) {
	ccp.Certificate = &cert
	updated, err := s.NSXClient.ClusterControlPlanesClient.Update(siteId, enforcementpointId, name, ccp)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		return model.ClusterControlPlane{}, err
	}
	s.ClusterControlPlaneStore.Add(&updated)
	return updated, nil
}

func (s *NSXServiceAccountService) updatePIAndCCPCert(normalizedClusterName, uid, cert string) error {
	hasPI := len(s.PrincipalIdentityStore.GetByIndex(common.TagScopeNSXServiceAccountCRUID, uid)) > 0
	hasCCP := len(s.ClusterControlPlaneStore.GetByIndex(common.TagScopeNSXServiceAccountCRUID, uid)) > 0
	piObj := s.PrincipalIdentityStore.GetByKey(normalizedClusterName)
	ccpObj := s.ClusterControlPlaneStore.GetByKey(normalizedClusterName)
	if !hasPI || !hasCCP || piObj == nil || ccpObj == nil {
		return fmt.Errorf("missing PI or CCP, cluster=%s", normalizedClusterName)
	}

	if _, err := s.updateCCPCertificate(normalizedClusterName, *ccpObj.(*model.ClusterControlPlane), cert); err != nil {
		return err
	}

	// update PI cert
	pi := piObj.(*mpmodel.PrincipalIdentity)
	return s.updatePICert(pi, normalizedClusterName, cert)
}

func (s *NSXServiceAccountService) updatePICert(pi *mpmodel.PrincipalIdentity, normalizedClusterName, cert string) error {
	// update PI cert
	oldCertId := ""
	if pi.CertificateId != nil {
		oldCertId = *pi.CertificateId
	}
	certList, err := s.NSXClient.CertificatesClient.Importcertificate(mpmodel.TrustObjectData{
		DisplayName: &normalizedClusterName,
		PemEncoded:  &cert,
	})
	if err != nil {
		err = nsxutil.TransNSXApiError(err)
		return err
	}

	if pi2, err := s.NSXClient.PrincipalIdentitiesClient.Updatecertificate(mpmodel.UpdatePrincipalIdentityCertificateRequest{
		CertificateId:       certList.Results[0].Id,
		PrincipalIdentityId: pi.Id,
	}); err != nil {
		err = nsxutil.TransNSXApiError(err)
		return err
	} else {
		pi = &pi2
		s.PrincipalIdentityStore.Add(pi)
	}
	if oldCertId != "" {
		if err := s.NSXClient.CertificatesClient.Delete(oldCertId); err != nil {
			err = nsxutil.TransNSXApiError(err)
			log.Error(err, "failed to delete", "PrincipalIdentity", *pi.Name, "Old Certificate", *pi.CertificateId)
		}
	}
	return nil
}

// ListNSXServiceAccountRealization returns all existing realized or failed NSXServiceAccount on NSXT
func (s *NSXServiceAccountService) ListNSXServiceAccountRealization() sets.Set[string] {
	// List PI
	uidSet := s.PrincipalIdentityStore.ListIndexFuncValues(common.TagScopeNSXServiceAccountCRUID)

	// List ClusterControlPlane
	uidSet = uidSet.Union(s.ClusterControlPlaneStore.ListIndexFuncValues(common.TagScopeNSXServiceAccountCRUID))
	return uidSet
}

func (s *NSXServiceAccountService) GetNSXServiceAccountNameByUID(uid string) (namespacedName types.NamespacedName) {
	objs, err := s.PrincipalIdentityStore.ByIndex(common.TagScopeNSXServiceAccountCRUID, uid)
	if err != nil {
		log.Error(err, "failed to search PrincipalIdentityStore by UID")
		return
	}
	for _, obj := range objs {
		pi := obj.(*mpmodel.PrincipalIdentity)
		for _, tag := range pi.Tags {
			switch *tag.Scope {
			case common.TagScopeNamespace:
				namespacedName.Namespace = *tag.Tag
			case common.TagScopeNSXServiceAccountCRName:
				namespacedName.Name = *tag.Tag
			}
			if namespacedName.Name != "" && namespacedName.Namespace != "" {
				return
			}
		}
	}
	objs, err = s.ClusterControlPlaneStore.ByIndex(common.TagScopeNSXServiceAccountCRUID, uid)
	if err != nil {
		log.Error(err, "failed to search ClusterControlPlaneStore by UID")
		return
	}
	for _, obj := range objs {
		ccp := obj.(*model.ClusterControlPlane)
		for _, tag := range ccp.Tags {
			if tag.Scope != nil {
				switch *tag.Scope {
				case common.TagScopeNamespace:
					namespacedName.Namespace = *tag.Tag
				case common.TagScopeNSXServiceAccountCRName:
					namespacedName.Name = *tag.Tag
				}
				if namespacedName.Name != "" && namespacedName.Namespace != "" {
					return
				}
			}
		}
	}
	return
}

func (s *NSXServiceAccountService) getClusterName(namespace, name string) string {
	return fmt.Sprintf("%s-%s-%s", s.NSXConfig.CoeConfig.Cluster, namespace, name)
}

func GenerateNSXServiceAccountConditions(existingConditions []metav1.Condition, generation int64, realizedStatus metav1.ConditionStatus, realizedReason string, message string) []metav1.Condition {
	var conditions []metav1.Condition
	lastTransitionTime := metav1.Now()
	for _, condition := range existingConditions {
		switch condition.Type {
		case v1alpha1.ConditionTypeRealized:
			if condition.Status == realizedStatus {
				lastTransitionTime = condition.LastTransitionTime
			}
		default:
			conditions = append(conditions, *condition.DeepCopy())
		}
	}
	conditions = append(conditions, metav1.Condition{
		Type:               v1alpha1.ConditionTypeRealized,
		Status:             realizedStatus,
		Reason:             realizedReason,
		ObservedGeneration: generation,
		LastTransitionTime: lastTransitionTime,
		Message:            message,
	})
	return conditions
}

func IsNSXServiceAccountRealized(status *v1alpha1.NSXServiceAccountStatus) bool {
	for _, condition := range status.Conditions {
		if condition.Type == v1alpha1.ConditionTypeRealized && condition.Status == metav1.ConditionTrue {
			return true
		}
	}
	return status.Phase == v1alpha1.NSXServiceAccountPhaseRealized
}

func (s *NSXServiceAccountService) GetNSXRestoreStatus() (*v1alpha1.NSXRestoreStatus, error) {
	clusterRestoreStatus, err := s.NSXClient.StatusClient.Get(nil)
	if err != nil {
		return nil, err
	}
	nsxRestoreStatus := v1alpha1.NSXRestoreStatus{}
	if clusterRestoreStatus.Id != nil {
		nsxRestoreStatus.Id = *clusterRestoreStatus.Id
	}
	if clusterRestoreStatus.Status != nil && clusterRestoreStatus.Status.Value != nil {
		nsxRestoreStatus.Status = *clusterRestoreStatus.Status.Value
	}
	if clusterRestoreStatus.RestoreEndTime != nil {
		nsxRestoreStatus.RestoreEndTime = *clusterRestoreStatus.RestoreEndTime
	}
	return &nsxRestoreStatus, nil
}

func (s *NSXServiceAccountService) UpdateConnectionInfoIfNeeded(ctx context.Context, obj *v1alpha1.NSXServiceAccount) error {
	proxyEndpoints, err := s.getProxyEndpoints(ctx)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(proxyEndpoints, obj.Status.ProxyEndpoints) ||
		!reflect.DeepEqual(s.NSXConfig.NsxApiManagers, obj.Status.NSXManagers) {
		obj.Status.NSXManagers = s.NSXConfig.NsxApiManagers
		obj.Status.ProxyEndpoints = proxyEndpoints
		return retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latestObj := &v1alpha1.NSXServiceAccount{}
			if err := s.Client.Get(ctx, types.NamespacedName{Name: obj.Name, Namespace: obj.Namespace}, latestObj); err != nil {
				return err
			}
			latestObj.Status.NSXManagers = obj.Status.NSXManagers
			latestObj.Status.ProxyEndpoints = obj.Status.ProxyEndpoints
			if err := s.Client.Status().Update(ctx, latestObj); err != nil {
				return err
			}
			obj.ResourceVersion = latestObj.ResourceVersion
			obj.Status = latestObj.Status
			return nil
		})
	}
	return nil
}

func (s *NSXServiceAccountService) CleanupBeforeVPCDeletion(ctx context.Context) error {
	ccpList := s.ClusterControlPlaneStore.List()
	log.Info("Starting cluster control plane cleanup", "count", len(ccpList), "status", "attempting")
	if len(ccpList) == 0 {
		log.Info("No cluster control plane found while cleanup", "count", 0)
		return nil
	}
	// Delete ClusterControlPlanes sequentially.
	// We cannot use PagingUpdateResources to delete ClusterControlPlanes in a batch because we need to pass a cascade=true request parameter.
	successCount := 0
	for i := range ccpList {
		ccp := ccpList[i].(*model.ClusterControlPlane)
		ccpID := *ccp.Id
		ccpName := ""
		if ccp.DisplayName != nil {
			ccpName = *ccp.DisplayName
		}
		log.Info("Attempting to delete cluster control plane", "ccpID", ccpID, "ccpName", ccpName, "index", i+1, "total", len(ccpList))
		err := s.DeleteClusterControlPlane(ctx, ccpID)
		if err != nil {
			log.Error(err, "Failed to delete cluster control plane from NSX", "ccpID", ccpID, "ccpName", ccpName, "status", "failed")
			return err
		}
		err = s.ClusterControlPlaneStore.Delete(&model.ClusterControlPlane{Id: ccp.Id})
		if err != nil {
			log.Error(err, "Failed to delete cluster control plane from store", "ccpID", ccpID, "ccpName", ccpName, "status", "failed")
			return err
		}
		log.Info("Successfully deleted cluster control plane", "ccpID", ccpID, "ccpName", ccpName, "status", "success")
		successCount++
	}
	log.Info("Completed cluster control plane cleanup", "successCount", successCount, "status", "success")
	return nil
}

func (s *NSXServiceAccountService) DeleteClusterControlPlane(ctx context.Context, ccpID string) error {
	log.Info("Send request to NSX to delete cluster control plane", "ClusterControlPlane", ccpID)
	cascade := true
	err := s.NSXClient.ClusterControlPlanesClient.Delete(siteId, enforcementpointId, ccpID, &cascade)
	return err
}
