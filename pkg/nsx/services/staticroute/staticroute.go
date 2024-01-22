package staticroute

import (
	"fmt"
	"strings"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	commonctl "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type StaticRouteService struct {
	common.Service
	StaticRouteStore *StaticRouteStore
}

var (
	log                     = logger.Log
	resourceTypeStaticRoute = "StaticRoutes"
	String                  = common.String
)

// InitializeStaticRoute sync NSX resources
func InitializeStaticRoute(commonService common.Service) (*StaticRouteService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(1)
	staticRouteService := &StaticRouteService{Service: commonService}
	staticRouteStore := &StaticRouteStore{}
	staticRouteStore.Indexer = cache.NewIndexer(keyFunc, cache.Indexers{
		common.TagScopeStaticRouteCRUID: indexFunc,
	})
	staticRouteStore.BindingType = model.StaticRoutesBindingType()
	staticRouteService.StaticRouteStore = staticRouteStore
	staticRouteService.NSXConfig = commonService.NSXConfig

	go staticRouteService.InitializeResourceStore(&wg, fatalErrors, resourceTypeStaticRoute, nil, staticRouteService.StaticRouteStore)

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

	vpc := commonctl.ServiceMediator.GetVPCsByNamespace(namespace)
	if len(vpc) == 0 {
		return fmt.Errorf("no vpc found for ns %s", namespace)
	}
	path := strings.Split(*vpc[0].Path, "/")
	err = service.patch(path[2], path[4], *vpc[0].Id, nsxStaticRoute)
	if err != nil {
		return err
	}
	staticRoute, err := service.NSXClient.StaticRouteClient.Get(path[2], path[4], *vpc[0].Id, *nsxStaticRoute.Id)
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
	if err != nil {
		return err
	}
	return nil
}

func (service *StaticRouteService) DeleteStaticRouteByPath(orgId string, projectId string, vpcId string, uid string) error {
	staticRouteClient := service.NSXClient.StaticRouteClient
	staticroute := service.StaticRouteStore.GetByKey(uid)
	if staticroute == nil {
		return nil
	}

	if err := staticRouteClient.Delete(orgId, projectId, vpcId, *staticroute.Id); err != nil {
		return err
	}
	if err := service.StaticRouteStore.Delete(staticroute); err != nil {
		return err
	}

	log.Info("successfully deleted NSX StaticRoute", "nsxStaticRoute", *staticroute.Id)
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

func (service *StaticRouteService) DeleteStaticRoute(namespace string, uid string) error {
	vpc := commonctl.ServiceMediator.GetVPCsByNamespace(namespace)
	if len(vpc) == 0 {
		return nil
	}
	path := strings.Split(*vpc[0].Path, "/")
	return service.DeleteStaticRouteByPath(path[2], path[4], *vpc[0].Id, uid)
}

func (service *StaticRouteService) ListStaticRoute() []*model.StaticRoutes {
	staticRoutes := service.StaticRouteStore.List()
	staticRouteSet := []*model.StaticRoutes{}
	for _, staticroute := range staticRoutes {
		staticRouteSet = append(staticRouteSet, staticroute.(*model.StaticRoutes))
	}
	return staticRouteSet
}

func (service *StaticRouteService) Cleanup() error {
	staticRouteSet := service.ListStaticRoute()
	log.Info("cleanup staticroute", "count", len(staticRouteSet))
	for _, staticRoute := range staticRouteSet {
		path := strings.Split(*staticRoute.Path, "/")
		log.Info("removing staticroute", "staticroute path", *staticRoute.Path)
		err := service.DeleteStaticRouteByPath(path[2], path[4], path[6], *staticRoute.Id)
		if err != nil {
			log.Error(err, "remove staticroute failed", "staticroute id", *staticRoute.Id)
			return err
		}
	}
	return nil
}
