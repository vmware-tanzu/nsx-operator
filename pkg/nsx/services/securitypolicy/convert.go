package securitypolicy

import (
	"unsafe"

	crdv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/crd.nsx.vmware.com/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/apis/nsx.vmware.com/v1alpha1"
)

func T1ToVPC(in *v1alpha1.SecurityPolicy) *crdv1alpha1.SecurityPolicy {
	out := (*crdv1alpha1.SecurityPolicy)(unsafe.Pointer(in))
	out.APIVersion = "crd.nsx.vmware.com/v1alpha1"
	return out
}

func VPCToT1(in *crdv1alpha1.SecurityPolicy) *v1alpha1.SecurityPolicy {
	out := (*v1alpha1.SecurityPolicy)(unsafe.Pointer(in))
	out.APIVersion = "nsx.vmware.com/v1alpha1"
	return out
}
