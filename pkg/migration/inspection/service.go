/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"fmt"

	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vapi/rest"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/fabric"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

// ServiceConfig supplies authenticated clients and NSX project scope. The caller
// owns both vCenter sessions and the NSX client's lifecycle.
type ServiceConfig struct {
	KubernetesReader client.Reader
	KubernetesClient client.Client
	VCenter          *vim25.Client
	VCenterREST      *rest.Client
	NSX              *nsx.Client
	ComputeManagerID string
	SupervisorID     string
	OrgID            string
	ProjectID        string
}

// NewService wires source discovery, zone resolution and NSX target discovery.
// Net Operator types must be registered in the Kubernetes reader's scheme.
func NewService(config ServiceConfig) (Service, error) {
	if config.KubernetesReader == nil || config.KubernetesClient == nil || config.VCenter == nil || config.VCenterREST == nil || config.NSX == nil || config.NSX.RestConnector == nil || config.ComputeManagerID == "" || config.SupervisorID == "" || config.OrgID == "" || config.ProjectID == "" {
		return Service{}, fmt.Errorf("Kubernetes, vCenter and NSX clients and project identities are required")
	}
	vcUUID := config.VCenter.ServiceContent.About.InstanceUuid
	if vcUUID == "" {
		return Service{}, fmt.Errorf("vCenter instance UUID is required")
	}
	zoneResolver := VCenterZoneResolver{VCenter: config.VCenterREST, NSXZones: fabric.NewZonesClient(config.NSX.RestConnector), ComputeManagerID: config.ComputeManagerID, SupervisorID: config.SupervisorID}
	topology := VCenterTopology{VCenterUUID: vcUUID, Properties: property.DefaultCollector(config.VCenter), Zones: zoneResolver}
	return Service{
		Inventory:           KubernetesInventory{Reader: config.KubernetesReader, Topology: topology},
		TargetInventory:     NSXTargetInventory{Client: config.NSX, OrgID: config.OrgID, ProjectID: config.ProjectID, VCenterUUID: vcUUID, Topology: topology},
		FLBNetworkInventory: VCenterFLBNetworkReader{VCenter: config.VCenterREST, SupervisorID: config.SupervisorID, VCenterUUID: vcUUID},
		StatusClient:        config.KubernetesClient,
	}, nil
}
