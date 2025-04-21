package subnetport

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	controllercommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	String = common.String
)

func (service *SubnetPortService) buildSubnetPort(obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, labelTags *map[string]string, isVmSubnetPort bool) (*model.VpcSubnetPort, error) {
	var objNamespace, appId, allocateAddresses string
	objMeta := getObjectMeta(obj)
	if objMeta == nil {
		return nil, fmt.Errorf("unsupported object: %v", obj)
	}
	objNamespace = objMeta.Namespace
	if _, ok := obj.(*corev1.Pod); ok {
		appId = string(objMeta.UID)
	}
	var externalAddressBinding *model.ExternalAddressBinding
	switch o := obj.(type) {
	case *v1alpha1.SubnetPort:
		externalAddressBinding = service.buildExternalAddressBinding(o)
	}

	if nsxSubnet.SubnetDhcpConfig != nil && nsxSubnet.SubnetDhcpConfig.Mode != nil && *nsxSubnet.SubnetDhcpConfig.Mode != nsxutil.ParseDHCPMode(v1alpha1.DHCPConfigModeDeactivated) {
		allocateAddresses = "DHCP"
	} else {
		allocateAddresses = "BOTH"
	}

	nsxSubnetPortName := service.BuildSubnetPortName(objMeta)
	nsxSubnetPortID := service.BuildSubnetPortId(objMeta)
	// use the subnetPort CR UID as the attachment uid generation to ensure the latter stable
	nsxCIFID, err := uuid.NewRandomFromReader(bytes.NewReader([]byte(string(objMeta.UID))))
	if err != nil {
		return nil, err
	}
	nsxSubnetPortPath := fmt.Sprintf("%s/ports/%s", *nsxSubnet.Path, nsxSubnetPortID)
	namespace := &corev1.Namespace{}
	namespacedName := types.NamespacedName{
		Name: objNamespace,
	}
	if err := service.Client.Get(context.Background(), namespacedName, namespace); err != nil {
		return nil, err
	}
	namespace_uid := namespace.UID
	tags := util.BuildBasicTags(getCluster(service), obj, namespace_uid)

	// Filter tags based on the type of subnet port (VM or Pod).
	// For VM subnet ports, we need to filter out tags with scope VMNamespaceUID and VMNamespace.
	// For Pod subnet ports, we need to filter out tags with scope NamespaceUID and Namespace.
	var tagsFiltered []model.Tag
	for _, tag := range tags {
		if isVmSubnetPort && *tag.Scope == common.TagScopeNamespaceUID {
			continue
		}
		if isVmSubnetPort && *tag.Scope == common.TagScopeNamespace {
			continue
		}
		if !isVmSubnetPort && *tag.Scope == common.TagScopeVMNamespaceUID {
			continue
		}
		if !isVmSubnetPort && *tag.Scope == common.TagScopeVMNamespace {
			continue
		}
		tagsFiltered = append(tagsFiltered, tag)
	}

	if labelTags != nil {
		// Append Namespace labels in order as tags
		labelKeys := make([]string, 0, len(*labelTags))
		for k := range *labelTags {
			labelKeys = append(labelKeys, k)
		}
		sort.Strings(labelKeys)
		for _, k := range labelKeys {
			tagsFiltered = append(tagsFiltered, model.Tag{Scope: common.String(k), Tag: common.String((*labelTags)[k])})
		}
	}
	nsxSubnetPort := &model.VpcSubnetPort{
		DisplayName: String(nsxSubnetPortName),
		Id:          String(nsxSubnetPortID),
		Attachment: &model.PortAttachment{
			AllocateAddresses: &allocateAddresses,
			Id:                String(nsxCIFID.String()),
			TrafficTag:        common.Int64(0),
			Type_:             String("STATIC"),
		},
		Tags:                   tagsFiltered,
		Path:                   &nsxSubnetPortPath,
		ParentPath:             nsxSubnet.Path,
		ExternalAddressBinding: externalAddressBinding,
	}
	if appId != "" {
		nsxSubnetPort.Attachment.AppId = &appId
		nsxSubnetPort.Attachment.ContextId = &contextID
	}
	return nsxSubnetPort, nil
}

