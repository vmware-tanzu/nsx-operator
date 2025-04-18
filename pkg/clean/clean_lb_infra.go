package clean

import (
	"context"
	"errors"
	"sync"

	"github.com/go-logr/logr"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	MarkedForDelete = true
	forceDelete     = true
)

type LBInfraCleaner struct {
	common.Service
	log *logr.Logger
}

func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *model.LBVirtualServer:
		return *v.Path, nil
	case *model.LBService:
		return *v.Path, nil
	case *model.LBPool:
		return *v.Path, nil
	case *model.LBAppProfile:
		return *v.Path, nil
	case *model.LBMonitorProfile:
		return *v.Path, nil
	case *model.LBPersistenceProfile:
		return *v.Path, nil
	case *model.Share:
		return *v.Path, nil
	case *model.SharedResource:
		return *v.Path, nil
	case *model.TlsCertificate:
		return *v.Path, nil
	case *model.Group:
		return *v.Path, nil
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

// CleanupInfraResources is to clean up the LB related resources created under path /infra, including,
// group/share/cert/LBAppProfile/LBPersistentProfile/dlb virtual servers/dlb services/dlb groups/dlb pools
func (s *LBInfraCleaner) CleanupInfraResources(ctx context.Context) error {
	// LB virtual server has dependencies on LB pool, so we can't delete vs and pool in parallel.
	if err := s.cleanupInfraDLBVirtualServers(ctx); err != nil {
		s.log.Error(err, "Failed to clean up DLB virtual servers")
		return err
	}
	// SharedResource has dependencies on Group, so we can't delete sharedResources and groups in parallel.
	if err := s.cleanupInfraSharedResources(ctx); err != nil {
		s.log.Error(err, "Failed to clean up infra SharedResources")
		return err
	}

	parallelCleaners := []func(ctx context.Context) error{
		s.cleanupInfraShares,
		s.cleanupInfraDLBPools,
		s.cleanupInfraDLBServices,
		s.cleanupInfraDLBGroups,
		s.cleanupInfraCerts,
		s.cleanupLBAppProfiles,
		s.cleanupLBPersistenceProfiles,
		s.cleanupLBMonitorProfiles,
	}

	cleanerCount := len(parallelCleaners)
	errs := make(chan error, cleanerCount)
	defer close(errs)
	wg := sync.WaitGroup{}
	wg.Add(cleanerCount)
	for i := range parallelCleaners {
		cleaner := parallelCleaners[i]
		go func() {
			defer wg.Done()
			err := cleaner(ctx)
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		return <-errs
	}
	return nil
}

func (s *LBInfraCleaner) cleanupInfraSharedResources(ctx context.Context) error {
	store, err := s.queryNCPCreatedResources([]string{common.ResourceTypeSharedResource}, model.SharedResourceBindingType(), nil)
	if err != nil {
		return nil
	}

	var srSet []*model.SharedResource
	for _, obj := range store.List() {
		sr := obj.(*model.SharedResource)
		sr.MarkedForDelete = &MarkedForDelete
		srSet = append(srSet, sr)
	}

	s.log.Info("Cleaning up shares", "Count", len(srSet))
	sharedResourcesBuilder, _ := common.PolicyPathInfraSharedResource.NewPolicyTreeBuilder()
	return sharedResourcesBuilder.PagingUpdateResources(ctx, srSet, common.DefaultHAPIChildrenCount, s.NSXClient, nil)
}

func (s *LBInfraCleaner) cleanupInfraShares(ctx context.Context) error {
	store, err := s.queryNCPCreatedResources([]string{common.ResourceTypeShare}, model.ShareBindingType(), nil)
	if err != nil {
		return nil
	}

	var sharesSet []*model.Share
	for _, obj := range store.List() {
		share := obj.(*model.Share)
		share.MarkedForDelete = &MarkedForDelete
		sharesSet = append(sharesSet, share)
	}

	s.log.Info("Cleaning up shares", "Count", len(sharesSet))
	sharesBuilder, _ := common.PolicyPathInfraShare.NewPolicyTreeBuilder()
	return sharesBuilder.PagingUpdateResources(ctx, sharesSet, common.DefaultHAPIChildrenCount, s.NSXClient, nil)
}

func (s *LBInfraCleaner) cleanupInfraCerts(ctx context.Context) error {
	store, err := s.queryNCPCreatedResources([]string{common.ResourceTypeTlsCertificate}, model.TlsCertificateBindingType(), nil)
	if err != nil {
		return nil
	}
	var certsSet []*model.TlsCertificate
	for _, obj := range store.List() {
		cert := obj.(*model.TlsCertificate)
		cert.MarkedForDelete = &MarkedForDelete
		certsSet = append(certsSet, cert)
	}

	s.log.Info("Cleaning up certificates", "Count", len(certsSet))
	certsBuilder, _ := common.PolicyPathInfraCert.NewPolicyTreeBuilder()
	return certsBuilder.PagingUpdateResources(ctx, certsSet, common.DefaultHAPIChildrenCount, s.NSXClient, nil)
}

type ResourceStore struct {
	common.ResourceStore
}

func (r *ResourceStore) Apply(i interface{}) error {
	return nil
}

func (s *LBInfraCleaner) queryNCPCreatedResources(resourceTypes []string, resourceBindingType bindings.BindingType, additionalQueryFn func(query string) string) (*ResourceStore, error) {
	store := &ResourceStore{common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: resourceBindingType,
	}}
	if err := s.Service.QueryNCPCreatedResources(resourceTypes, store, additionalQueryFn); err != nil {
		s.log.Error(err, "Failed to query NCP created resources", "resource types", resourceTypes)
		return nil, err
	}
	return store, nil
}

