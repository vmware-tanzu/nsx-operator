package staticroute

import (
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
	mediator                = commonctl.ServiceMediator
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

	vpc := mediator.GetVPCsByNamespace(namespace)
	if len(vpc) == 0 {
		return nil
	}
	path := strings.Split(*vpc[0].Path, "/")
	err = service.patch(path[2], path[4], *vpc[0].Id, nsxStaticRoute)
	if err != nil {
		return err
	}
	err = service.StaticRouteStore.Add(*nsxStaticRoute)
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
	if err := service.StaticRouteStore.Delete(*staticroute); err != nil {
		return err
	}

	log.Info("successfully deleted NSX StaticRoute", "nsxStaticRoute", *staticroute.Id)
	return nil
}

func (service *StaticRouteService) DeleteStaticRoute(namespace string, uid string) error {
	vpc := mediator.GetVPCsByNamespace(namespace)
	if len(vpc) == 0 {
		return nil
	}
	path := strings.Split(*vpc[0].Path, "/")
	return service.DeleteStaticRouteByPath(path[2], path[4], *vpc[0].Id, uid)
}

func (service *StaticRouteService) ListStaticRoute() []model.StaticRoutes {
	staticRoutes := service.StaticRouteStore.List()
	staticRouteSet := []model.StaticRoutes{}
	for _, staticroute := range staticRoutes {
		staticRouteSet = append(staticRouteSet, staticroute.(model.StaticRoutes))
	}
	return staticRouteSet
}
