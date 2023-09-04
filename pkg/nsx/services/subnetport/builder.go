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

func (service *SubnetPortService) buildSubnetPort(obj interface{}, nsxSubnetPath string, contextID string, labelTags *map[string]string) (*model.SegmentPort, error) {
	var objName, objNamespace, uid, appId string
	switch o := obj.(type) {
	case *v1alpha1.SubnetPort:
		objName = o.Name
		objNamespace = o.Namespace
		uid = string(o.UID)
	case *corev1.Pod:
		objName = o.Name
		objNamespace = o.Namespace
		uid = string(o.UID)
		appId = string(o.UID)
	}
	allocateAddresses := "BOTH"
	nsxSubnetPortName := fmt.Sprintf("port-%s", objName)
	nsxSubnetPortID := string(uid)
	// use the subnetPort CR UID as the attachment uid generation to ensure the latter stable
	nsxCIFID, err := uuid.NewRandomFromReader(bytes.NewReader([]byte(nsxSubnetPortID)))
	if err != nil {
		return nil, err
	}
	nsxSubnetPortPath := fmt.Sprintf("%s/ports/%s", nsxSubnetPath, uid)
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
	tags := service.buildBasicTags(obj, namespace_uid)
	if labelTags != nil {
		for k, v := range *labelTags {
			tags = append(tags, model.Tag{Scope: common.String(k), Tag: common.String(v)})
		}
	}
	nsxSubnetPort := &model.SegmentPort{
		DisplayName: common.String(nsxSubnetPortName),
		Id:          common.String(nsxSubnetPortID),
		Attachment: &model.PortAttachment{
			AllocateAddresses: &allocateAddresses,
			Id:                common.String(nsxCIFID.String()),
			TrafficTag:        common.Int64(0),
			Type_:             common.String("STATIC"),
		},
		Tags:       tags,
		Path:       &nsxSubnetPortPath,
		ParentPath: &nsxSubnetPath,
	}
	if appId != "" {
		nsxSubnetPort.Attachment.AppId = &appId
		nsxSubnetPort.Attachment.ContextId = &contextID
	}
	return nsxSubnetPort, nil
}

func getCluster(service *SubnetPortService) string {
	return service.NSXConfig.Cluster
}

func (service *SubnetPortService) buildBasicTags(obj interface{}, namespaceUID types.UID) []model.Tag {
	var tags []model.Tag
	switch o := obj.(type) {
	case *v1alpha1.SubnetPort:
		tags = []model.Tag{
			{
				Scope: String(common.TagScopeCluster),
				Tag:   String(getCluster(service)),
			},
			{
				Scope: String(common.TagScopeVMNamespace),
				Tag:   String(o.ObjectMeta.Namespace),
			},
			{
				Scope: String(common.TagScopeVMNamespaceUID),
				Tag:   String(string(namespaceUID)),
			},
			{
				Scope: String(common.TagScopeSubnetPortCRName),
				Tag:   String(o.ObjectMeta.Name),
			},
			{
				Scope: String(common.TagScopeSubnetPortCRUID),
				Tag:   String(string(o.UID)),
			},
		}
	case *corev1.Pod:
		tags = []model.Tag{
			{
				Scope: String(common.TagScopeCluster),
				Tag:   String(getCluster(service)),
			},
			{
				Scope: String(common.TagScopeNamespace),
				Tag:   String(o.ObjectMeta.Namespace),
			},
			{
				Scope: String(common.TagScopeNamespaceUID),
				Tag:   String(string(namespaceUID)),
			},
			{
				Scope: String(common.TagScopePodName),
				Tag:   String(o.ObjectMeta.Name),
			},
			{
				Scope: String(common.TagScopePodUID),
				Tag:   String(string(o.UID)),
			},
		}
	}
	return tags
}
