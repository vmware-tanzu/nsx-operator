package staticroute

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/crd.nsx.vmware.com/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

type StaticRouteService struct {
	common.Service
	StaticRouteStore *StaticRouteStore
	VPCService       common.VPCServiceProvider
}

var (
	log                     = &logger.Log
	resourceTypeStaticRoute = "StaticRoutes"
	String                  = common.String
)

// InitializeStaticRoute sync NSX resources
func InitializeStaticRoute(commonService common.Service, vpcService common.VPCServiceProvider) (*StaticRouteService, error) {
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
	staticRouteService.VPCService = vpcService

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

	vpc := service.VPCService.ListVPCInfo(namespace)
	if len(vpc) == 0 {
		return fmt.Errorf("no vpc found for ns %s", namespace)
	}
	err = service.patch(vpc[0].OrgID, vpc[0].ProjectID, vpc[0].ID, nsxStaticRoute)
	if err != nil {
		return err
	}
	staticRoute, err := service.NSXClient.StaticRouteClient.Get(vpc[0].OrgID, vpc[0].ProjectID, vpc[0].ID, *nsxStaticRoute.Id)
	err = nsxutil.NSXApiError(err)
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
	err = nsxutil.NSXApiError(err)
	if err != nil {
		return err
	}
	return nil
}

func (service *StaticRouteService) DeleteStaticRouteByPath(orgId string, projectId string, vpcId string, id string) error {
	staticRouteClient := service.NSXClient.StaticRouteClient
	staticroute := service.StaticRouteStore.GetByKey(id)
	if staticroute == nil {
		return nil
	}

	if err := staticRouteClient.Delete(orgId, projectId, vpcId, *staticroute.Id); err != nil {
		err = nsxutil.NSXApiError(err)
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

func (service *StaticRouteService) DeleteStaticRoute(obj *v1alpha1.StaticRoute) error {
	id := util.GenerateIDByObject(obj)
	staticroute := service.StaticRouteStore.GetByKey(id)
	if staticroute == nil {
		return nil
	}
	vpcResourceInfo, err := common.ParseVPCResourcePath(*staticroute.Path)
	if err != nil {
		return err
	}
	return service.DeleteStaticRouteByPath(vpcResourceInfo.OrgID, vpcResourceInfo.ProjectID, vpcResourceInfo.ID, id)
}

func (service *StaticRouteService) ListStaticRoute() []*model.StaticRoutes {
	staticRoutes := service.StaticRouteStore.List()
	staticRouteSet := []*model.StaticRoutes{}
	for _, staticroute := range staticRoutes {
		staticRouteSet = append(staticRouteSet, staticroute.(*model.StaticRoutes))
	}
	return staticRouteSet
}

func (service *StaticRouteService) Cleanup(ctx context.Context) error {
	staticRouteSet := service.ListStaticRoute()
	log.Info("cleanup staticroute", "count", len(staticRouteSet))
	for _, staticRoute := range staticRouteSet {
		path := strings.Split(*staticRoute.Path, "/")
		log.Info("removing staticroute", "staticroute path", *staticRoute.Path)
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			err := service.DeleteStaticRouteByPath(path[2], path[4], path[6], *staticRoute.Id)
			if err != nil {
				log.Error(err, "remove staticroute failed", "staticroute id", *staticRoute.Id)
				return err
			}
		}
	}
	return nil
}
