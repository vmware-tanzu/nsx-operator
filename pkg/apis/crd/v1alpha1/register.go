package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// GroupName is the group name for this API.
	GroupName = "nsx.vmware.com"
	// Version is the API version.
	Version = "v1alpha1"
)

// SchemeGroupVersion is group version used to register these objects.
var SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: Version}

// Resource takes an unqualified resource and returns a Group qualified GroupResource.
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}

var (
	// SchemeBuilder points to a list of functions added to Scheme.
	SchemeBuilder      runtime.SchemeBuilder
	localSchemeBuilder = &SchemeBuilder
	// AddToScheme applies all the stored functions to the scheme.
	AddToScheme = localSchemeBuilder.AddToScheme
)

func init() {
	// We only register manually written functions here. The registration of the
	// generated functions takes place in the generated files. The separation
	// makes the code compile even when the generated files are missing.
	localSchemeBuilder.Register(addKnownTypes)
}

// Adds the list of known types to api.Scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(
		SchemeGroupVersion,
		&ClusterNetworkInfo{},
		&ClusterNetworkInfoList{},
		&IPPool{},
		&IPPoolList{},
		&NamespaceNetworkInfo{},
		&NamespaceNetworkInfoList{},
		&NSXNetworkConfiguration{},
		&NSXNetworkConfigurationList{},
		&RouteSet{},
		&RouteSetList{},
		&VPC{},
		&VPCList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}
