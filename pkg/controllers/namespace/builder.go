package namespace

import (
	"github.com/google/uuid"
	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
)

func BuildVPCCR(ns string, ncName string, vpcName *string) *v1alpha1.VPC {
	log.V(2).Info("building vpc", "ns", ns, "nc", ncName, "VPC", vpcName)
	vpc := &v1alpha1.VPC{}
	if vpcName == nil {
		vpc.Name = "vpc-" + uuid.New().String()
	} else {
		vpc.Name = *vpcName
	}

	vpc.Namespace = ns
	return vpc
}
