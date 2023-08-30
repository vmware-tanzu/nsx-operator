/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package clean

import (
	"fmt"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
)

var log = logger.Log

// Clean cleans up NSX resources,
// including security policy, static route, subnet, subnet port, subnet set, vpc, ip pool, nsx service account
// it is usually used when nsx-operator is uninstalled and remove all the resources created by nsx-operator
// return error if any, return nil if no error
func Clean(cf *config.NSXOperatorConfig) error {
	log.Info("starting NSX cleanup")
	if cleanupServices, err := InitializeCleanupServices(cf); err != nil {
		return fmt.Errorf("failed to initialize cleanup service: %w", err)
	} else {
		for _, cleanupService := range cleanupServices {
			if err := cleanupService.Cleanup(); err != nil {
				return fmt.Errorf("failed to clean up: %w", err)
			}
		}
	}
	log.Info("cleanup NSX resources successfully")
	return nil
}

// InitializeCleanupServices initializes all the CR services
func InitializeCleanupServices(cf *config.NSXOperatorConfig) ([]cleanup, error) {
	var cleanupServices []cleanup

	nsxClient := nsx.GetClient(cf)
	if nsxClient == nil {
		return nil, fmt.Errorf("failed to get nsx client")
	}

	var commonService = common.Service{
		NSXClient: nsxClient,
		NSXConfig: cf,
	}

	// initialize all the CR services
	securityService, err := securitypolicy.InitializeSecurityPolicy(commonService)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize security policy service: %w", err)
	} else {
		cleanupServices = append(cleanupServices, securityService)
	}
	// TODO: initialize other CR services

	return cleanupServices, nil
}
