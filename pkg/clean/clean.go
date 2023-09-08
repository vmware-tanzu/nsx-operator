/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package clean

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/client-go/util/retry"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	commonctl "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/ippool"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
	sr "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/staticroute"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var log = logger.Log

// Clean cleans up NSX resources,
// including security policy, static route, subnet, subnet port, subnet set, vpc, ip pool, nsx service account
// it is usually used when nsx-operator is uninstalled and remove all the resources created by nsx-operator
// return error if any, return nil if no error
// the error type include followings:
// ValidationFailed 			indicate that the config is incorrect and failed to pass validation
// GetNSXClientFailed  			indicate that could not retrieve nsx client to perform cleanup operation
// InitCleanupServiceFailed 	indicate that error happened when trying to initialize cleanup service
// CleanupResourceFailed    	indicate that the cleanup operation failed at some services, the detailed will in the service logs
func Clean(ctx context.Context, cf *config.NSXOperatorConfig) error {
	log.Info("starting NSX cleanup")
	if err := cf.ValidateConfigFromCmd(); err != nil {
		return errors.Join(nsxutil.ValidationFailed, err)
	}
	nsxClient := nsx.GetClient(cf)
	if nsxClient == nil {
		return nsxutil.GetNSXClientFailed
	}
	if cleanupService, err := InitializeCleanupService(cf); err != nil {
		return errors.Join(nsxutil.InitCleanupServiceFailed, err)
	} else if cleanupService.err != nil {
		return errors.Join(nsxutil.InitCleanupServiceFailed, cleanupService.err)
	} else {
		for _, clean := range cleanupService.cleans {
			if err := retry.OnError(retry.DefaultRetry, retriable, wrapCleanFunc(ctx, clean)); err != nil {
				return errors.Join(nsxutil.CleanupResourceFailed, err)
			}
		}
	}
	log.Info("cleanup NSX resources successfully")
	return nil
}

func retriable(err error) bool {
	if err != nil && !errors.As(err, &nsxutil.TimeoutFailed) {
		log.Info("retrying to clean up NSX resources", "error", err)
		return true
	}
	return false
}

func wrapCleanFunc(ctx context.Context, clean cleanup) func() error {
	return func() error {
		if err := clean.Cleanup(ctx); err != nil {
			return err
		}
		return nil
	}
}

// InitializeCleanupService initializes all the CR services
func InitializeCleanupService(cf *config.NSXOperatorConfig) (*CleanupService, error) {
	cleanupService := NewCleanupService()

	nsxClient := nsx.GetClient(cf)
	if nsxClient == nil {
		return cleanupService, fmt.Errorf("failed to get nsx client")
	}

	var commonService = common.Service{
		NSXClient: nsxClient,
		NSXConfig: cf,
	}
	vpcService, vpcErr := vpc.InitializeVPC(commonService)

	vpcService, vpcErr := vpc.InitializeVPC(commonService)
	commonctl.ServiceMediator.VPCService = vpcService

	// initialize all the CR services
	// Use Fluent Interface to escape error check hell

	wrapInitializeSubnetService := func(service common.Service) cleanupFunc {
		return func() (cleanup, error) {
			return subnet.InitializeSubnetService(service)
		}
	}
	wrapInitializeSecurityPolicy := func(service common.Service) cleanupFunc {
		return func() (cleanup, error) {
			return securitypolicy.InitializeSecurityPolicy(service, vpcService)
		}
	}
	wrapInitializeIPPool := func(service common.Service) cleanupFunc {
		return func() (cleanup, error) {
			return ippool.InitializeIPPool(service, vpcService)
		}
	}

	wrapInitializeVPC := func(service common.Service) cleanupFunc {
		return func() (cleanup, error) {
			return vpcService, vpcErr
		}
	}

	wrapInitializeStaticRoute := func(service common.Service) cleanupFunc {
		return func() (cleanup, error) {
			return sr.InitializeStaticRoute(service, vpcService)
		}
	}

	wrapInitializeSubnetPort := func(service common.Service) cleanupFunc {
		return func() (cleanup, error) {
			return subnetport.InitializeSubnetPort(service)
		}
	}
	// TODO: initialize other CR services
	cleanupService = cleanupService.
		AddCleanupService(wrapInitializeSubnetPort(commonService)).
		AddCleanupService(wrapInitializeSubnetService(commonService)).
		AddCleanupService(wrapInitializeSecurityPolicy(commonService)).
		AddCleanupService(wrapInitializeIPPool(commonService)).
		AddCleanupService(wrapInitializeStaticRoute(commonService)).
		AddCleanupService(wrapInitializeVPC(commonService))

	return cleanupService, nil
}
