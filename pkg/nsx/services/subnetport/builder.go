package subnetport

import (
	"bytes"
	"context"
	"fmt"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	String = common.String
)

func (service *SubnetPortService) buildSubnetPort(obj *v1alpha1.SubnetPort, nsxSubnetPath string) (*model.SegmentPort, error) {
	allocateAddresses := "BOTH"
	nsxSubnetPortName := fmt.Sprintf("port-%s", obj.Name)
	nsxSubnetPortID := string(obj.UID)
	// use the subnetPort CR UID as the attachment uid generation to ensure the latter stable
	nsxCIFID, err := uuid.NewRandomFromReader(bytes.NewReader([]byte(nsxSubnetPortID)))
	if err != nil {
		return nil, err
	}
	nsxSubnetPortPath := fmt.Sprintf("%s/ports/%s", nsxSubnetPath, obj.UID)
	if err != nil {
		return nil, err
	}
	namespace := &corev1.Namespace{}
	namespacedName := types.NamespacedName{
		Name: obj.Namespace,
	}
	if err := service.Client.Get(context.Background(), namespacedName, namespace); err != nil {
		return nil, err
	}
	namespace_uid := namespace.UID
	// TODO: set AppId and ContextId for pod port.
	nsxSubnetPort := &model.SegmentPort{
		DisplayName: common.String(nsxSubnetPortName),
		Id:          common.String(nsxSubnetPortID),
		Attachment: &model.PortAttachment{
			AllocateAddresses: &allocateAddresses,
			// AppId:             common.String("nsx.ns-3.pod-5%"),
			// ContextId:         common.String("95ccaad4-0dfb-469e-a1e6-27d815826382"),
			Id:         common.String(nsxCIFID.String()),
			TrafficTag: nil,
			Type_:      common.String("STATIC"),
		},
		Tags: service.buildBasicTags(obj, namespace_uid),
		Path: &nsxSubnetPortPath,
	}
	return nsxSubnetPort, nil
}

func getCluster(service *SubnetPortService) string {
	return service.NSXConfig.Cluster
}

func (service *SubnetPortService) buildBasicTags(obj *v1alpha1.SubnetPort, namespaceUID types.UID) []model.Tag {
	tags := []model.Tag{
		{
			Scope: String(common.TagScopeCluster),
			Tag:   String(getCluster(service)),
		},
		{
			Scope: String(common.TagScopeNamespace),
			Tag:   String(obj.ObjectMeta.Namespace),
		},
		{
			Scope: String(common.TagScopeNamespaceUID),
			Tag:   String(string(namespaceUID)),
		},
		{
			Scope: String(common.TagScopeSubnetPortCRName),
			Tag:   String(obj.ObjectMeta.Name),
		},
		{
			Scope: String(common.TagScopeSubnetPortCRUID),
			Tag:   String(string(obj.UID)),
		},
	}
	return tags
}
