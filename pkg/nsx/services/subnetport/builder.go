package subnetport

import (
	"bytes"
	"context"
	"fmt"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/crd.nsx.vmware.com/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	String = common.String
)

func (service *SubnetPortService) buildSubnetPort(obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, labelTags *map[string]string) (*model.VpcSubnetPort, error) {
	var objNamespace, appId, allocateAddresses string
	objMeta := getObjectMeta(obj)
	if objMeta == nil {
		return nil, fmt.Errorf("unsupported object: %v", obj)
	}
	objNamespace = objMeta.Namespace
	if _, ok := obj.(*corev1.Pod); ok {
		appId = string(objMeta.UID)
	}
	if nsxSubnet.DhcpConfig != nil && nsxSubnet.DhcpConfig.EnableDhcp != nil && *nsxSubnet.DhcpConfig.EnableDhcp {
		allocateAddresses = "DHCP"
	} else {
		allocateAddresses = "BOTH"
	}
	nsxSubnetPortName := service.BuildSubnetPortName(objMeta)
	nsxSubnetPortID := service.BuildSubnetPortId(objMeta)
	// use the subnetPort CR UID as the attachment uid generation to ensure the latter stable
	nsxCIFID, err := uuid.NewRandomFromReader(bytes.NewReader([]byte(nsxSubnetPortID)))
	if err != nil {
		return nil, err
	}
	nsxSubnetPortPath := fmt.Sprintf("%s/ports/%s", *nsxSubnet.Path, nsxSubnetPortID)
	if err != nil {
		return nil, err
	}
	namespace := &corev1.Namespace{}
	namespacedName := types.NamespacedName{
		Name: objNamespace,
	}
	if err := service.Client.Get(context.Background(), namespacedName, namespace); err != nil {
		return nil, err
	}
	namespace_uid := namespace.UID
	tags := util.BuildBasicTags(getCluster(service), obj, namespace_uid)
	if labelTags != nil {
		for k, v := range *labelTags {
			tags = append(tags, model.Tag{Scope: String(k), Tag: String(v)})
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
		Tags:       tags,
		Path:       &nsxSubnetPortPath,
		ParentPath: nsxSubnet.Path,
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
