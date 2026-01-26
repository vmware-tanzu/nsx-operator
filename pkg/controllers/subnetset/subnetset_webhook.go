/* Copyright © 2024 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetset

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	controllercommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var NSXOperatorSA = "system:serviceaccount:vmware-system-nsx:ncp-svc-account"

// Create validator instead of using the existing one in controller-runtime because the existing one can't
// inspect admission.Request in Handle function.

// +kubebuilder:webhook:path=/validate-crd-nsx-vmware-com-v1alpha1-subnetset,mutating=false,failurePolicy=fail,sideEffects=None,
// groups=crd.nsx.vmware.com,resources=subnetsets,verbs=create;update;delete,versions=v1alpha1,
// name=subnetset.validating.crd.nsx.vmware.com,admissionReviewVersions=v1

type SubnetSetValidator struct {
	Client            client.Client
	decoder           admission.Decoder
	nsxClient         *nsx.Client
	vpcService        common.VPCServiceProvider
	subnetService     common.SubnetServiceProvider
	subnetPortService common.SubnetPortServiceProvider
}

type SubnetSetType string

const (
	SubnetSetTypePreCreated  SubnetSetType = "PreCreated"
	SubnetSetTypeAutoCreated SubnetSetType = "AutoCreated"
	// Subnetset without Spec.SubnetNames or Spec.IPv4SubnetSize/AccessMode/SubnetDHCPConfig
	SubnetSetTypeNone SubnetSetType = "None"
)

func defaultSubnetSetLabelChanged(oldSubnetSet, subnetSet *v1alpha1.SubnetSet) bool {
	var oldValue, value string
	oldValue, oldExists := oldSubnetSet.ObjectMeta.Labels[common.LabelDefaultNetwork]
	value, exists := subnetSet.ObjectMeta.Labels[common.LabelDefaultNetwork]
	return oldExists != exists || oldValue != value
}

func isDefaultSubnetSet(s *v1alpha1.SubnetSet) bool {
	if _, ok := s.Labels[common.LabelDefaultNetwork]; ok {
		return true
	}
	// keep the old logic for backward compatibility
	if _, ok := s.Labels[common.LabelDefaultSubnetSet]; ok {
		return true
	}
	return s.Name == common.DefaultVMSubnetSet || s.Name == common.DefaultPodSubnetSet
}

func hasExclusiveFields(s *v1alpha1.SubnetSet) bool {
	return s.Spec.SubnetNames != nil && (s.Spec.IPv4SubnetSize != 0 || s.Spec.AccessMode != "" || s.Spec.SubnetDHCPConfig.Mode != "")
}

func subnetSetType(s *v1alpha1.SubnetSet) SubnetSetType {
	if s.Spec.SubnetNames != nil {
		return SubnetSetTypePreCreated
	}
	if s.Spec.IPv4SubnetSize != 0 || s.Spec.AccessMode != "" || s.Spec.SubnetDHCPConfig.Mode != "" {
		return SubnetSetTypeAutoCreated
	}
	return SubnetSetTypeNone
}

// switchSubnetSetType check whether the SubnetSet type is switched between
// Pre-created and Auto-created.
// It returns true if the type is switched, otherwise false.
func switchSubnetSetType(old *v1alpha1.SubnetSet, new *v1alpha1.SubnetSet) (bool, string) {
	typeOld := subnetSetType(old)
	typeNew := subnetSetType(new)
	if typeOld != typeNew && typeOld != SubnetSetTypeNone && typeNew != SubnetSetTypeNone {
		return true, "SubnetSet type cannot be switched between Pre-created and Auto-created"
	}
	if old.Spec.SubnetNames != nil && new.Spec.SubnetNames == nil {
		return true, "SubnetName should at least have value like subnetNames:[]"
	}
	return false, ""
}

// Handle handles admission requests.
func (v *SubnetSetValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	subnetSet := &v1alpha1.SubnetSet{}
	var err error
	if req.Operation == admissionv1.Delete {
		err = v.decoder.DecodeRaw(req.OldObject, subnetSet)
	} else {
		err = v.decoder.Decode(req, subnetSet)
	}
	if err != nil {
		log.Error(err, "Failed to decode SubnetSet", "SubnetSet", req.Namespace+"/"+req.Name)
		return admission.Errored(http.StatusBadRequest, err)
	}

	log.Debug("Handling request", "user", req.UserInfo.Username, "operation", req.Operation)
	switch req.Operation {
	case admissionv1.Create:
		valid, msg := util.ValidateSubnetSize(v.nsxClient, subnetSet.Spec.IPv4SubnetSize)
		if !valid {
			return admission.Denied(fmt.Sprintf("SubnetSet %s/%s has invalid size %d: %s", subnetSet.Namespace, subnetSet.Name, subnetSet.Spec.IPv4SubnetSize, msg))
		}
		if isDefaultSubnetSet(subnetSet) && req.UserInfo.Username != NSXOperatorSA {
			return admission.Denied("default SubnetSet only can be created by nsx-operator")
		}
		valid, err = v.validateSubnetNames(ctx, subnetSet.Namespace, subnetSet.Spec.SubnetNames)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		if !valid {
			return admission.Denied(fmt.Sprintf("Subnets under SubnetSet %s/%s should belong to the same VPC", subnetSet.Namespace, subnetSet.Name))
		}
	case admissionv1.Update:
		oldSubnetSet := &v1alpha1.SubnetSet{}
		if err := v.decoder.DecodeRaw(req.OldObject, oldSubnetSet); err != nil {
			log.Error(err, "Failed to decode old SubnetSet", "SubnetSet", req.Namespace+"/"+req.Name)
			return admission.Errored(http.StatusBadRequest, err)
		}
		if (isDefaultSubnetSet(subnetSet) || isDefaultSubnetSet(oldSubnetSet)) && req.UserInfo.Username != NSXOperatorSA {
			return admission.Denied("default SubnetSet only can be updated by nsx-operator")
		}
		if defaultSubnetSetLabelChanged(oldSubnetSet, subnetSet) && req.UserInfo.Username != NSXOperatorSA {
			log.Debug("Default SubnetSet label change detected", "oldLabels", oldSubnetSet.ObjectMeta.Labels, "newLabels", subnetSet.ObjectMeta.Labels, "username", req.UserInfo.Username)
			return admission.Denied(fmt.Sprintf("SubnetSet label %s can only be updated by NSX Operator", common.LabelDefaultNetwork))
		}
		valid, err := v.validateSubnetNames(ctx, subnetSet.Namespace, subnetSet.Spec.SubnetNames)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		if !valid {
			return admission.Denied(fmt.Sprintf("Subnets under SubnetSet %s/%s should belong to the same VPC", subnetSet.Namespace, subnetSet.Name))
		}
		result, msg := switchSubnetSetType(oldSubnetSet, subnetSet)
		if result {
			return admission.Denied(msg)
		}
		// Only check for user defined SubnetSet as Subnets for default network
		// is allowed to be removed from SubnetSet when there is port on it
		if subnetSet.Spec.SubnetNames != nil && oldSubnetSet.Spec.SubnetNames != nil {
			// Lock the SubnetSet to avoid new SubnetPort created on the removed Subnet
			subnetSetLock := controllercommon.WLockSubnetSet(subnetSet.UID)
			defer controllercommon.WUnlockSubnetSet(subnetSet.UID, subnetSetLock)
			removedSubnetNames := nsxutil.DiffArrays(*oldSubnetSet.Spec.SubnetNames, *subnetSet.Spec.SubnetNames)
			valid, err = v.validateRemovedSubnets(ctx, subnetSet, removedSubnetNames)
			if err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			if !valid {
				return admission.Denied(fmt.Sprintf("Subnets %s on SubnetSet %s/%s used by SubnetPorts cannot be removed", removedSubnetNames, subnetSet.Namespace, subnetSet.Name))
			}
		}
	case admissionv1.Delete:
		if isDefaultSubnetSet(subnetSet) && req.UserInfo.Username != NSXOperatorSA {
			return admission.Denied("default SubnetSet only can be deleted by nsx-operator")
		}
		hasSubnetPort, err := v.checkSubnetPort(ctx, subnetSet.Namespace, subnetSet)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		if hasSubnetPort {
			return admission.Denied(fmt.Sprintf("SubnetSet %s/%s with stale SubnetPorts cannot be deleted", subnetSet.Namespace, subnetSet.Name))
		}
	}
	if req.Operation != admissionv1.Delete {
		if hasExclusiveFields(subnetSet) {
			return admission.Denied("SubnetSet spec.subnetNames is exclusive with spec.ipv4SubnetSize, spec.accessMode and spec.subnetDHCPConfig")
		}
		err := controllercommon.CheckAccessModeOrVisibility(v.Client, ctx, subnetSet.Namespace, string(subnetSet.Spec.AccessMode), "subnetset")
		if err != nil {
			if errors.Is(err, controllercommon.ErrFailedToListNetworkInfo) {
				return admission.Errored(http.StatusServiceUnavailable, err)
			}
			log.Error(err, "AccessMode not supported", "AccessMode", subnetSet.Spec.AccessMode, "namespace", subnetSet.Namespace)
			return admission.Denied(err.Error())
		}
	}
	return admission.Allowed("")
}

func getSubentSetKind(subnetSet *v1alpha1.SubnetSet) string {
	var defaultSubnetSetFor string
	if value, ok := subnetSet.Labels[common.LabelDefaultNetwork]; ok {
		defaultSubnetSetFor = value
	} else if value, ok := subnetSet.Labels[common.LabelDefaultSubnetSet]; ok {
		switch value {
		case common.LabelDefaultPodSubnetSet:
			defaultSubnetSetFor = common.DefaultPodNetwork
		case common.LabelDefaultVMSubnetSet:
			defaultSubnetSetFor = common.DefaultVMNetwork
		}
	}
	return defaultSubnetSetFor
}

func (v *SubnetSetValidator) checkSubnetPort(ctx context.Context, ns string, subnetSet *v1alpha1.SubnetSet) (bool, error) {
	crdSubnetPorts := &v1alpha1.SubnetPortList{}
	err := v.Client.List(ctx, crdSubnetPorts, client.InNamespace(ns))
	if err != nil {
		return false, fmt.Errorf("failed to list SubnetPort: %v", err)
	}
	for _, crdSubnetPort := range crdSubnetPorts.Items {
		if crdSubnetPort.Spec.SubnetSet == subnetSet.Name {
			return true, nil
		}
	}
	if isDefaultSubnetSet(subnetSet) {
		defaultSubnetSetFor := getSubentSetKind(subnetSet)
		switch defaultSubnetSetFor {
		case common.DefaultPodNetwork:
			crdPods := &v1.PodList{}
			err := v.Client.List(ctx, crdPods, client.InNamespace(ns))
			if err != nil {
				return false, fmt.Errorf("failed to list Pod: %v", err)
			}
			if len(crdPods.Items) > 0 {
				return true, nil
			}
		case common.DefaultVMNetwork:
			for _, crdSubnetPort := range crdSubnetPorts.Items {
				if crdSubnetPort.Spec.SubnetSet == "" && crdSubnetPort.Spec.Subnet == "" {
					return true, nil
				}
			}
		default:
			// Should not reach here
			log.Error(nil, "Unrecognized default SubnetSet label", "value", defaultSubnetSetFor)
		}
	}
	return false, nil
}

func (v *SubnetSetValidator) getSubnetPortsID(ctx context.Context, subnetSet *v1alpha1.SubnetSet) ([]types.UID, error) {
	crdSubnetPorts := &v1alpha1.SubnetPortList{}
	crdSubnetPortsIDs := make([]types.UID, 0)
	err := v.Client.List(ctx, crdSubnetPorts, client.InNamespace(subnetSet.Namespace))
	if err != nil {
		return crdSubnetPortsIDs, fmt.Errorf("failed to list SubnetPort: %v", err)
	}
	for _, crdSubnetPort := range crdSubnetPorts.Items {
		if crdSubnetPort.Spec.SubnetSet == subnetSet.Name {
			crdSubnetPortsIDs = append(crdSubnetPortsIDs, crdSubnetPort.UID)
		}
	}
	if isDefaultSubnetSet(subnetSet) {
		defaultSubnetSetFor := getSubentSetKind(subnetSet)
		switch defaultSubnetSetFor {
		// Check Pods for pod-default SubnetSet
		case common.DefaultPodNetwork:
			crdPods := &v1.PodList{}
			err := v.Client.List(ctx, crdPods, client.InNamespace(subnetSet.Namespace))
			if err != nil {
				return crdSubnetPortsIDs, fmt.Errorf("failed to list Pod: %v", err)
			}
			for _, crdPod := range crdPods.Items {
				crdSubnetPortsIDs = append(crdSubnetPortsIDs, crdPod.UID)
			}
		// Check SubnetPort without Subnet/SubnetSet for vm-default SubnetSet
		case common.DefaultVMNetwork:
			for _, crdSubnetPort := range crdSubnetPorts.Items {
				if crdSubnetPort.Spec.SubnetSet == "" && crdSubnetPort.Spec.Subnet == "" {
					crdSubnetPortsIDs = append(crdSubnetPortsIDs, crdSubnetPort.UID)
				}
			}
		default:
			// Should not reach here
			log.Error(nil, "Unrecognized default SubnetSet label", "value", defaultSubnetSetFor)
		}
	}
	return crdSubnetPortsIDs, nil
}

// Verify that all the SubnetPorts on the SubnetSet does not use the removed Subnets
func (v *SubnetSetValidator) validateRemovedSubnets(ctx context.Context, subnetSet *v1alpha1.SubnetSet, subnetNames []string) (bool, error) {
	log.Debug("Verify if Subnets can be removed from SubnetSet", "Namespace", subnetSet.Namespace, "subnetSetName", subnetSet.Name, "subnetNames", subnetNames)
	usedSubnetPaths := sets.New[string]()
	ids, err := v.getSubnetPortsID(ctx, subnetSet)
	if err != nil {
		return false, fmt.Errorf("failed to get SubnetPort IDs: %v", err)
	}
	for _, id := range ids {
		subnetPath := v.subnetPortService.GetSubnetPathForSubnetPortFromStore(id)
		if subnetPath != "" {
			usedSubnetPaths.Insert(subnetPath)
		}
	}
	for _, subnetName := range subnetNames {
		crdSubnet := &v1alpha1.Subnet{}
		err := v.Client.Get(ctx, types.NamespacedName{Name: subnetName, Namespace: subnetSet.Namespace}, crdSubnet)
		if err != nil {
			return false, fmt.Errorf("failed to get Subnet %s/%s: %v", subnetSet.Namespace, subnetName, err)
		}
		nsxSubnet, err := v.subnetService.GetSubnetByCR(crdSubnet)
		if err != nil {
			return false, fmt.Errorf("failed to get NSX Subnet %s/%s: %v", subnetSet.Namespace, subnetName, err)
		}
		if usedSubnetPaths.Has(*nsxSubnet.Path) {
			return false, nil
		}
	}
	return true, nil
}

func (v *SubnetSetValidator) getVPCPath(ns string) (string, error) {
	vpcInfoList := v.vpcService.ListVPCInfo(ns)
	if len(vpcInfoList) == 0 {
		return "", fmt.Errorf("failed to get VPC Info %s", ns)
	}
	return fmt.Sprintf("/orgs/%s/projects/%s/vpcs/%s", vpcInfoList[0].OrgID, vpcInfoList[0].ProjectID, vpcInfoList[0].VPCID), nil
}

// Check all Subnet CRs are created and associated NSX Subnet belongs to the same VPC.
func (v *SubnetSetValidator) validateSubnetNames(ctx context.Context, ns string, subnetNames *[]string) (bool, error) {
	var namespaceVpc string
	var existingVPC string
	if subnetNames == nil {
		return true, nil
	}
	for _, subnetName := range *subnetNames {
		crdSubnet := &v1alpha1.Subnet{}
		err := v.Client.Get(ctx, types.NamespacedName{Name: subnetName, Namespace: ns}, crdSubnet)
		if err != nil {
			return false, fmt.Errorf("failed to get Subnet %s/%s: %v", ns, subnetName, err)
		}
		if crdSubnet.Spec.SubnetDHCPConfig.Mode == v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeRelay) {
			return false, fmt.Errorf("DHCPRelay Subnet %s/%s is not supported in SubnetSet", crdSubnet.Namespace, crdSubnet.Name)
		}
		var subnetVPC string
		associatedResource, exists := crdSubnet.Annotations[common.AnnotationAssociatedResource]
		if exists {
			subnetPath, err := common.GetSubnetPathFromAssociatedResource(associatedResource)
			if err != nil {
				return false, err
			}
			subnetVPC = strings.Split(subnetPath, "/subnets/")[0]
		} else {
			if namespaceVpc == "" {
				namespaceVpc, err = v.getVPCPath(ns)
				if err != nil {
					return false, err
				}
			}
			subnetVPC = namespaceVpc
		}
		if existingVPC == "" {
			existingVPC = subnetVPC
		}
		if existingVPC != subnetVPC {
			log.Warn("Subnets under SubnetSet is from different VPCs", "vpc1", existingVPC, "vpc2", subnetVPC)
			return false, nil
		}
	}
	return true, nil
}
