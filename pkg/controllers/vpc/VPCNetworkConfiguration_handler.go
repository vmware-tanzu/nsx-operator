package vpc

import (
	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	vpcservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// VPCNetworkConfigurationHandler handles VPC NetworkConfiguration event:
// - VPC Network Configuration creation: Add VPC Network Configuration into cache.
// - VPC Network Configuration deletion: Delete VPC Network Configuration from cache.

type VPCNetworkConfigurationHandler struct {
	Client     client.Client
	vpcService *vpc.VPCService
}

func (h *VPCNetworkConfigurationHandler) Create(e event.CreateEvent, _ workqueue.RateLimitingInterface) {
	vpcConfigCR := e.Object.(*v1alpha1.VPCNetworkConfiguration)
	vname := vpcConfigCR.GetName()
	ninfo := &vpc.VPCNetworkConfigInfo{
		Name:                    vname,
		DefaultGatewayPath:      vpcConfigCR.Spec.DefaultGatewayPath,
		EdgeClusterPath:         vpcConfigCR.Spec.EdgeClusterPath,
		NsxtProject:             vpcConfigCR.Spec.NSXTProject,
		ExternalIPv4Blocks:      vpcConfigCR.Spec.ExternalIPv4Blocks,
		PrivateIPv4CIDRs:        vpcConfigCR.Spec.PrivateIPv4CIDRs,
		DefaultIPv4SubnetSize:   vpcConfigCR.Spec.DefaultIPv4SubnetSize,
		DefaultSubnetAccessMode: vpcConfigCR.Spec.DefaultSubnetAccessMode,
	}
	h.vpcService.RegisterVPCNetworkConfig(vname, *ninfo)
	log.Info("create vpc network config object and insert to storage", "vpc", ninfo)
}

func (h *VPCNetworkConfigurationHandler) Delete(e event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	// Currently we do not support deleting networkconfig
	log.V(1).Info("do not support vpc network config deletion")
}

func (h *VPCNetworkConfigurationHandler) Generic(_ event.GenericEvent, _ workqueue.RateLimitingInterface) {
	log.V(1).Info("VPCNetworkConfiguration generic event, do nothing")
}

func (h *VPCNetworkConfigurationHandler) Update(e event.UpdateEvent, q workqueue.RateLimitingInterface) {
	log.V(1).Info("start processing vpc network config update event")
	oldNc := e.ObjectOld.(*v1alpha1.VPCNetworkConfiguration)
	newNc := e.ObjectNew.(*v1alpha1.VPCNetworkConfiguration)
	if len(oldNc.Spec.ExternalIPv4Blocks) == len(newNc.Spec.ExternalIPv4Blocks) &&
		len(oldNc.Spec.PrivateIPv4CIDRs) == len(newNc.Spec.PrivateIPv4CIDRs) {
		log.V(1).Info("only support updating external/private ipv4 cidr, no change")
		return
	}

	//TODO: known defect, this handling need to be moving to vpc reconciler by trigger an vpc cr event.
	//if the appended cidr is private, no ip block created.
	vpc := h.vpcService.GetVPCsByNamespace(newNc.GetNamespace())[0]
	vpc.ExternalIpv4Blocks = vpcservice.TransferIpblockIDstoPaths(newNc.Spec.ExternalIPv4Blocks)
	vpc.PrivateIpv4Blocks = newNc.Spec.PrivateIPv4CIDRs
	path, err := common.ParseVPCResourcePath(*vpc.Path)
	if err != nil {
		log.Error(err, "failed to parse nsx path", "PATH", vpc.Path)
	}
	err = h.vpcService.NSXClient.VPCClient.Patch(path.OrgID, path.ProjectID, path.ID, vpc)
	if err != nil {
		log.Error(err, "Failed to update vpc model in nsx", "vpc")
	}
	h.vpcService.VpcStore.Update(vpc)
}

var VPCNetworkConfigurationPredicate = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return true
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		return true
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return false
	},
	GenericFunc: func(genericEvent event.GenericEvent) bool {
		return false
	},
}
