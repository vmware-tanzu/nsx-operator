package mediator

import (
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

var (
	log = logger.Log
)

// ServiceMediator We use mediator pattern to wrap all the services,
// embed all the services in ServiceMediator, so that we can mediate all the methods of all the services
// transparently to the caller, for example, in other packages, we can use ServiceMediator.GetVPCsByNamespace directly.
// In startCRDController function, we register the CRDService to the ServiceMediator, since only one controller writes to
// its own store and other controllers read from the store, so we don't need lock here.
type ServiceMediator struct {
	*securitypolicy.SecurityPolicyService
	*vpc.VPCService
	*subnet.SubnetService
}

// ListVPCInfo is a common method, extracting the org, the project, and the vpc string from vpc path of the VPC model.
// VPC path looks like "/orgs/default/projects/project-1/vpcs/vpc-1",
// Since other modules only know namespace, this is the only entry point to get org and project.
// Currently, we only support one vpc per namespace, but we may support multiple vpcs per namespace in the future,
// so we return a slice of VPCInfo.
func (serviceMediator *ServiceMediator) ListVPCInfo(ns string) []common.VPCResourceInfo {
	var VPCInfoList []common.VPCResourceInfo
	vpcs := serviceMediator.GetVPCsByNamespace(ns) // Transparently call the VPCService.GetVPCsByNamespace method
	for _, v := range vpcs {
		vpcResourceInfo, err := common.ParseVPCResourcePath(*v.Path)
		if err != nil {
			log.Error(err, "Failed to get vpc info from vpc path", "vpc path", *v.Path)
		}
		VPCInfoList = append(VPCInfoList, vpcResourceInfo)
	}
	return VPCInfoList
}
