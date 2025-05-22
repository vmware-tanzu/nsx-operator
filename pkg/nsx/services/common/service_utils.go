package common

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (service *Service) GetNamespaceUID(ns string) (nsUid types.UID) {
	namespace := &corev1.Namespace{}
	namespacedName := types.NamespacedName{
		Name: ns,
	}
	if err := service.Client.Get(context.Background(), namespacedName, namespace); err != nil {
		log.Error(err, "Failed to get namespace UID", "namespace", ns)
		return ""
	}
	namespace_uid := namespace.UID
	return namespace_uid
}

func GetNamespaceUUID(tags []model.Tag) string {
	for _, tag := range tags {
		if *tag.Scope == TagScopeNamespaceUID {
			return *tag.Tag
		}
	}
	return ""
}
