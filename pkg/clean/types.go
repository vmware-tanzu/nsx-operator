package clean

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

const (
	vpcCleanerWorkers = 8
	maxRetries        = 12
)

type vpcPreCleaner interface {
	// CleanupBeforeVPCDeletion is called to clean up the VPC resources which may block the VPC recursive deletion, or
	// break the parallel deletion with other resource types, e.g., VpcSubnetPort, SubnetConnectionBindingMap, LBVirtualServer.
	// CleanupBeforeVPCDeletion is called before recursively deleting the VPCs in parallel.
	CleanupBeforeVPCDeletion(ctx context.Context) error
}

type vpcChildrenCleaner interface {
	// CleanupVPCChildResources is called when cleaning up the VPC related resources. For the resources in an auto-created
	// VPC, this function is called after the VPC is recursively deleted on NSX, so the providers only needs to clean up
	// with the local cache. It uses an empty string for vpcPath for all pre-created VPCs, so the providers should delete
	// resources both on NSX and from the local cache.
	// CleanupVPCChildResources is called after the VPC with path "vpcPath" is recursively deleted.
	CleanupVPCChildResources(ctx context.Context, vpcPath string) error
}

type infraCleaner interface {
	// CleanupInfraResources is to clean up the resources created under path /infra.
	CleanupInfraResources(ctx context.Context) error
}

type cleanupFunc func() (interface{}, error)

type CleanupService struct {
	log *logr.Logger

	vpcService          *vpc.VPCService
	vpcPreCleaners      []vpcPreCleaner
	vpcChildrenCleaners []vpcChildrenCleaner
	infraCleaners       []infraCleaner
	svcErr              error
}

func NewCleanupService() *CleanupService {
	return &CleanupService{}
}

func (c *CleanupService) AddCleanupService(f cleanupFunc) *CleanupService {
	if c.svcErr != nil {
		return c
	}

	var clean interface{}
	clean, c.svcErr = f()
	if c.svcErr != nil {
		return c
	}

	if svc, ok := clean.(vpcPreCleaner); ok {
		c.vpcPreCleaners = append(c.vpcPreCleaners, svc)
	}
	if svc, ok := clean.(vpcChildrenCleaner); ok {
		c.vpcChildrenCleaners = append(c.vpcChildrenCleaners, svc)
	}
	if svc, ok := clean.(infraCleaner); ok {
		c.infraCleaners = append(c.infraCleaners, svc)
	}

	return c
}

func (c *CleanupService) retriable(err error) bool {
	if err != nil && !errors.As(err, &nsxutil.TimeoutFailed) {
		c.log.Info("Retrying to clean up NSX resources", "error", err)
		return true
	}
	return false
}

func (c *CleanupService) cleanupBeforeVPCDeletion(ctx context.Context) error {
	cleanersCount := len(c.vpcPreCleaners)
	if cleanersCount > 0 {
		wgForPreVPCCleaners := sync.WaitGroup{}
		wgForPreVPCCleaners.Add(cleanersCount)
		errorChans := make(chan error, cleanersCount)
		for idx := range c.vpcPreCleaners {
			cleaner := c.vpcPreCleaners[idx]
			go func() {
				defer wgForPreVPCCleaners.Done()
				err := retry.OnError(Backoff, c.retriable, func() error {
					return cleaner.CleanupBeforeVPCDeletion(ctx)
				})
				if err != nil {
					errorChans <- err
				}
				return
			}()
		}
		wgForPreVPCCleaners.Wait()
		if len(errorChans) > 0 {
			err := <-errorChans
			return err
		}
	}
	return nil
}

func (c *CleanupService) cleanupVPCResourcesByVPCPath(ctx context.Context, vpcPath string) error {
	c.log.Info("Cleaning VPC resources", "path", vpcPath)
	if vpcPath != "" {
		if err := c.vpcService.DeleteVPC(vpcPath); err != nil {
			c.log.Error(err, "Failed to delete VPC on NSX", "path", vpcPath)
			return err
		}
		c.log.Info("Deleted VPC", "Path", vpcPath)
	}

	cleanersCount := len(c.vpcChildrenCleaners)
	cleanErrs := make(chan error, len(c.vpcChildrenCleaners))
	defer close(cleanErrs)

	wgForChildrenCleaners := sync.WaitGroup{}
	wgForChildrenCleaners.Add(cleanersCount)
	for idx := range c.vpcChildrenCleaners {
		cleaner := c.vpcChildrenCleaners[idx]
		go func() {
			defer wgForChildrenCleaners.Done()
			err := cleaner.CleanupVPCChildResources(ctx, vpcPath)
			if err != nil {
				cleanErrs <- err
			}
		}()
	}
	wgForChildrenCleaners.Wait()
	if len(cleanErrs) > 0 {
		return <-cleanErrs
	}
	return nil
}

