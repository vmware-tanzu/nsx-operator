package mediator

import (
	"regexp"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

var (
	log                    = logger.Log
	reExp = regexp.MustCompile(`/orgs/([^/]+)/projects/([^/]+)/vpcs/([^/]+)`)
)

// ServiceMediator We use mediator pattern to wrap all the services,
// embed all the services in ServiceMediator, so that we can mediate all the methods of all the services
// transparently to the caller, for example, in other packages, we can use ServiceMediator.GetVPCsByNamespace directly.
// In startCRDController function, we register the CRDService to the ServiceMediator, since only one controller writes to
// its own store and other controllers read from the store, so we don't need lock here.
type ServiceMediator struct {
	*securitypolicy.SecurityPolicyService
	*vpc.VPCService
}

// GetOrgProjectVPC is a common method, extracting the org and project string from vpc path of the VPC model.
// VPC path looks like "/orgs/default/projects/project-1/vpcs/vpc-1",
// Since other modules only know namespace, this is the only entry point to get org and project.
// Currently, we only support one vpc per namespace, but we may support multiple vpcs per namespace in the future,
// so we return a slice of OrgProject.
func (serviceMediator *ServiceMediator) GetOrgProjectVPC(ns string) []common.OrgProjectVPC {
	var orgProjectVPC []common.OrgProjectVPC
	vpcs := serviceMediator.GetVPCsByNamespace(ns) // Transparently call the VPCService.GetVPCsByNamespace method
	for _, v := range vpcs {
		matches := reExp.FindStringSubmatch(*v.Path)
		if len(matches) == 4 {
			org := matches[1]
			project := matches[2]
			vpc_ := matches[3]
			orgProjectVPC = append(orgProjectVPC, common.OrgProjectVPC{OrgID: org, ProjectID: project, VPCID: vpc_})
		} else {
			log.Error(nil, "Failed to get org and project from vpc path", "vpc path", *v.Path)
		}
	}
	return orgProjectVPC
}
