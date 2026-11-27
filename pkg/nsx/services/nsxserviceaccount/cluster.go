/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsxserviceaccount

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"reflect"
	"strings"
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
	siteId                = "default"
	enforcementpointId    = "default"
	PortRestAPI           = "rest-api"
	PortNumRestAPI        = 10091
	PortNSXRPCFwdProxy    = "nsx-rpc-fwd-proxy"
	PortNumNSXRPCFwdProxy = 10092
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

	// accountLocks holds one *sync.Mutex per normalized cluster name. Reconcile, restore
	// and the GC-driven cert rotation run on different goroutines and the workqueue only
	// serializes reconciles of the same key against each other, so without this they
	// could update the PI/CCP and the Secret of one NSXServiceAccount concurrently.
	accountLocks sync.Map
}

// lockAccount serializes the operations that read or change the credentials of one
// NSXServiceAccount (Secret, PI and CCP). Use it as `defer s.lockAccount(name)()` in
// the exported entry points only, never in helpers they call, as the mutex is not reentrant.
// Mutexes are kept for the life of the process; there is one per NSXServiceAccount.
func (s *NSXServiceAccountService) lockAccount(normalizedClusterName string) func() {
	m, _ := s.accountLocks.LoadOrStore(normalizedClusterName, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
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
)

func evaluateSecretOwnership(secret *v1.Secret, obj *v1alpha1.NSXServiceAccount) secretOwnership {
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
		// Preserve an existing CA when no replacement CA is configured.
		if len(desiredSecret.Data[CAName]) == 0 {
			if ca, exists := existingSecret.Data[CAName]; exists {
				desiredSecret.Data[CAName] = ca
			}
		}
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
	clusterName := s.getClusterName(string(obj.UID), obj.Namespace, obj.Name)
	normalizedClusterName := util.NormalizeId(clusterName)
	// Held from before the Secret is read so that a concurrent cert rotation cannot
	// replace the Secret's cert after we picked it but before it is sent to NSX.
	defer s.lockAccount(normalizedClusterName)()
	// TODO: Use WCPConfig.NSXTProject as project when WCPConfig.EnableWCPVPCNetwork is true
	project := s.NSXConfig.CoeConfig.Cluster
	vpcName := obj.Namespace + "-default-vpc"
	vpcPath := fmt.Sprintf("/orgs/default/projects/%s/vpcs/%s", util.NormalizeId(project), vpcName)

	// get proxy
	proxyEndpoints, err := s.getProxyEndpoints(ctx, obj)
	if err != nil {
		return err
	}

	secret, err := s.ensureServiceAccountSecret(ctx, obj, normalizedClusterName)
	if err != nil {
		return err
	}

	// Reuse the recorded ClusterID, if any, so that PI/CCP missing from NSX (e.g. after an NSX restore)
	// are recreated with the same node ID, while existing ones just get their certificates updated.
	// Note: the NSX cluster control plane API doesn't allow updating node_id (aka clusterID), so
	// reusing existingClusterId here is safe. NSX will not allow an arbitrary obj.Status.ClusterID
	// value to impersonate another cluster's control plane.
	var existingClusterId *string
	if obj.Status.ClusterID != "" {
		existingClusterId = &obj.Status.ClusterID
	}
	clusterId, err := s.createPIAndCCP(normalizedClusterName, vpcPath, string(secret.Data[SecretCertName]), existingClusterId, obj)
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

// RestoreRealizedNSXServiceAccount restores missing PI/CCP resources for a realized
// NSXServiceAccount, including a PI left behind by a failed CCP restore.
func (s *NSXServiceAccountService) RestoreRealizedNSXServiceAccount(ctx context.Context, obj *v1alpha1.NSXServiceAccount) error {
	normalizedClusterName := obj.Status.ClusterName
	defer s.lockAccount(normalizedClusterName)()

	// check PI and CCP is missing
	hasPI := len(s.PrincipalIdentityStore.GetByIndex(common.TagScopeNSXServiceAccountCRUID, string(obj.UID))) > 0
	hasCCP := len(s.ClusterControlPlaneStore.GetByIndex(common.TagScopeNSXServiceAccountCRUID, string(obj.UID))) > 0
	piObj := s.PrincipalIdentityStore.GetByKey(normalizedClusterName)
	ccpObj := s.ClusterControlPlaneStore.GetByKey(normalizedClusterName)
	var pi *mpmodel.PrincipalIdentity
	if piObj != nil {
		pi = piObj.(*mpmodel.PrincipalIdentity)
	}
	// TODO: Generate fresh Secret credentials when recreating PI/CCP during
	// restore instead of reusing the discovered Secret. Coordinate the new
	// credentials with consumers as part of that separate restore enhancement.
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

	if hasPI && hasCCP && piObj != nil && ccpObj != nil {
		if string(cert) == "" {
			return nil
		}
		same, err := s.piHasCertificate(pi, string(cert))
		if err != nil {
			return err
		}
		if !same {
			return s.updatePICert(pi, normalizedClusterName, string(cert))
		}
		return nil
	}
	// A failed CCP update can leave a PI owned by this account in NSX and in the
	// store. Resume that partial restore, but reject conflicting ownership and
	// other inconsistent store states.
	partialRestore := hasPI && pi != nil && !hasCCP && ccpObj == nil &&
		nsxutil.FindTag(common.ConvertMPTagsToTags(pi.Tags), common.TagScopeNSXServiceAccountCRUID) == string(obj.UID)
	if !partialRestore && (hasPI || hasCCP || piObj != nil || ccpObj != nil) {
		return fmt.Errorf("PI/CCP doesn't match")
	}

	if requeueAfter, skip := CCPConnectionCapacityFullBackoffRequeue(time.Now()); skip {
		log.Info("skipping NSX API calls for restore while cluster control plane connection capacity backoff is active",
			"nsxserviceaccount", types.NamespacedName{Name: obj.Name, Namespace: obj.Namespace}, "requeueAfter", requeueAfter)
		return CCPBackoffError{RequeueAfter: requeueAfter}
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
	hasCCP := len(s.ClusterControlPlaneStore.GetByIndex(common.TagScopeNSXServiceAccountCRUID, string(obj.UID))) > 0
	ccpObj := s.ClusterControlPlaneStore.GetByKey(normalizedClusterName)
	if !hasCCP != (ccpObj == nil) {
		return "", fmt.Errorf("old CCP exists")
	}
	// Only new CCPs consume capacity. Allow credential updates for existing
	// accounts even when another account has exhausted the connection limit.
	if ccpObj == nil {
		if err := checkCCPCapacityBackoff(); err != nil {
			return "", err
		}
	}
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
		pi := piObj.(*mpmodel.PrincipalIdentity)
		// The cert is reused from the Secret across reconciles, so the PI may already
		// hold it (e.g. a duplicate reconcile right after a successful one). NSX rejects
		// importing the same certificate again with MP2038, so only update on a change.
		same, err := s.piHasCertificate(pi, cert)
		if err != nil {
			return "", err
		}
		if same {
			log.Info("PI already has the certificate, skip updating it", "PrincipalIdentity", normalizedClusterName)
		} else {
			log.Info("Update PI with new cert", "PrincipalIdentity", normalizedClusterName)
			if err := s.updatePICert(pi, normalizedClusterName, cert); err != nil {
				return "", err
			}
		}
	}

	// Prepare a new CCP or update the existing CCP with the same persisted certificate.
	ccp := model.ClusterControlPlane{
		Revision: &revision1, ResourceType: &antreaClusterResourceType,
		VhcPath: &vpcPath, NodeId: existingClusterId, Tags: s.buildBasicTags(obj),
	}
	if ccpObj != nil {
		ccp = *ccpObj.(*model.ClusterControlPlane)
	} else if err := checkCCPCapacityBackoff(); err != nil {
		// Another worker may have discovered full capacity during the PI call.
		return "", err
	}
	updated := ccp
	if ccp.Certificate != nil && samePEMCertificate(*ccp.Certificate, cert) {
		// The cert is reused from the Secret across reconciles, so the CCP may already
		// hold it. NSX rejects re-sending an unchanged CCP (HTTP 500, error code 100),
		// so only write the CCP when its certificate changes.
		log.Debug("CCP already has the certificate, skip updating it")
	} else {
		ccp.Certificate = &cert
		var err error
		updated, err = s.updateClusterControlPlane(normalizedClusterName, ccp)
		if err != nil {
			return "", err
		}
	}
	nodeID := updated.NodeId
	if nodeID == nil {
		nodeID = ccp.NodeId
	}
	if nodeID == nil || *nodeID == "" {
		nodeID = existingClusterId
	}
	if nodeID == nil || *nodeID == "" {
		return "", fmt.Errorf("cluster control plane %s has no node ID", normalizedClusterName)
	}
	// Preserve the node ID in the cache when NSX omits it from the response.
	updated.NodeId = nodeID
	s.ClusterControlPlaneStore.Add(&updated)
	return *nodeID, nil
}

func (s *NSXServiceAccountService) getProxyType(obj *v1alpha1.NSXServiceAccount) v1alpha1.NSXServiceAccountProxyType {
	if obj.Spec.Proxy != "" {
		return obj.Spec.Proxy
	}
	return v1alpha1.SupervisorManagementProxy
}

func (s *NSXServiceAccountService) getProxyEndpoints(ctx context.Context, obj *v1alpha1.NSXServiceAccount) (v1alpha1.NSXProxyEndpoint, error) {
	proxyType := s.getProxyType(obj)

	if proxyType == v1alpha1.VMCIProxy {
		return v1alpha1.NSXProxyEndpoint{
			Addresses: []v1alpha1.NSXProxyEndpointAddress{{IP: "127.0.0.1"}},
			Ports: []v1alpha1.NSXProxyEndpointPort{
				{Name: PortRestAPI, Port: PortNumRestAPI, Protocol: v1alpha1.NSXProxyProtocolTCP},
				{Name: PortNSXRPCFwdProxy, Port: PortNumNSXRPCFwdProxy, Protocol: v1alpha1.NSXProxyProtocolTCP},
			},
		}, nil
	}

	proxyEndpoints := v1alpha1.NSXProxyEndpoint{}
	proxies := &v1.ServiceList{}
	if err := s.Client.List(ctx, proxies, client.MatchingLabels(proxyLabels)); err != nil {
		return proxyEndpoints, err
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

func (s *NSXServiceAccountService) getClusterControlPlaneByNSXServiceAccountUID(uid string) (*model.ClusterControlPlane, error) {
	objs, err := s.ClusterControlPlaneStore.ByIndex(common.TagScopeNSXServiceAccountCRUID, uid)
	if err != nil {
		return nil, err
	}
	for _, obj := range objs {
		ccp := obj.(*model.ClusterControlPlane)
		return ccp, nil
	}
	return nil, nil
}

func (s *NSXServiceAccountService) getPrincipalIdentityByNSXServiceAccountUID(uid string) (*mpmodel.PrincipalIdentity, error) {
	objs, err := s.PrincipalIdentityStore.ByIndex(common.TagScopeNSXServiceAccountCRUID, uid)
	if err != nil {
		return nil, err
	}
	for _, obj := range objs {
		pi := obj.(*mpmodel.PrincipalIdentity)
		return pi, nil
	}
	return nil, nil
}

func (s *NSXServiceAccountService) DeleteNSXServiceAccount(ctx context.Context, namespacedName types.NamespacedName, uid types.UID) error {
	isDeleteSecret := false
	nsxsa := &v1alpha1.NSXServiceAccount{}
	if err := s.Client.Get(ctx, namespacedName, nsxsa); err != nil {
		isDeleteSecret = true
	} else if uid == nsxsa.UID {
		isDeleteSecret = true
	}

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

	// delete ClusterControlPlane
	ccp, err := s.getClusterControlPlaneByNSXServiceAccountUID(string(uid))
	if err != nil {
		log.Error(err, "failed to search ClusterControlPlaneStore by NSXServiceAccount UID", "UID", uid)
		return err
	}
	if ccp != nil && ccp.Id != nil {
		if err := s.DeleteClusterControlPlane(ctx, *ccp.Id); err != nil {
			err = nsxutil.TransNSXApiError(err)
			log.Error(err, "failed to delete", "ClusterControlPlane", *ccp.Id)
			return err
		}
		s.ClusterControlPlaneStore.Delete(ccp)
	}

	// delete PI
	pi, err := s.getPrincipalIdentityByNSXServiceAccountUID(string(uid))
	if err != nil {
		log.Error(err, "failed to search PrincipalIdentityStore by NSXServiceAccount UID", "UID", uid)
		return err
	}
	if pi != nil {
		if err := s.NSXClient.PrincipalIdentitiesClient.Delete(*pi.Id); err != nil {
			err = nsxutil.TransNSXApiError(err)
			log.Error(err, "failed to delete", "PrincipalIdentity", *pi.Name)
			return err
		}
		if pi.CertificateId != nil && *pi.CertificateId != "" {
			if err := s.NSXClient.CertificatesClient.Delete(*pi.CertificateId); err != nil {
				err = nsxutil.TransNSXApiError(err)
				log.Error(err, "Failed to delete the certificate of the PrincipalIdentity", "PrincipalIdentity", *pi.Name, "Certificate", *pi.CertificateId)
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

	clusterName := s.getClusterName(string(obj.UID), obj.Namespace, obj.Name)
	normalizedClusterName := util.NormalizeId(clusterName)
	// Cert rotation changes the PI, CCP and Secret together, so it must not interleave
	// with a reconcile or restore of the same NSXServiceAccount.
	defer s.lockAccount(normalizedClusterName)()
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

// updateClusterControlPlane shares error handling and cache updates for CCP writes.
// Callers creating a CCP check capacity backoff; updates must remain available.
func (s *NSXServiceAccountService) updateClusterControlPlane(name string, ccp model.ClusterControlPlane) (model.ClusterControlPlane, error) {
	updated, err := s.NSXClient.ClusterControlPlanesClient.Update(siteId, enforcementpointId, name, ccp)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		if nsxutil.IsCCPConnectionCapacityFullError(err) {
			MarkCCPConnectionCapacityFull()
		}
		return model.ClusterControlPlane{}, err
	}
	// Keep the certificate we wrote in the cache even if NSX omits it from the response,
	// so that later reconciles can tell the CCP already has it and skip rewriting it.
	if updated.Certificate == nil {
		updated.Certificate = ccp.Certificate
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

	ccp := *ccpObj.(*model.ClusterControlPlane)
	ccp.Certificate = &cert
	if _, err := s.updateClusterControlPlane(normalizedClusterName, ccp); err != nil {
		return err
	}

	// update PI cert
	pi := piObj.(*mpmodel.PrincipalIdentity)
	return s.updatePICert(pi, normalizedClusterName, cert)
}

// samePEMCertificate reports whether two PEM strings hold the same X.509 certificate.
// It compares the DER bytes of the first CERTIFICATE block so that differences in
// line endings, trailing newlines or surrounding text do not matter.
func samePEMCertificate(a, b string) bool {
	derA := firstCertificateDER(a)
	return len(derA) > 0 && bytes.Equal(derA, firstCertificateDER(b))
}

func firstCertificateDER(pemData string) []byte {
	rest := []byte(pemData)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil
		}
		if block.Type == "CERTIFICATE" {
			return block.Bytes
		}
	}
}

// piHasCertificate reports whether the certificate currently attached to the PI
// in NSX is the given PEM certificate.
func (s *NSXServiceAccountService) piHasCertificate(pi *mpmodel.PrincipalIdentity, cert string) (bool, error) {
	if pi.CertificateId == nil || *pi.CertificateId == "" {
		return false, nil
	}
	detail := true
	existing, err := s.NSXClient.CertificatesClient.Get(*pi.CertificateId, &detail, nil)
	if err != nil {
		err = nsxutil.TransNSXApiError(err)
		// A certificate that no longer exists in NSX is not the given one. This happens
		// when the cached CertificateId is stale (e.g. after an NSX restore), and the
		// caller must then go on to import the certificate as it did before this check.
		if nsxErr, ok := err.(*nsxutil.NSXApiError); ok && nsxErr.Type() == vapierrors.ErrorType_NOT_FOUND {
			return false, nil
		}
		return false, err
	}
	if existing.PemEncoded == nil {
		return false, nil
	}
	return samePEMCertificate(*existing.PemEncoded, cert), nil
}

// certSearchPageSize is the page size for the certificate thumbprint search. The
// search normally returns one hit, the pagination is only a safeguard.
var certSearchPageSize = int64(50)

// certificateThumbprint returns the SHA-256 thumbprint of the first certificate in the PEM
// in the format NSX stores it (upper case hex separated by colons), or "" if there is none.
func certificateThumbprint(pemData string) string {
	der := firstCertificateDER(pemData)
	if len(der) == 0 {
		return ""
	}
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// findCertificateID looks up the ID of a certificate that is already imported in NSX.
// It searches by thumbprint without a resource_type filter, because NSX (observed on
// 9.2) indexes a self-signed certificate imported without a private key as
// certificate_ca, not certificate_self_signed. The DER bytes are compared as well, so
// that only the very same certificate is returned.
// It returns "" if the certificate is not found, e.g. because the search index lags
// behind the import by a moment.
func (s *NSXServiceAccountService) findCertificateID(cert string) (string, error) {
	thumbprint := certificateThumbprint(cert)
	if thumbprint == "" {
		return "", fmt.Errorf("invalid certificate PEM")
	}
	query := fmt.Sprintf("leaf_certificate_sha_256_thumbprint:\"%s\"", thumbprint)
	var cursor *string
	for {
		resp, err := s.NSXClient.MPQueryClient.List(query, cursor, nil, &certSearchPageSize, nil, nil)
		if err != nil {
			return "", nsxutil.TransNSXApiError(err)
		}
		for _, entity := range resp.Results {
			obj, errs := common.NewConverter().ConvertToGolang(entity, mpmodel.CertificateBindingType())
			if len(errs) > 0 {
				// One unreadable hit must not hide a matching one.
				log.Error(errs[0], "Failed to convert a certificate search result, skipping it")
				continue
			}
			found, ok := obj.(mpmodel.Certificate)
			if !ok || found.Id == nil || found.PemEncoded == nil {
				continue
			}
			if samePEMCertificate(*found.PemEncoded, cert) {
				return *found.Id, nil
			}
		}
		if resp.Cursor == nil || *resp.Cursor == "" {
			return "", nil
		}
		cursor = resp.Cursor
	}
}

// refreshPI re-reads the PI from NSX and updates the store, so that decisions are not
// made on a cached PI that went stale, e.g. after an Updatecertificate whose response was lost.
func (s *NSXServiceAccountService) refreshPI(pi *mpmodel.PrincipalIdentity) (*mpmodel.PrincipalIdentity, error) {
	if pi.Id == nil {
		return nil, fmt.Errorf("PrincipalIdentity has no ID")
	}
	fresh, err := s.NSXClient.PrincipalIdentitiesClient.Get(*pi.Id)
	if err != nil {
		return nil, nsxutil.TransNSXApiError(err)
	}
	if fresh.Name == nil {
		// The store is keyed by name, which NSX always returns; don't rely on it.
		fresh.Name = pi.Name
	}
	s.PrincipalIdentityStore.Add(&fresh)
	return &fresh, nil
}

func (s *NSXServiceAccountService) updatePICert(pi *mpmodel.PrincipalIdentity, normalizedClusterName, cert string) error {
	// update PI cert
	oldCertId := ""
	if pi.CertificateId != nil {
		oldCertId = *pi.CertificateId
	}
	// imported is true if this call imported the certificate, so that only then it is
	// this call's job to remove it again if the PI cannot be switched to it.
	imported := true
	var newCertId *string
	certList, err := s.NSXClient.CertificatesClient.Importcertificate(mpmodel.TrustObjectData{
		DisplayName: &normalizedClusterName,
		PemEncoded:  &cert,
	})
	if err != nil {
		err = nsxutil.TransNSXApiError(err)
		if !nsxutil.HasAnyErrorCode(err, nsxutil.CertificateAlreadyExistsErrorCode) {
			return err
		}
		// MP2038: the certificate is already in NSX. Either the PI already uses it, or an
		// earlier attempt imported it and was interrupted before switching the PI to it
		// (or the switch succeeded but its response was lost). The cached PI may be stale,
		// so recover from NSX's current state instead of failing on every retry.
		imported = false
		fresh, refreshErr := s.refreshPI(pi)
		if refreshErr != nil {
			return refreshErr
		}
		pi = fresh
		if same, getErr := s.piHasCertificate(pi, cert); getErr != nil {
			return getErr
		} else if same {
			log.Info("PrincipalIdentity already has the certificate, skip updating it", "PrincipalIdentity", normalizedClusterName)
			return nil
		}
		existingId, findErr := s.findCertificateID(cert)
		if findErr != nil {
			return findErr
		}
		if existingId == "" {
			// Probably temporary: the search index can lag behind an import for a moment, so
			// a later retry should find the certificate. The error is returned for that retry.
			return fmt.Errorf("certificate already exists in NSX but was not found by thumbprint yet, will retry: %w", err)
		}
		log.Info("Reusing certificate already imported in NSX", "PrincipalIdentity", normalizedClusterName, "Certificate", existingId)
		newCertId = &existingId
	} else {
		if len(certList.Results) == 0 || certList.Results[0].Id == nil {
			// Without its ID the certificate cannot be attached to the PI. It stays in NSX,
			// and the next retry finds it through the MP2038 recovery.
			return fmt.Errorf("NSX imported the certificate for PrincipalIdentity %s but returned no certificate ID", normalizedClusterName)
		}
		newCertId = certList.Results[0].Id
	}

	pi2, err := s.NSXClient.PrincipalIdentitiesClient.Updatecertificate(mpmodel.UpdatePrincipalIdentityCertificateRequest{
		CertificateId:       newCertId,
		PrincipalIdentityId: pi.Id,
	})
	if err != nil {
		err = nsxutil.TransNSXApiError(err)
		// The failure may be a lost response of an update NSX did apply, so look at the PI
		// in NSX before deciding whether the imported certificate is orphaned.
		fresh, refreshErr := s.refreshPI(pi)
		if refreshErr != nil {
			// Unknown state: keep the certificate. The next retry hits MP2038 again and
			// recovers from it, provided the thumbprint search finds the certificate.
			return err
		}
		if fresh.CertificateId != nil && *fresh.CertificateId == *newCertId {
			log.Info("PrincipalIdentity certificate was updated despite the error", "PrincipalIdentity", normalizedClusterName, "error", err.Error())
			pi2 = *fresh
		} else {
			if imported {
				if delErr := s.NSXClient.CertificatesClient.Delete(*newCertId); delErr != nil {
					log.Error(nsxutil.TransNSXApiError(delErr), "Failed to delete the orphaned certificate", "PrincipalIdentity", normalizedClusterName, "Certificate", *newCertId)
				}
			}
			return err
		}
	}
	pi = &pi2
	s.PrincipalIdentityStore.Add(pi)
	if oldCertId != "" && oldCertId != *newCertId {
		if err := s.NSXClient.CertificatesClient.Delete(oldCertId); err != nil {
			err = nsxutil.TransNSXApiError(err)
			// The PI already uses the new certificate, so this is only a leftover in NSX.
			log.Error(err, "Failed to delete the old certificate of the PrincipalIdentity", "PrincipalIdentity", normalizedClusterName, "Old Certificate", oldCertId)
		} else {
			log.Info("Deleted the old certificate of the PrincipalIdentity", "PrincipalIdentity", normalizedClusterName, "Old Certificate", oldCertId)
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
	pi, err := s.getPrincipalIdentityByNSXServiceAccountUID(uid)
	if err != nil {
		log.Error(err, "failed to search PrincipalIdentityStore by NSXServiceAccount UID", "UID", uid)
		return
	}
	if pi != nil {
		for _, tag := range pi.Tags {
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

	ccp, err := s.getClusterControlPlaneByNSXServiceAccountUID(uid)
	if err != nil {
		log.Error(err, "failed to search ClusterControlPlaneStore by NSXServiceAccount UID", "UID", uid)
		return
	}
	if ccp != nil {
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

// getNormalizedClusterNameByUID returns the normalized cluster name of an existing NSXServiceAccount,
// parsed from its PrincipalIdentity NodeId or ClusterControlPlane Id. It returns "" if neither exists.
// NSXServiceAccount.Status.ClusterName is deliberately not used because users can modify it.
func (s *NSXServiceAccountService) getNormalizedClusterNameByUID(uid string) string {
	pi, err := s.getPrincipalIdentityByNSXServiceAccountUID(uid)
	if err != nil {
		log.Error(err, "failed to search PrincipalIdentityStore by NSXServiceAccount UID", "UID", uid)
		return ""
	}
	if pi != nil && pi.NodeId != nil && *pi.NodeId != "" {
		return *pi.NodeId
	}

	ccp, err := s.getClusterControlPlaneByNSXServiceAccountUID(uid)
	if err != nil {
		log.Error(err, "failed to search ClusterControlPlaneStore by NSXServiceAccount UID", "UID", uid)
		return ""
	}
	if ccp != nil && ccp.Id != nil {
		return *ccp.Id
	}
	return ""
}

// getClusterName reuses the cluster name of an existing PI/CCP of the NSXServiceAccount so that
// existing NSX objects keep their names (e.g. legacy names using "-" as delimiter). Otherwise it
// generates a new name.
func (s *NSXServiceAccountService) getClusterName(uid, namespace, name string) string {
	if clusterName := s.getNormalizedClusterNameByUID(uid); clusterName != "" {
		return clusterName
	}
	return fmt.Sprintf("%s_%s_%s", s.NSXConfig.CoeConfig.Cluster, namespace, name)
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
	proxyEndpoints, err := s.getProxyEndpoints(ctx, obj)
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
