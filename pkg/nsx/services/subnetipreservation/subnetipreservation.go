package subnetipreservation

import (
	"fmt"
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
	log                                    = logger.Log
	MarkedForDelete                        = true
	ResourceTypeDynamicSubnetIPReservation = "DynamicIpAddressReservation"
	ResourceTypeStaticSubnetIPReservation  = "StaticIpAddressReservation"
)

type IPReservationService struct {
	common.Service
	DynamicIPReservationStore   *DynamicIPReservationStore
	StaticIPReservationStore    *StaticIPReservationStore
	SubnetPortService           common.SubnetPortServiceProvider
	DynamicIPReservationBuilder *common.PolicyTreeBuilder[*model.DynamicIpAddressReservation]
	StaticIPReservationBuilder  *common.PolicyTreeBuilder[*model.StaticIpAddressReservation]
}

// InitializeService initializes SubnetIPReservationService service.
func InitializeService(service common.Service, subnetPortService common.SubnetPortServiceProvider) (*IPReservationService, error) {
	dynamicIPReservationBuilder, _ := common.PolicyPathVpcSubnetDynamicIPReservation.NewPolicyTreeBuilder()
	staticIPReservationBuilder, _ := common.PolicyPathVpcSubnetStaticIPReservation.NewPolicyTreeBuilder()
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error, 2)

	ipReservationService := &IPReservationService{
		Service:                     service,
		DynamicIPReservationStore:   SetupDynamicIPReservationStore(),
		StaticIPReservationStore:    SetupStaticIPReservationStore(),
		DynamicIPReservationBuilder: dynamicIPReservationBuilder,
		StaticIPReservationBuilder:  staticIPReservationBuilder,
		SubnetPortService:           subnetPortService,
	}

	wg.Add(2)
	go ipReservationService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeDynamicSubnetIPReservation, nil, ipReservationService.DynamicIPReservationStore)
	go ipReservationService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeStaticSubnetIPReservation, nil, ipReservationService.StaticIPReservationStore)
	go func() {
		wg.Wait()
		close(wgDone)
	}()

	select {
	case <-wgDone:
		break
	case err := <-fatalErrors:
		return ipReservationService, err
	}

	return ipReservationService, nil
}

func (s *IPReservationService) CreateOrUpdateSubnetIPReservation(ipReservation *v1alpha1.SubnetIPReservation, subnetPath string, restoreMode bool) ([]string, error) {
	log.Info("Getting or creating Subnet IPReservation", "SubnetIPReservation", ipReservation.UID, "nsxSubnetPath", subnetPath)
	// For SubnetIPReservation in restore mode or with ReservedIPs, create NSX StaticIPReservation
	// For SubnetIPReservation with numberOfIPs in normal mode, create NSX DynamicIPReservation
	if restoreMode || len(ipReservation.Spec.ReservedIPs) > 0 {
		return s.CreateOrUpdateStaticIPReservation(ipReservation, subnetPath)
	}
	return s.GetOrCreateDynamicIPReservation(ipReservation, subnetPath)
}

// CreateOrUpdateStaticIPReservation will create or update StaticIPReservation according to the SubnetIPReservation CR.
// StaticIPReservation refers to the SubnetIPReservation with ReservedIPs specified. It can be updated after creation.
func (s *IPReservationService) CreateOrUpdateStaticIPReservation(ipReservation *v1alpha1.SubnetIPReservation, subnetPath string) ([]string, error) {
	nsxIPReservation := s.buildStaticIPReservation(ipReservation, subnetPath)
	isChanged := true
	existingIPReservations := s.StaticIPReservationStore.GetByIndex(common.TagScopeSubnetIPReservationCRUID, string(ipReservation.UID))
	if len(existingIPReservations) > 0 {
		// Update NSX StaticIPReservation id with the existing settings.
		nsxIPReservation.Id = existingIPReservations[0].Id
		isChanged = common.CompareResource(StaticIpAddressReservationToComparable(existingIPReservations[0]), StaticIpAddressReservationToComparable(nsxIPReservation))
		if !isChanged {
			log.Info("NSX StaticIPReservation not changed, skipping the update", "StaticIPReservation", *existingIPReservations[0].Path)
			return existingIPReservations[0].ReservedIps, nil
		}
	}
	log.Info("Updating the NSX StaticIPReservation", "existingStaticIPReservation", existingIPReservations, "desiredSubnetPort", nsxIPReservation)
	subnetInfo, err := common.ParseVPCResourcePath(subnetPath)
	if err != nil {
		return nil, err
	}
	err = s.NSXClient.StaticIPReservationsClient.Patch(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, *nsxIPReservation.Id, *nsxIPReservation)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "failed to create NSX Subnet StaticIPReservation", "StaticIPReservation", nsxIPReservation.Path)
		return nil, err
	}

	nsxIPReservationCreated, err := s.NSXClient.StaticIPReservationsClient.Get(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, *nsxIPReservation.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "failed to get NSX Subnet StaticIPReservation", "StaticIPReservation", nsxIPReservation.Path)
		return nil, err
	}
	// Subnet totalIP will change when ipreservation is created.
	// Need to get the totalIP from NSX again for SubnetPort creation.
	s.SubnetPortService.ResetSubnetTotalIP(subnetPath)
	err = s.StaticIPReservationStore.Apply(&nsxIPReservationCreated)
	if err != nil {
		return nil, err
	}
	log.Info("Created NSX Subnet StaticIPReservation", "StaticIPReservation", nsxIPReservation.Path)
	return nsxIPReservationCreated.ReservedIps, nil
}

