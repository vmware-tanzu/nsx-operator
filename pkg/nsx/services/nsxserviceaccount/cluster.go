/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsxserviceaccount

import (
	"context"
	"fmt"
	"sync"

	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

const (
	siteId             = "default"
	enforcementpointId = "default"
	PortRestAPI        = "rest-api"
	PortNSXRPCFwdProxy = "nsx-rpc-fwd-proxy"
	SecretSuffix       = "-nsx-cert"
	SecretCAName       = "ca.crt"
	SecretCertName     = "tls.crt"
	SecretKeyName      = "tls.key"
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

	// generate certificate
	subject := util.DefaultSubject
	subject.CommonName = normalizedClusterName
	cert, key, err := util.GenerateCertificate(&subject, util.DefaultValidDays)
	if err != nil {
		return err
	}

	// create PI and CCP
	clusterId, err := s.createPIAndCCP(normalizedClusterName, vpcPath, cert, nil, obj)
	if err != nil {
		return err
	}

	// create Secret
	secretName := obj.Name + SecretSuffix
	secretNamespace := obj.Namespace
	if err := s.Client.Create(ctx, &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: secretNamespace,
			// TODO: Add labels/annotations
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
		// TODO: Add NSX CA
		Data: map[string][]byte{SecretCertName: []byte(cert), SecretKeyName: []byte(key)},
		Type: "",
	}); err != nil {
		return err
	}

	// update NSXServiceAccountStatus
	obj.Status.Phase = v1alpha1.NSXServiceAccountPhaseRealized
	obj.Status.Reason = "Success"
	obj.Status.Conditions = GenerateNSXServiceAccountConditions(obj.Status.Conditions, obj.Generation, metav1.ConditionTrue, v1alpha1.ConditionReasonRealizationSuccess, "Success.")
	obj.Status.NSXManagers = s.NSXConfig.NsxApiManagers
	obj.Status.ClusterID = clusterId
	obj.Status.ClusterName = normalizedClusterName
	obj.Status.Secrets = []v1alpha1.NSXSecret{{
		Name:      secretName,
		Namespace: secretNamespace,
	}}
	obj.Status.VPCPath = vpcPath
	obj.Status.ProxyEndpoints = proxyEndpoints
	return s.Client.Status().Update(ctx, obj)
}

// UpdateRealizedNSXServiceAccount checks if PI/CCP is created on NSXT for a realized NSXServiceAccount. If both PI/CCP
// is missing, restore PI/CCP from realized NSXServiceAccount and Secret.
func (s *NSXServiceAccountService) UpdateRealizedNSXServiceAccount(ctx context.Context, obj *v1alpha1.NSXServiceAccount) error {
	normalizedClusterName := obj.Status.ClusterName

	// check PI and CCP is missing
	piObj := s.PrincipalIdentityStore.GetByKey(normalizedClusterName)
	ccpObj := s.ClusterControlPlaneStore.GetByKey(normalizedClusterName)
	if piObj != nil && ccpObj != nil {
		return nil
	} else if (piObj != nil) != (ccpObj != nil) {
		return fmt.Errorf("PI/CCP doesn't match")
	}
	_, err := s.NSXClient.ClusterControlPlanesClient.Get(siteId, enforcementpointId, normalizedClusterName)
	if err == nil {
		return fmt.Errorf("CCP store is not synchronized")
	}
	switch err.(type) {
	case vapierrors.NotFound:
	default:
		return err
	}

	log.Info("Start to restore realized resource", "nsxserviceaccount", types.NamespacedName{Name: obj.Name, Namespace: obj.Namespace})
	// read Secret
	secretName := obj.Status.Secrets[0].Name
	secretNamespace := obj.Status.Secrets[0].Namespace
	secret := &v1.Secret{}
	if err := s.Client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: secretNamespace}, secret); err != nil {
		return err
	}
	cert := secret.Data[SecretCertName]
	vpcPath := obj.Status.VPCPath
	existingClusterId := obj.Status.ClusterID

	// restore PI and CCP
	_, err = s.createPIAndCCP(normalizedClusterName, vpcPath, string(cert), &existingClusterId, obj)
	return err
}

