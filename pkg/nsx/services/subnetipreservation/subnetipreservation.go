package subnetipreservation

import (
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	log                             = logger.Log
	MarkedForDelete                 = true
	ResourceTypeSubnetIPReservation = "DynamicIpAddressReservation"
	ipReservationCRUIDIndexKey      = "ipReservationCRUID"
	ipReservationCRNameIndexKey     = "ipReservationCRName"
)

type IPReservationService struct {
	common.Service
	IPReservationStore *IPReservationStore
	builder            *common.PolicyTreeBuilder[*model.DynamicIpAddressReservation]
}

// InitializeService initializes SubnetIPReservationService service.
func InitializeService(service common.Service) (*IPReservationService, error) {
	builder, _ := common.PolicyPathVpcSubnetDynamicIPReservation.NewPolicyTreeBuilder()
	wg := sync.WaitGroup{}
	fatalErrors := make(chan error, 1)
	defer close(fatalErrors)

	ipReservationService := &IPReservationService{
		Service:            service,
		IPReservationStore: SetupStore(),
		builder:            builder,
	}

	wg.Add(1)
	go ipReservationService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeSubnetIPReservation, nil, ipReservationService.IPReservationStore)
	wg.Wait()

	if len(fatalErrors) > 0 {
		err := <-fatalErrors
		return ipReservationService, err
	}

	return ipReservationService, nil
}

func (s *IPReservationService) GetOrCreateSubnetIPReservation(ipReservation *v1alpha1.SubnetIPReservation, subnetPath string) (*model.DynamicIpAddressReservation, error) {
	log.Info("Getting or creating Subnet IPReservation", "SubnetIPReservation", ipReservation.UID, "nsxSubnetPath", subnetPath)
	nsxIPReservation := s.buildIPReservation(ipReservation, subnetPath)

	existingIPReservations := s.IPReservationStore.GetByIndex(ipReservationCRUIDIndexKey, string(ipReservation.UID))
	if len(existingIPReservations) > 0 {
		// NSX Subnet IPReservation cannot be updated once created
		log.Info("NSX Subnet IPReservation is created, skipping", "SubnetIPReservation", existingIPReservations[0].Path)
		return existingIPReservations[0], nil
	}
	subnetInfo, err := common.ParseVPCResourcePath(subnetPath)
	if err != nil {
		return nil, err
	}
	err = s.NSXClient.DynamicIPReservationsClient.Patch(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, *nsxIPReservation.Id, *nsxIPReservation)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to create NSX Subnet IPReservation", "NSXSubnetIPReservation", nsxIPReservation.Path)
		return nil, err
	}

	nsxIPReservationCreated, err := s.NSXClient.DynamicIPReservationsClient.Get(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, *nsxIPReservation.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to get NSX Subnet IPReservation", "SubnetIPReservation", nsxIPReservation.Path)
		return nil, err
	}
	err = s.IPReservationStore.Apply(&nsxIPReservationCreated)
	if err != nil {
		return nil, err
	}
	log.Info("Created NSX Subnet IPReservation", "SubnetIPReservation", nsxIPReservation.Path)
	return &nsxIPReservationCreated, nil
}

func (s *IPReservationService) DeleteIPReservationByCRName(ns, name string) error {
	namespacedName := types.NamespacedName{Namespace: ns, Name: name}
	nsxIPReservations := s.IPReservationStore.GetByIndex(ipReservationCRNameIndexKey, namespacedName.String())
	for _, nsxIPReservation := range nsxIPReservations {
		if err := s.DeleteIPReservation(nsxIPReservation); err != nil {
			return err
		}
	}
	return nil
}

func (s *IPReservationService) DeleteIPReservationByCRId(id string) error {
	nsxIPReservations := s.IPReservationStore.GetByIndex(ipReservationCRUIDIndexKey, id)
	for _, nsxIPReservation := range nsxIPReservations {
		if err := s.DeleteIPReservation(nsxIPReservation); err != nil {
			return err
		}
	}
	return nil
}

func (s *IPReservationService) DeleteIPReservation(nsxIPReservation *model.DynamicIpAddressReservation) error {
	ipReservationInfo, _ := common.ParseVPCResourcePath(*nsxIPReservation.Path)
	err := s.NSXClient.DynamicIPReservationsClient.Delete(ipReservationInfo.OrgID, ipReservationInfo.ProjectID, ipReservationInfo.VPCID, ipReservationInfo.ParentID, *nsxIPReservation.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to delete Subnet IPReservation", "SubnetIPReservation", *nsxIPReservation.Path)
		return err
	}
	if err = s.IPReservationStore.Delete(*nsxIPReservation.Id); err != nil {
		return err
	}
	log.Info("Successfully deleted Subnet IPReservation", "SubnetIPReservation", *nsxIPReservation.Id)
	return nil
}

func (s *IPReservationService) ListSubnetIPReservationCRUIDsInStore() sets.Set[string] {
	crUIDs := sets.New[string]()
	for _, obj := range s.IPReservationStore.List() {
		ipr, _ := obj.(*model.DynamicIpAddressReservation)
		for _, tag := range ipr.Tags {
			if *tag.Scope == common.TagScopeSubnetIPReservationCRUID {
				crUIDs.Insert(*tag.Tag)
			}
		}
	}
	return crUIDs
}