// GetOrCreateDynamicIPReservation will create new DynamicIPReservation or get the existing DynamicIPReservation if it exists.
// DynamicIPReservation refers to the SubnetIPReservation with NumberOfIPs specified. It cannot be updated after created.
func (s *IPReservationService) GetOrCreateDynamicIPReservation(ipReservation *v1alpha1.SubnetIPReservation, subnetPath string) ([]string, error) {
	nsxIPReservation := s.buildDynamicIPReservation(ipReservation, subnetPath)
	existingDynamicIPReservations := s.DynamicIPReservationStore.GetByIndex(common.TagScopeSubnetIPReservationCRUID, string(ipReservation.UID))
	if len(existingDynamicIPReservations) > 0 {
		log.Info("NSX Subnet DynamicIPReservation is created, skipping", "DynamicIPReservation", existingDynamicIPReservations[0].Path)
		return existingDynamicIPReservations[0].Ips, nil
	}
	// DynamicIPReservation will be created as NSX StaticIPReservation in restore mode.
	// Thus we need to also check the StaticIPReservationStore for existing DynamicIPReservation.
	existingStaticIPReservations := s.StaticIPReservationStore.GetByIndex(common.TagScopeSubnetIPReservationCRUID, string(ipReservation.UID))
	if len(existingStaticIPReservations) > 0 {
		log.Info("NSX Subnet DynamicIPReservation is created, skipping", "DynamicIPReservation", existingStaticIPReservations[0].Path)
		return existingStaticIPReservations[0].ReservedIps, nil
	}
	subnetInfo, err := common.ParseVPCResourcePath(subnetPath)
	if err != nil {
		return nil, err
	}
	err = s.NSXClient.DynamicIPReservationsClient.Patch(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, *nsxIPReservation.Id, *nsxIPReservation)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "failed to create NSX Subnet DynamicIPReservation", "DynamicIPReservation", nsxIPReservation.Path)
		return nil, err
	}

	nsxIPReservationCreated, err := s.NSXClient.DynamicIPReservationsClient.Get(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, *nsxIPReservation.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "failed to get NSX Subnet DynamicIPReservation", "DynamicIPReservation", nsxIPReservation.Path)
		return nil, err
	}
	// Subnet totalIP will change when ipreservation is created.
	// Need to get the totalIP from NSX again for SubnetPort creation.
	s.SubnetPortService.ResetSubnetTotalIP(subnetPath)
	err = s.DynamicIPReservationStore.Apply(&nsxIPReservationCreated)
	if err != nil {
		return nil, err
	}
	log.Info("Created NSX Subnet DynamicIPReservation", "DynamicIPReservation", nsxIPReservation.Path)
	return nsxIPReservationCreated.Ips, nil
}

func (s *IPReservationService) DeleteIPReservationByCRName(ns, name string) error {
	namespacedName := types.NamespacedName{Namespace: ns, Name: name}
	nsxDynamicIPReservations := s.DynamicIPReservationStore.GetByIndex(common.TagScopeSubnetIPReservationCRName, namespacedName.String())
	for _, nsxIPReservation := range nsxDynamicIPReservations {
		if err := s.DeleteDynamicIPReservation(nsxIPReservation); err != nil {
			return err
		}
	}
	nsxStaticIPReservations := s.StaticIPReservationStore.GetByIndex(common.TagScopeSubnetIPReservationCRName, namespacedName.String())
	for _, nsxIPReservation := range nsxStaticIPReservations {
		if err := s.DeleteStaticIPReservation(nsxIPReservation); err != nil {
			return err
		}
	}
	return nil
}

