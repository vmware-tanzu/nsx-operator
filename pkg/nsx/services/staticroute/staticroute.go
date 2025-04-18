package staticroute

import (
	"fmt"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
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
	log    = &logger.Log
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
	staticRouteStore := &StaticRouteStore{}
	staticRouteStore.Indexer = cache.NewIndexer(keyFunc, cache.Indexers{
		common.TagScopeStaticRouteCRUID: indexFunc,
		common.TagScopeNamespace:        indexStaticRouteNamespace,
		common.IndexByVPCPathFuncKey:    common.IndexByVPCFunc,
	})
	staticRouteStore.BindingType = model.StaticRoutesBindingType()
	staticRouteService.StaticRouteStore = staticRouteStore
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

func (service *StaticRouteService) CreateOrUpdateStaticRoute(namespace string, obj *v1alpha1.StaticRoute) error {
	nsxStaticRoute, err := service.buildStaticRoute(obj)
	if err != nil {
		return err
	}

	existingStaticRoute := service.StaticRouteStore.GetByKey(*nsxStaticRoute.Id)
	if existingStaticRoute != nil && service.compareStaticRoute(existingStaticRoute, nsxStaticRoute) {
		return nil
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
	err = service.StaticRouteStore.Add(&staticRoute)
	if err != nil {
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
	id := util.GenerateIDByObject(obj)
	staticroute := service.StaticRouteStore.GetByKey(id)
	if staticroute == nil {
		return nil
	}
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