func (c *CleanupService) vpcWorker(ctx context.Context, queue workqueue.TypedRateLimitingInterface[string], potentialVPCs sets.Set[string], completedVPCs sets.Set[string], mu *sync.Mutex, finalErrors chan error) bool {
	vpcPath, shutdown := queue.Get()
	if shutdown {
		return false
	}

	// Mark task as done
	defer queue.Done(vpcPath)

	err := c.cleanupVPCResourcesByVPCPath(ctx, vpcPath)
	if err != nil && queue.NumRequeues(vpcPath) < maxRetries {
		queue.AddAfter(vpcPath, 10*time.Second)
		return true
	}

	defer queue.Forget(vpcPath)
	mu.Lock()
	defer mu.Unlock()

	if err != nil {
		finalErrors <- err
	}

	completedVPCs.Insert(vpcPath)
	if potentialVPCs.Equal(completedVPCs) {
		queue.ShutDown()
	}

	return true
}

func (c *CleanupService) cleanPreCreatedVPCs(ctx context.Context) error {
	if err := retry.OnError(Backoff, c.retriable, func() error {
		return c.cleanupVPCResourcesByVPCPath(ctx, "")
	}); err != nil {
		return errors.Join(nsxutil.CleanupResourceFailed, err)
	}
	return nil
}

func (c *CleanupService) cleanupAutoCreatedVPCs(ctx context.Context) error {
	queue := workqueue.NewTypedRateLimitingQueue[string](workqueue.DefaultTypedControllerRateLimiter[string]())
	defer queue.ShutDown()

	autoCreatedVPCs := c.vpcService.ListAutoCreatedVPCPaths()
	if autoCreatedVPCs.Len() == 0 {
		return nil
	}

	var completedMutex sync.Mutex
	completedVPCs := sets.New[string]()
	vpcFinalErrors := make(chan error, autoCreatedVPCs.Len())
	defer close(vpcFinalErrors)

	wg := &sync.WaitGroup{}
	wg.Add(vpcCleanerWorkers)
	for i := 0; i < vpcCleanerWorkers; i++ {
		go func() {
			defer wg.Done()
			for c.vpcWorker(ctx, queue, autoCreatedVPCs, completedVPCs, &completedMutex, vpcFinalErrors) {
			}
		}()
	}
	for vpcPath := range autoCreatedVPCs {
		queue.Add(vpcPath)
	}

	wg.Wait()

	if len(vpcFinalErrors) > 0 {
		return <-vpcFinalErrors
	}
	return nil
}

// cleanupVPCResources cleans up the VPCs and their children resources created by nsx-operator.
func (c *CleanupService) cleanupVPCResources(ctx context.Context) error {
	// Clean up the indirect VPC children resources before deleting the VPCs, otherwise, it may block VPC deletion request
	if err := c.cleanupBeforeVPCDeletion(ctx); err != nil {
		c.log.Error(err, "Failed to clean up the resources before deleting VPCs")
		return err
	}
	c.log.Info("Completed to clean up the resources before deleting VPCs")

	// Clean up the auto-created VPC and its children resources
	if err := c.cleanupAutoCreatedVPCs(ctx); err != nil {
		c.log.Error(err, "Failed to clean up the auto created VPCs and their child resources")
		return err
	}
	c.log.Info("Completed to clean up the auto created VPCs and their child resources")

	// Clean up the resources in pre-created VPC.
	if err := c.cleanPreCreatedVPCs(ctx); err != nil {
		c.log.Error(err, "Failed to clean up the pre-created VPCs' child resources")
		return err
	}
	c.log.Info("Completed to clean up the pre-created VPCs' child resources")

	return nil
}

func (c *CleanupService) cleanupInfraResources(ctx context.Context) error {
	if err := retry.OnError(Backoff, c.retriable, func() error {
		cleanersCount := len(c.infraCleaners)
		cleanErrs := make([]error, 0)
		wgForInfraCleaners := sync.WaitGroup{}
		wgForInfraCleaners.Add(cleanersCount)

		for idx := range c.infraCleaners {
			cleaner := c.infraCleaners[idx]
			go func() {
				defer wgForInfraCleaners.Done()
				err := cleaner.CleanupInfraResources(ctx)
				if err != nil {
					cleanErrs = append(cleanErrs, err)
				}
			}()
		}

		wgForInfraCleaners.Wait()
		if len(cleanErrs) > 0 {
			return cleanErrs[0]
		}

		return nil
	}); err != nil {
		return err
	}
	return nil
}