func (s *LBInfraCleaner) queryDLBResources(resourceTypes []string, resourceBindingType bindings.BindingType) (*ResourceStore, error) {
	return s.queryNCPCreatedResources(resourceTypes, resourceBindingType, func(query string) string {
		return common.AddNCPCreatedForTag(query, common.TagValueDLB)
	})
}

func (s *LBInfraCleaner) cleanupInfraDLBVirtualServers(ctx context.Context) error {
	store, err := s.queryDLBResources([]string{common.ResourceTypeLBVirtualServer}, model.LBVirtualServerBindingType())
	if err != nil {
		return nil
	}

	var vss []*model.LBVirtualServer
	for _, obj := range store.List() {
		vs := obj.(*model.LBVirtualServer)
		vs.MarkedForDelete = &MarkedForDelete
		vss = append(vss, vs)
	}

	s.log.Info("Cleaning up DLB virtual servers", "Count", len(vss))
	vssBuilder, _ := common.PolicyPathInfraLBVirtualServer.NewPolicyTreeBuilder()
	return vssBuilder.PagingUpdateResources(ctx, vss, common.DefaultHAPIChildrenCount, s.NSXClient, nil)
}

func (s *LBInfraCleaner) cleanupInfraDLBPools(ctx context.Context) error {
	store, err := s.queryDLBResources([]string{common.ResourceTypeLBPool}, model.LBPoolBindingType())
	if err != nil {
		return nil
	}

	var pools []*model.LBPool
	for _, obj := range store.List() {
		pool := obj.(*model.LBPool)
		pool.MarkedForDelete = &MarkedForDelete
		pools = append(pools, pool)
	}

	s.log.Info("Cleaning up DLB pools", "Count", len(pools))
	poolBuilder, _ := common.PolicyPathInfraLBPool.NewPolicyTreeBuilder()
	return poolBuilder.PagingUpdateResources(ctx, pools, common.DefaultHAPIChildrenCount, s.NSXClient, nil)
}

func (s *LBInfraCleaner) cleanupInfraDLBServices(ctx context.Context) error {
	store, err := s.queryDLBResources([]string{common.ResourceTypeLBService}, model.LBServiceBindingType())
	if err != nil {
		return nil
	}

	var lbServices []*model.LBService
	for _, obj := range store.List() {
		svc := obj.(*model.LBService)
		svc.MarkedForDelete = &MarkedForDelete
		lbServices = append(lbServices, svc)
	}

	s.log.Info("Cleaning up DLB services", "Count", len(lbServices))
	lbsBuilder, _ := common.PolicyPathInfraLBService.NewPolicyTreeBuilder()
	return lbsBuilder.PagingUpdateResources(ctx, lbServices, common.DefaultHAPIChildrenCount, s.NSXClient, nil)
}

func (s *LBInfraCleaner) cleanupInfraDLBGroups(ctx context.Context) error {
	store, err := s.queryDLBResources([]string{common.ResourceTypeGroup}, model.GroupBindingType())
	if err != nil {
		return nil
	}

	var lbGroups []*model.Group
	for _, obj := range store.List() {
		grp := obj.(*model.Group)
		grp.MarkedForDelete = &MarkedForDelete
		lbGroups = append(lbGroups, grp)
	}

	s.log.Info("Cleaning up DLB groups", "Count", len(lbGroups))
	groupBuilder, _ := common.PolicyPathInfraGroup.NewPolicyTreeBuilder()
	return groupBuilder.PagingUpdateResources(ctx, lbGroups, common.DefaultHAPIChildrenCount, s.NSXClient, nil)
}

