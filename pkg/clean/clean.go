/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package clean

import (
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
)

var log = logger.Log

// Clean cleans up NSX resources,
// including security policy, static route, subnet, subnet port, subnet set, vpc, ip pool, nsx service account
// it is usually used when nsx-operator is uninstalled and remove all the resources created by nsx-operator
// return error if any, return nil if no error
func Clean(cf *config.NSXOperatorConfig) error {
	log.Info("starting NSX cleanup")
	if err := cf.ValidateConfigFromCmd(); err != nil {
		return fmt.Errorf("failed to validate config: %w", err)
	}
	if cleanupService, err := InitializeCleanupService(cf); err != nil {
		return err // failed to get nsx client
	} else if cleanupService.err != nil {
		return fmt.Errorf("failed to initialize cleanup service: %w", cleanupService.err)
	} else {
		for _, clean := range cleanupService.cleans {
			if err := retry.OnError(retry.DefaultRetry, func(err error) bool {
				if err != nil {
					log.Info("retrying to clean up NSX resources", "error", err)
					return true
				}
				return false
			}, func() error {
				if err := clean.Cleanup(); err != nil {
					return fmt.Errorf("failed to clean up specific resource: %w", err)
				}
				return nil
			}); err != nil {
				return err
			}
		}
	}
	log.Info("cleanup NSX resources successfully")
	return nil
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
			return securitypolicy.InitializeSecurityPolicy(service)
		}
	}
	wrapInitializeIPPool := func(service common.Service) cleanupFunc {
		return func() (cleanup, error) {
			return ippool.InitializeIPPool(service)
		}
	}

	wrapInitializeVPC := func(service common.Service) cleanupFunc {
		return func() (cleanup, error) {
			return vpcService, vpcErr
		}
	}

	wrapInitializeStaticRoute := func(service common.Service) cleanupFunc {
		return func() (cleanup, error) {
			return sr.InitializeStaticRoute(service)
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
