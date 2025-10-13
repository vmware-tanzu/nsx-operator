package staticroute

import (
	"fmt"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/realizestate"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

type StaticRouteService struct {
	common.Service
	StaticRouteStore *StaticRouteStore
	VPCService       common.VPCServiceProvider
	builder          *common.PolicyTreeBuilder[*model.StaticRoutes]
}

var (
	log    = logger.Log
	String = common.String
)

// InitializeStaticRoute sync NSX resources
func InitializeStaticRoute(commonService common.Service, vpcService common.VPCServiceProvider) (*StaticRouteService, error) {
	builder, _ := common.PolicyPathVpcStaticRoutes.NewPolicyTreeBuilder()

	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(1)
	staticRouteService := &StaticRouteService{Service: commonService, builder: builder}
	staticRouteService.StaticRouteStore = buildStaticRouteStore()
	staticRouteService.NSXConfig = commonService.NSXConfig
	staticRouteService.VPCService = vpcService

	go staticRouteService.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeStaticRoutes, nil, staticRouteService.StaticRouteStore)

	go func() {
		wg.Wait()
		close(wgDone)
	}()

	select {
	case <-wgDone:
		break
	case err := <-fatalErrors:
		close(fatalErrors)
		return staticRouteService, err
	}

	return staticRouteService, nil
}

func isStaticRouteReady(staticRoute *v1alpha1.StaticRoute) bool {
	for _, cond := range staticRoute.Status.Conditions {
		if cond.Type == v1alpha1.Ready && cond.Status == v1.ConditionTrue {
			return true
		}
	}
	return false
}

func (service *StaticRouteService) CreateOrUpdateStaticRoute(namespace string, obj *v1alpha1.StaticRoute) error {
	nsxStaticRoute, err := service.buildStaticRoute(obj)
	if err != nil {
		return err
	}

	existingStaticRoute := service.StaticRouteStore.GetStaticRoutesByCRUID(obj.GetUID())
	if existingStaticRoute != nil {
		// Update the generated NSX static route's id and display_name with the existing configurations.
		nsxStaticRoute.Id = String(*existingStaticRoute.Id)
		nsxStaticRoute.DisplayName = String(*existingStaticRoute.DisplayName)
		if service.compareStaticRoute(existingStaticRoute, nsxStaticRoute) {
			// If operator restarts between StaticRoute is created and StaticRoute realizedstate check,
			// unrealized StaticRoute will be saved to the store after full sync.
			// Recheck the realizedstate if the StaticRoute CR is not ready.
			if !isStaticRouteReady(obj) {
				return service.checkStaticRouteRealizeState(existingStaticRoute)
			}
			return nil
		}
	}

	vpc := service.VPCService.ListVPCInfo(namespace)
	if len(vpc) == 0 {
		return fmt.Errorf("no vpc found for ns %s", namespace)
	}
	err = service.patch(vpc[0].OrgID, vpc[0].ProjectID, vpc[0].ID, nsxStaticRoute)
	if err != nil {
		return err
	}
	staticRoute, err := service.NSXClient.StaticRouteClient.Get(vpc[0].OrgID, vpc[0].ProjectID, vpc[0].ID, *nsxStaticRoute.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		return err
	}
	err = service.checkStaticRouteRealizeState(&staticRoute)
	if err != nil {
		return err
	}
	err = service.StaticRouteStore.Add(&staticRoute)
	if err != nil {
		return err
	}
	return nil
}

func (service *StaticRouteService) checkStaticRouteRealizeState(staticRoute *model.StaticRoutes) error {
	realizeService := realizestate.InitializeRealizeState(service.Service)
	if err := realizeService.CheckRealizeState(util.NSXTRealizeRetry, *staticRoute.Path, []string{}); err != nil {
		log.Error(err, "Failed to check static route realization state", "ID", *staticRoute.Id)
		deleteErr := service.DeleteStaticRoute(staticRoute)
		if deleteErr != nil {
			log.Error(deleteErr, "Failed to delete static route after realization check failure", "ID", *staticRoute.Id)
			return fmt.Errorf("realization check failed: %v; deletion failed: %v", err, deleteErr)
		}
		return err
	}
	return nil
}

func (service *StaticRouteService) patch(orgId string, projectId string, vpcId string, st *model.StaticRoutes) error {
	err := service.NSXClient.StaticRouteClient.Patch(orgId, projectId, vpcId, *st.Id, *st)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		return err
	}
	return nil
}

func (service *StaticRouteService) DeleteStaticRoute(nsxStaticRoute *model.StaticRoutes) error {
	staticRouteClient := service.NSXClient.StaticRouteClient
	vpcInfo, err := common.ParseVPCResourcePath(*nsxStaticRoute.Path)
	if err != nil {
		log.Error(err, "Failed to parse NSX VPC path for StaticRoute", "path", *nsxStaticRoute.Path)
		return err
	}
	if err := staticRouteClient.Delete(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, *nsxStaticRoute.Id); err != nil {
		err = nsxutil.TransNSXApiError(err)
		return err
	}
	if err := service.StaticRouteStore.Delete(nsxStaticRoute); err != nil {
		return err
	}

	log.Info("Successfully deleted NSX StaticRoute", "nsxStaticRoute", *nsxStaticRoute.Id)
	return nil
}

func (service *StaticRouteService) GetUID(staticroute *model.StaticRoutes) *string {
	if staticroute == nil {
		return nil
	}
	for _, tag := range staticroute.Tags {
		if *tag.Scope == common.TagScopeStaticRouteCRUID {
			return tag.Tag
		}
	}
	return nil

}

func (service *StaticRouteService) DeleteStaticRouteByCR(obj *v1alpha1.StaticRoute) error {
	// Use obj.UID as the index to search the NSX StaticRoute from the local cache. Since this function is called
	// when the "StaticRoute" is got from the kube-apiserver and its DeletionTimestamp is not Zero, the UID field
	// must be set in the CR.
	staticroutes := service.StaticRouteStore.GetByIndex(common.TagScopeStaticRouteCRUID, string(obj.GetUID()))
	if len(staticroutes) == 0 {
		return nil
	}
	staticroute := staticroutes[0].(*model.StaticRoutes)
	return service.DeleteStaticRoute(staticroute)
}

func (service *StaticRouteService) ListStaticRouteByName(ns, name string) []*model.StaticRoutes {
	var result []*model.StaticRoutes
	staticroutes := service.StaticRouteStore.GetByIndex(common.TagScopeNamespace, ns)
	for _, staticroute := range staticroutes {
		sr := staticroute.(*model.StaticRoutes)
		tagname := nsxutil.FindTag(sr.Tags, common.TagScopeStaticRouteCRName)
		if tagname == name {
			result = append(result, staticroute.(*model.StaticRoutes))
		}
	}
	return result
}

func (service *StaticRouteService) ListStaticRoute() []*model.StaticRoutes {
	staticRoutes := service.StaticRouteStore.List()
	staticRouteSet := []*model.StaticRoutes{}
	for _, staticroute := range staticRoutes {
		staticRouteSet = append(staticRouteSet, staticroute.(*model.StaticRoutes))
	}
	return staticRouteSet
}
