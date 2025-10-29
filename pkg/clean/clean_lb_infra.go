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
	case *model.Domain:
		return *v.Path, nil
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

// CleanupBeforeVPCDeletion cleans up LB-related shares before VPC deletion
func (s *LBInfraCleaner) CleanupBeforeVPCDeletion(ctx context.Context) error {
	s.log.Info("Cleaning up LB infra shares before VPC deletion")

	// Clean up shares
	if err := s.cleanupInfraShares(ctx); err != nil {
		s.log.Error(err, "Failed to clean up infra shares")
		return err
	}

	s.log.Info("Successfully cleaned up LB infra shares")
	return nil
}

// CleanupInfraResources is to clean up the LB related resources created under path /infra, including,
// cert/LBAppProfile/LBPersistentProfile/dlb virtual servers/dlb services/dlb groups/dlb pools
func (s *LBInfraCleaner) CleanupInfraResources(ctx context.Context) error {
	// LB virtual server has dependencies on LB pool, so we can't delete vs and pool in parallel.
	if err := s.cleanupInfraDLBVirtualServers(ctx); err != nil {
		s.log.Error(err, "Failed to clean up DLB virtual servers")
		return err
	}

	parallelCleaners := []func(ctx context.Context) error{
		s.cleanupInfraDLBPools,
		s.cleanupInfraDLBServices,
		s.cleanupInfraDLBGroups,
		s.cleanupInfraCerts,
		s.cleanupLBAppProfiles,
		s.cleanupLBPersistenceProfiles,
		s.cleanupLBMonitorProfiles,
		s.cleanupInfraDomain,
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

	s.log.Info("Cleaning up shared resources", "Count", len(srSet))
	sharedResourcesBuilder, _ := common.PolicyPathInfraSharedResource.NewPolicyTreeBuilder()
	return sharedResourcesBuilder.PagingUpdateResources(ctx, srSet, common.DefaultHAPIChildrenCount, s.NSXClient, nil)
}

func (s *LBInfraCleaner) cleanupInfraShares(ctx context.Context) error {
	store, err := s.queryNCPCreatedResources([]string{common.ResourceTypeShare}, model.ShareBindingType(), nil)
	if err != nil {
		return err
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
		return err
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

func (s *LBInfraCleaner) cleanupInfraDomain(ctx context.Context) error {
	store, err := s.queryNCPCreatedResources([]string{common.ResourceTypeDomain}, model.DomainBindingType(), nil)
	if err != nil {
		return err
	}
	var domainSet []*model.Domain
	for _, obj := range store.List() {
		domain := obj.(*model.Domain)
		domain.MarkedForDelete = &MarkedForDelete
		domainSet = append(domainSet, domain)
	}

	s.log.Info("Cleaning up Domain", "Count", len(domainSet))
	domainBuilder, _ := common.PolicyPathInfraDomain.NewPolicyTreeBuilder()
	return domainBuilder.PagingUpdateResources(ctx, domainSet, common.DefaultHAPIChildrenCount, s.NSXClient, nil)
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
		return err
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
		return err
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
		return err
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
		return err
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

func (s *LBInfraCleaner) ListLBAppProfile() ([]*model.LBAppProfile, error) {
	store, err := s.queryNCPCreatedResources([]string{common.ResourceTypeLBHttpProfile, common.ResourceTypeLBFastTcpProfile, common.ResourceTypeLBFastUdpProfile}, model.LBAppProfileBindingType(), nil)
	if err != nil {
		return nil, err
	}

	lbAppProfiles := store.List()
	var lbAppProfilesSet []*model.LBAppProfile
	for _, obj := range lbAppProfiles {
		appProfile := obj.(*model.LBAppProfile)
		lbAppProfilesSet = append(lbAppProfilesSet, appProfile)
	}
	return lbAppProfilesSet, nil
}

func (s *LBInfraCleaner) ListLBPersistenceProfile() ([]*model.LBPersistenceProfile, error) {
	store, err := s.queryNCPCreatedResources([]string{common.ResourceTypeLBCookiePersistenceProfile, common.ResourceTypeLBSourceIpPersistenceProfile}, model.LBPersistenceProfileBindingType(), nil)
	if err != nil {
		return nil, err
	}
	lbPersistenceProfiles := store.List()
	var lbPersistenceProfilesSet []*model.LBPersistenceProfile
	for _, lbPersistenceProfile := range lbPersistenceProfiles {
		lbPersistenceProfilesSet = append(lbPersistenceProfilesSet, lbPersistenceProfile.(*model.LBPersistenceProfile))
	}
	return lbPersistenceProfilesSet, nil
}

func (s *LBInfraCleaner) ListLBMonitorProfile() ([]model.LBMonitorProfile, error) {
	store, err := s.queryNCPCreatedResources([]string{common.ResourceTypeLBHttpMonitorProfile, common.ResourceTypeLBTcpMonitorProfile}, model.LBMonitorProfileBindingType(), nil)
	if err != nil {
		return nil, err
	}

	lbMonitorProfiles := store.List()
	var lbMonitorProfilesSet []model.LBMonitorProfile
	for _, lbMonitorProfile := range lbMonitorProfiles {
		lbMonitorProfilesSet = append(lbMonitorProfilesSet, *lbMonitorProfile.(*model.LBMonitorProfile))
	}
	return lbMonitorProfilesSet, nil
}

func (s *LBInfraCleaner) cleanupLBAppProfiles(ctx context.Context) error {
	lbAppProfiles, err := s.ListLBAppProfile()
	if err != nil {
		s.log.Error(err, "Failed to list lbAppProfiles")
		return err
	}
	s.log.Info("Cleaning up lbAppProfiles", "Count", len(lbAppProfiles))
	var delErr error
	successCount := 0
	failedCount := 0
	for _, lbAppProfile := range lbAppProfiles {
		select {
		case <-ctx.Done():
			s.log.Info("LbAppProfile cleanup interrupted by context", "successCount", successCount, "failedCount", failedCount)
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			id := *lbAppProfile.Id
			name := ""
			if lbAppProfile.DisplayName != nil {
				name = *lbAppProfile.DisplayName
			}
			profileType := lbAppProfile.ResourceType
			s.log.Info("Attempting to delete LB app profile", "profileID", id, "profileName", name, "profileType", profileType)
			if err := s.NSXClient.LbAppProfileClient.Delete(id, &forceDelete); err != nil {
				s.log.Error(err, "Failed to delete LB app profile", "profileID", id, "profileName", name, "profileType", profileType)
				failedCount++
				delErr = err
				continue
			}
			s.log.Info("Successfully deleted LB app profile", "profileID", id, "profileName", name, "profileType", profileType)
			successCount++
		}
	}

	if delErr != nil {
		s.log.Info("LbAppProfile cleanup completed with errors", "successCount", successCount, "failedCount", failedCount)
		return delErr
	}

	s.log.Info("Completed to clean up NCP created lbAppProfiles", "successCount", successCount, "failedCount", failedCount)
	return nil
}

func (s *LBInfraCleaner) cleanupLBPersistenceProfiles(ctx context.Context) error {
	lbPersistenceProfiles, err := s.ListLBPersistenceProfile()
	if err != nil {
		s.log.Error(err, "Failed to list lbPersistenceProfiles")
		return err
	}
	s.log.Info("Cleaning up lbPersistenceProfiles", "Count", len(lbPersistenceProfiles))
	var delErr error
	successCount := 0
	failedCount := 0
	for _, lbPersistenceProfile := range lbPersistenceProfiles {
		select {
		case <-ctx.Done():
			s.log.Info("LbPersistenceProfile cleanup interrupted by context", "successCount", successCount, "failedCount", failedCount)
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			id := *lbPersistenceProfile.Id
			name := ""
			if lbPersistenceProfile.DisplayName != nil {
				name = *lbPersistenceProfile.DisplayName
			}
			profileType := lbPersistenceProfile.ResourceType
			if err := s.NSXClient.LbPersistenceProfilesClient.Delete(*lbPersistenceProfile.Id, &forceDelete); err != nil {
				s.log.Error(err, "Failed to delete LB persistence profile", "profileID", id, "profileName", name, "profileType", profileType)
				failedCount++
				delErr = err
				continue
			}
			s.log.Info("Successfully deleted LB persistence profile", "profileID", id, "profileName", name, "profileType", profileType)
			successCount++
		}
	}

	if delErr != nil {
		s.log.Info("LbPersistenceProfile cleanup completed with errors", "successCount", successCount, "failedCount", failedCount)
		return delErr
	}

	s.log.Info("Completed to clean up NCP created lbPersistenceProfiles", "successCount", successCount, "failedCount", failedCount)
	return nil
}

func (s *LBInfraCleaner) cleanupLBMonitorProfiles(ctx context.Context) error {
	lbMonitorProfiles, err := s.ListLBMonitorProfile()
	if err != nil {
		s.log.Error(err, "Failed to list lbMonitorProfiles")
		return err
	}
	s.log.Info("Cleaning up lbMonitorProfiles", "Count", len(lbMonitorProfiles))
	var delErr error
	successCount := 0
	failedCount := 0
	for _, lbMonitorProfile := range lbMonitorProfiles {
		select {
		case <-ctx.Done():
			s.log.Info("LbMonitorProfile cleanup interrupted by context", "successCount", successCount, "failedCount", failedCount)
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			id := *lbMonitorProfile.Id
			name := ""
			if lbMonitorProfile.DisplayName != nil {
				name = *lbMonitorProfile.DisplayName
			}
			profileType := lbMonitorProfile.ResourceType
			if err := s.NSXClient.LbMonitorProfilesClient.Delete(id, &forceDelete); err != nil {
				s.log.Error(err, "Failed to delete LB monitor profile", "profileID", id, "profileName", name, "profileType", profileType)
				failedCount++
				delErr = err
				continue
			}
			s.log.Info("Successfully deleted LB monitor profile", "profileID", id, "profileName", name, "profileType", profileType)
			successCount++
		}
	}
	if delErr != nil {
		s.log.Info("LbMonitorProfile cleanup completed with errors", "successCount", successCount, "failedCount", failedCount)
		return delErr
	}
	s.log.Info("Completed to clean up NCP created lbMonitorProfiles", "successCount", successCount, "failedCount", failedCount)
	return nil
}
