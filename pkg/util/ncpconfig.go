package util

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// NewNSXRestoreStatus returns the cluster-scoped NCPConfig shared by NCP and the operator.
func NewNSXRestoreStatus() *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "nsx.vmware.com", Version: "v1", Kind: "NCPConfig"})
	obj.SetName(NSXRestoreStatus)
	return obj
}