func (s *NSXServiceAccountService) createPIAndCCP(normalizedClusterName string, vpcPath string, cert string, existingClusterId *string, obj *v1alpha1.NSXServiceAccount) (string, error) {
	// create PI
	if piObj := s.PrincipalIdentityStore.GetByKey(normalizedClusterName); piObj == nil {
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
		if err != nil {
			return "", err
		}
		s.PrincipalIdentityStore.Add(pi)
	}

	// create ClusterControlPlane
	clusterId := ""
	if ccpObj := s.ClusterControlPlaneStore.GetByKey(normalizedClusterName); ccpObj == nil {
		ccp, err := s.NSXClient.ClusterControlPlanesClient.Update(siteId, enforcementpointId, normalizedClusterName, model.ClusterControlPlane{
			Revision:     &revision1,
			ResourceType: &antreaClusterResourceType,
			Certificate:  &cert,
			VhcPath:      &vpcPath,
			NodeId:       existingClusterId,
			Tags:         s.buildBasicTags(obj),
		})
		if err != nil {
			return "", err
		}
		s.ClusterControlPlaneStore.Add(ccp)
		clusterId = *ccp.NodeId
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

func (s *NSXServiceAccountService) DeleteNSXServiceAccount(ctx context.Context, namespacedName types.NamespacedName) error {
	clusterName := s.getClusterName(namespacedName.Namespace, namespacedName.Name)
	normalizedClusterName := util.NormalizeId(clusterName)
	// delete Secret
	secretName := namespacedName.Name + SecretSuffix
	secretNamespace := namespacedName.Namespace
	if err := s.Client.Delete(ctx, &v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: secretNamespace}}); err != nil && !errors.IsNotFound(err) {
		log.Error(err, "failed to delete", "secret", secretName, "namespace", secretNamespace)
		return err
	}

	// delete ClusterControlPlane
	cascade := true
	if err := s.NSXClient.ClusterControlPlanesClient.Delete(siteId, enforcementpointId, normalizedClusterName, &cascade); err != nil {
		log.Error(err, "failed to delete", "ClusterControlPlane", normalizedClusterName)
		return err
	}
	s.ClusterControlPlaneStore.Delete(model.ClusterControlPlane{Id: &normalizedClusterName})

	// delete PI
	if piobj := s.PrincipalIdentityStore.GetByKey(normalizedClusterName); piobj != nil {
		pi := piobj.(mpmodel.PrincipalIdentity)
		if err := s.NSXClient.PrincipalIdentitiesClient.Delete(*pi.Id); err != nil {
			log.Error(err, "failed to delete", "PrincipalIdentity", *pi.Name)
			return err
		}
		if pi.CertificateId != nil && *pi.CertificateId != "" {
			if err := s.NSXClient.CertificatesClient.Delete(*pi.CertificateId); err != nil {
				log.Error(err, "failed to delete", "PrincipalIdentity", *pi.Name, "Certificate", *pi.CertificateId)
				return err
			}
		}
		s.PrincipalIdentityStore.Delete(pi)
	}
	return nil
}

// ListNSXServiceAccountRealization returns all existing realized or failed NSXServiceAccount on NSXT
func (s *NSXServiceAccountService) ListNSXServiceAccountRealization() sets.String {
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
		pi := obj.(mpmodel.PrincipalIdentity)
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
		ccp := obj.(model.ClusterControlPlane)
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

func IsNSXServiceAccountRealized(status v1alpha1.NSXServiceAccountStatus) bool {
	for _, condition := range status.Conditions {
		if condition.Type == v1alpha1.ConditionTypeRealized && condition.Status == metav1.ConditionTrue {
			return true
		}
	}
	return status.Phase == v1alpha1.NSXServiceAccountPhaseRealized
}
