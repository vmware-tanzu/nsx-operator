/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package clean

import (
	"context"
	"errors"
	"time"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/inventory"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/ipaddressallocation"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
	sr "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/staticroute"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetbinding"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/wait"
)

var Backoff = wait.Backoff{
	Steps:    12,
	Duration: 10 * time.Second,
	Factor:   2.0,
	Jitter:   0.1,
}

// Clean cleans up NSX resources,
// including security policy, static route, subnet, subnet port, subnet set, vpc, ip pool, nsx service account
// besides, it also cleans up DLB resources, which was previously implemented in nsx-ncp,
// it is usually used when nsx-operator is uninstalled and remove all the resources created by nsx-operator
// return error if any, return nil if no error
// the error type include followings:
// ValidationFailed 			indicate that the config is incorrect and failed to pass validation
// GetNSXClientFailed  			indicate that could not retrieve nsx client to perform cleanup operation
// InitCleanupServiceFailed 	indicate that error happened when trying to initialize cleanup service
// CleanupResourceFailed    	indicate that the cleanup operation failed at some services, the detailed will in the service logs
func Clean(ctx context.Context, cf *config.NSXOperatorConfig, log *logr.Logger, debug bool, logLevel int) error {
	// Clean needs to support many instances which each have its own logger
	if log == nil {
		logg := logger.ZapLogger(debug, logLevel)
		log = &logg
	}
	logger.InitLog(log)

	log.Info("Starting NSX cleanup")
	if err := cf.ValidateConfigFromCmd(); err != nil {
		return errors.Join(nsxutil.ValidationFailed, err)
	}
	cf.LibMode = true
	nsxClient := nsx.GetClient(cf)
	if nsxClient == nil {
		return nsxutil.GetNSXClientFailed
	}
	// add timeout for initialization
	errChan := make(chan error)
	var cleanupService *CleanupService
	var err error
	go func() {
		cleanupService, err = InitializeCleanupService(cf, nsxClient, log)
		errChan <- err
	}()

	select {
	case err := <-errChan:
		if err != nil {
			return errors.Join(nsxutil.InitCleanupServiceFailed, err)
		}
	case <-ctx.Done():
		return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
	}

	if cleanupService.svcErr != nil {
		return errors.Join(nsxutil.InitCleanupServiceFailed, cleanupService.svcErr)
	}

	cleanupService.log = log

	if err := cleanupService.cleanupVPCResources(ctx); err != nil {
		return errors.Join(nsxutil.CleanupResourceFailed, err)
	}

	if err := cleanupService.cleanupInfraResources(ctx); err != nil {
		return errors.Join(nsxutil.CleanupResourceFailed, err)
	}

	log.Info("Cleanup NSX resources successfully")
	return nil
}

// InitializeCleanupService initializes all the CR services
func InitializeCleanupService(cf *config.NSXOperatorConfig, nsxClient *nsx.Client, log *logr.Logger) (*CleanupService, error) {
	cleanupService := NewCleanupService()

	commonService := common.Service{
		NSXClient: nsxClient,
		NSXConfig: cf,
	}
	vpcService, vpcErr := vpc.InitializeVPC(commonService)

	// initialize all the CR services
	// Use Fluent Interface to escape error check hell

	wrapInitializeSubnetService := func(service common.Service) cleanupFunc {
		return func() (interface{}, error) {
			return subnet.InitializeSubnetService(service)
		}
	}
	wrapInitializeSecurityPolicy := func(service common.Service) cleanupFunc {
		return func() (interface{}, error) {
			return securitypolicy.InitializeSecurityPolicy(service, vpcService, true)
		}
	}
	wrapInitializeVPC := func(service common.Service) cleanupFunc {
		return func() (interface{}, error) {
			return vpcService, vpcErr
		}
	}

	wrapInitializeStaticRoute := func(service common.Service) cleanupFunc {
		return func() (interface{}, error) {
			return sr.InitializeStaticRoute(service, vpcService)
		}
	}

	wrapInitializeSubnetPort := func(service common.Service) cleanupFunc {
		return func() (interface{}, error) {
			return subnetport.InitializeSubnetPort(service)
		}
	}
	wrapInitializeIPAddressAllocation := func(service common.Service) cleanupFunc {
		return func() (interface{}, error) {
			return ipaddressallocation.InitializeIPAddressAllocation(service, vpcService, true)
		}
	}
	wrapInitializeSubnetBinding := func(service common.Service) cleanupFunc {
		return func() (interface{}, error) {
			return subnetbinding.InitializeService(service)
		}
	}
	wrapInitializeInventory := func(service common.Service) cleanupFunc {
		return func() (interface{}, error) {
			return inventory.InitializeService(service, true)
		}
	}
	wrapInitializeLBInfraCleaner := func(service common.Service) cleanupFunc {
		return func() (interface{}, error) {
			return &LBInfraCleaner{Service: service, log: log}, nil
		}
	}

	cleanupService.vpcService = vpcService
	// TODO: initialize other CR services
	cleanupService = cleanupService.
		AddCleanupService(wrapInitializeSubnetPort(commonService)).
		AddCleanupService(wrapInitializeSubnetBinding(commonService)).
		AddCleanupService(wrapInitializeSubnetService(commonService)).
		AddCleanupService(wrapInitializeSecurityPolicy(commonService)).
		AddCleanupService(wrapInitializeStaticRoute(commonService)).
		AddCleanupService(wrapInitializeVPC(commonService)).
		AddCleanupService(wrapInitializeIPAddressAllocation(commonService)).
		AddCleanupService(wrapInitializeInventory(commonService)).
		AddCleanupService(wrapInitializeLBInfraCleaner(commonService))

	return cleanupService, nil
}