func (s *IPReservationService) DeleteIPReservationByCRId(id string) error {
	nsxDynamicIPReservations := s.DynamicIPReservationStore.GetByIndex(common.TagScopeSubnetIPReservationCRUID, id)
	for _, nsxIPReservation := range nsxDynamicIPReservations {
		if err := s.DeleteDynamicIPReservation(nsxIPReservation); err != nil {
			return err
		}
	}
	nsxStaticIPReservations := s.StaticIPReservationStore.GetByIndex(common.TagScopeSubnetIPReservationCRUID, id)
	for _, nsxIPReservation := range nsxStaticIPReservations {
		if err := s.DeleteStaticIPReservation(nsxIPReservation); err != nil {
			return err
		}
	}
	return nil
}

func (s *IPReservationService) DeleteStaticIPReservation(nsxIPReservation *model.StaticIpAddressReservation) error {
	ipReservationInfo, _ := common.ParseVPCResourcePath(*nsxIPReservation.Path)
	err := s.NSXClient.StaticIPReservationsClient.Delete(ipReservationInfo.OrgID, ipReservationInfo.ProjectID, ipReservationInfo.VPCID, ipReservationInfo.ParentID, *nsxIPReservation.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to delete Subnet StaticIPReservation", "StaticIPReservation", *nsxIPReservation.Path)
		return err
	}
	// Subnet totalIP will change when ipreservation is deleted.
	// Need to get the totalIP from NSX again for SubnetPort creation.
	subnetPath := fmt.Sprintf("/orgs/%s/projects/%s/vpcs/%s/subnets/%s", ipReservationInfo.OrgID, ipReservationInfo.ProjectID, ipReservationInfo.VPCID, ipReservationInfo.ParentID)
	s.SubnetPortService.ResetSubnetTotalIP(subnetPath)
	if err = s.StaticIPReservationStore.Delete(*nsxIPReservation.Id); err != nil {
		return err
	}
	log.Info("Successfully deleted Subnet StaticIPReservation", "StaticIPReservation", *nsxIPReservation.Id)
	return nil
}

func (s *IPReservationService) DeleteDynamicIPReservation(nsxIPReservation *model.DynamicIpAddressReservation) error {
	ipReservationInfo, _ := common.ParseVPCResourcePath(*nsxIPReservation.Path)
	err := s.NSXClient.DynamicIPReservationsClient.Delete(ipReservationInfo.OrgID, ipReservationInfo.ProjectID, ipReservationInfo.VPCID, ipReservationInfo.ParentID, *nsxIPReservation.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to delete Subnet DynamicIPReservation", "DynamicIPReservation", *nsxIPReservation.Path)
		return err
	}
	// Subnet totalIP will change when ipreservation is deleted.
	// Need to get the totalIP from NSX again for SubnetPort creation.
	subnetPath := fmt.Sprintf("/orgs/%s/projects/%s/vpcs/%s/subnets/%s", ipReservationInfo.OrgID, ipReservationInfo.ProjectID, ipReservationInfo.VPCID, ipReservationInfo.ParentID)
	s.SubnetPortService.ResetSubnetTotalIP(subnetPath)
	if err = s.DynamicIPReservationStore.Delete(*nsxIPReservation.Id); err != nil {
		return err
	}
	log.Info("Successfully deleted Subnet DynamicIPReservation", "DynamicIPReservation", *nsxIPReservation.Id)
	return nil
}

func (s *IPReservationService) ListSubnetIPReservationCRUIDsInStore() sets.Set[string] {
	crUIDs := sets.New[string]()
	for _, obj := range s.DynamicIPReservationStore.List() {
		ipr, _ := obj.(*model.DynamicIpAddressReservation)
		for _, tag := range ipr.Tags {
			if *tag.Scope == common.TagScopeSubnetIPReservationCRUID {
				crUIDs.Insert(*tag.Tag)
			}
		}
	}
	for _, obj := range s.StaticIPReservationStore.List() {
		ipr, _ := obj.(*model.StaticIpAddressReservation)
		for _, tag := range ipr.Tags {
			if *tag.Scope == common.TagScopeSubnetIPReservationCRUID {
				crUIDs.Insert(*tag.Tag)
			}
		}
	}
	return crUIDs
}