func (service *SubnetPortService) BuildSubnetPortId(obj *metav1.ObjectMeta) string {
	return util.GenerateIDByObject(obj)
}

func (service *SubnetPortService) BuildSubnetPortName(obj *metav1.ObjectMeta) string {
	return util.GenerateTruncName(common.MaxNameLength, obj.Name, "", "", "", "")
}

func getObjectMeta(obj interface{}) *metav1.ObjectMeta {
	switch o := obj.(type) {
	case *v1alpha1.SubnetPort:
		return &o.ObjectMeta
	case *corev1.Pod:
		return &o.ObjectMeta
	}
	return nil
}

func getCluster(service *SubnetPortService) string {
	return service.NSXConfig.Cluster
}

func buildSubnetPortExternalAddressBindingFromExisting(subnetPort *model.VpcSubnetPort, existingSubnetPort *model.VpcSubnetPort) *model.VpcSubnetPort {
	if existingSubnetPort == nil {
		return subnetPort
	}
	if existingSubnetPort.ExternalAddressBinding != nil {
		if subnetPort.ExternalAddressBinding != nil {
			// update is not supported, keep existing ExternalAddressBinding
			subnetPort.ExternalAddressBinding = &model.ExternalAddressBinding{}
		}
	}
	return subnetPort
}

func (service *SubnetPortService) buildExternalAddressBinding(sp *v1alpha1.SubnetPort) *model.ExternalAddressBinding {
	if service.GetAddressBindingBySubnetPort(sp) != nil {
		return &model.ExternalAddressBinding{}
	}
	return nil
}

func (service *SubnetPortService) GetAddressBindingBySubnetPort(sp *v1alpha1.SubnetPort) *v1alpha1.AddressBinding {
	vm, port, err := controllercommon.GetVirtualMachineNameForSubnetPort(sp)
	if err != nil {
		log.Error(err, "Failed to get VM name from SubnetPort", "namespace", sp.Namespace, "name", sp.Name, "annotations", sp.Annotations)
		return nil
	} else if vm == "" {
		log.Info("Failed to get VM name from SubnetPort", "namespace", sp.Namespace, "name", sp.Name, "annotations", sp.Annotations)
		return nil
	}
	abList := &v1alpha1.AddressBindingList{}
	abIndexValue := fmt.Sprintf("%s/%s", sp.Namespace, vm)
	err = service.Client.List(context.TODO(), abList, client.MatchingFields{util.AddressBindingNamespaceVMIndexKey: abIndexValue})
	if err != nil {
		log.Error(err, "Failed to list AddressBinding from cache", "indexValue", abIndexValue)
		return nil
	}
	// sort by CreationTimestamp
	slices.SortFunc(abList.Items, func(a, b v1alpha1.AddressBinding) int {
		return a.CreationTimestamp.UTC().Compare(b.CreationTimestamp.UTC())
	})
	for _, ab := range abList.Items {
		if ab.Spec.InterfaceName == "" {
			spList := &v1alpha1.SubnetPortList{}
			spIndexValue := fmt.Sprintf("%s/%s", ab.Namespace, ab.Spec.VMName)
			err = service.Client.List(context.TODO(), spList, client.MatchingFields{util.SubnetPortNamespaceVMIndexKey: spIndexValue})
			if err != nil || len(spList.Items) == 0 {
				log.Error(err, "Failed to list SubnetPort from cache", "indexValue", spIndexValue)
				return nil
			}
			if len(spList.Items) == 1 {
				log.Info("Found default AddressBinding for SubnetPort", "namespace", sp.Namespace, "name", sp.Name, "defaultAddressBindingName", ab.Name, "VM", vm)
				return &ab
			}
			log.Info("Found multiple SubnetPorts for a VM, ignore default AddressBinding for SubnetPort", "namespace", sp.Namespace, "name", sp.Name, "defaultAddressBindingName", ab.Name, "VM", vm)
		} else if ab.Spec.InterfaceName == port {
			log.V(1).Info("Found AddressBinding for SubnetPort", "namespace", sp.Namespace, "name", sp.Name, "addressBindingName", ab.Name)
			return &ab
		}
	}
	return nil
}