func (s *LBInfraCleaner) ListLBAppProfile() []*model.LBAppProfile {
	store, err := s.queryNCPCreatedResources([]string{common.ResourceTypeLBHttpProfile, common.ResourceTypeLBFastTcpProfile, common.ResourceTypeLBFastUdpProfile}, model.LBAppProfileBindingType(), nil)
	if err != nil {
		return nil
	}

	lbAppProfiles := store.List()
	var lbAppProfilesSet []*model.LBAppProfile
	for _, obj := range lbAppProfiles {
		appProfile := obj.(*model.LBAppProfile)
		lbAppProfilesSet = append(lbAppProfilesSet, appProfile)
	}
	return lbAppProfilesSet
}

func (s *LBInfraCleaner) ListLBPersistenceProfile() []*model.LBPersistenceProfile {
	store, err := s.queryNCPCreatedResources([]string{common.ResourceTypeLBCookiePersistenceProfile, common.ResourceTypeLBSourceIpPersistenceProfile}, model.LBPersistenceProfileBindingType(), nil)
	if err != nil {
		return nil
	}
	lbPersistenceProfiles := store.List()
	var lbPersistenceProfilesSet []*model.LBPersistenceProfile
	for _, lbPersistenceProfile := range lbPersistenceProfiles {
		lbPersistenceProfilesSet = append(lbPersistenceProfilesSet, lbPersistenceProfile.(*model.LBPersistenceProfile))
	}
	return lbPersistenceProfilesSet
}

func (s *LBInfraCleaner) ListLBMonitorProfile() []model.LBMonitorProfile {
	store, err := s.queryNCPCreatedResources([]string{common.ResourceTypeLBHttpMonitorProfile, common.ResourceTypeLBTcpMonitorProfile}, model.LBMonitorProfileBindingType(), nil)
	if err != nil {
		return nil
	}

	lbMonitorProfiles := store.List()
	var lbMonitorProfilesSet []model.LBMonitorProfile
	for _, lbMonitorProfile := range lbMonitorProfiles {
		lbMonitorProfilesSet = append(lbMonitorProfilesSet, *lbMonitorProfile.(*model.LBMonitorProfile))
	}
	return lbMonitorProfilesSet
}

func (s *LBInfraCleaner) cleanupLBAppProfiles(ctx context.Context) error {
	lbAppProfiles := s.ListLBAppProfile()
	s.log.Info("Cleaning up lbAppProfiles", "Count", len(lbAppProfiles))
	var delErr error
	for _, lbAppProfile := range lbAppProfiles {
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			id := *lbAppProfile.Id
			if err := s.NSXClient.LbAppProfileClient.Delete(id, &forceDelete); err != nil {
				s.log.Error(err, "Failed to deleted NCP created lbAppProfile", "lbAppProfile", id)
				delErr = err
				continue
			}
		}
	}

	if delErr != nil {
		return delErr
	}

	s.log.Info("Completed to clean up NCP created lbAppProfiles")
	return nil
}

func (s *LBInfraCleaner) cleanupLBPersistenceProfiles(ctx context.Context) error {
	lbPersistenceProfiles := s.ListLBPersistenceProfile()
	s.log.Info("Cleaning up lbPersistenceProfiles", "Count", len(lbPersistenceProfiles))
	var delErr error
	for _, lbPersistenceProfile := range lbPersistenceProfiles {
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			id := *lbPersistenceProfile.Id
			if err := s.NSXClient.LbPersistenceProfilesClient.Delete(*lbPersistenceProfile.Id, &forceDelete); err != nil {
				s.log.Error(err, "Failed to deleted NCP created lbPersistenceProfile", "lbPersistenceProfile", id)
				delErr = err
				continue
			}
		}
	}

	if delErr != nil {
		return delErr
	}

	s.log.Info("Completed to clean up NCP created lbPersistenceProfiles")
	return nil
}

func (s *LBInfraCleaner) cleanupLBMonitorProfiles(ctx context.Context) error {
	lbMonitorProfiles := s.ListLBMonitorProfile()
	s.log.Info("Cleaning up lbMonitorProfiles", "Count", len(lbMonitorProfiles))
	var delErr error
	for _, lbMonitorProfile := range lbMonitorProfiles {
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			id := *lbMonitorProfile.Id
			if err := s.NSXClient.LbMonitorProfilesClient.Delete(id, &forceDelete); err != nil {
				s.log.Error(err, "Failed to deleted NCP created lbMonitorProfile", "lbMonitorProfile", id)
				delErr = err
				continue
			}
		}
	}
	if delErr != nil {
		return delErr
	}
	s.log.Info("Completed to clean up NCP created lbMonitorProfiles")
	return nil
}
