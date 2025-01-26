/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package realizestate

import (
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	log = &logger.Log
)

type RealizeStateService struct {
	common.Service
}

func InitializeRealizeState(service common.Service) *RealizeStateService {
	return &RealizeStateService{
		Service: service,
	}
}

// CheckRealizeState allows the caller to check realize status of intentPath with retries.
// Backoff defines the maximum retries and the wait interval between two retries.
// Check all the entities, all entities should be in the REALIZED state to be treated as REALIZED
func (service *RealizeStateService) CheckRealizeState(backoff wait.Backoff, intentPath string, extraIds []string) error {
	// TODO， ask NSX if there were multiple realize states could we check only the latest one?
	return retry.OnError(backoff, func(err error) bool {
		// Won't retry when realized state is `ERROR`.
		return !nsxutil.IsRealizeStateError(err)
	}, func() error {
		results, err := service.NSXClient.RealizedEntitiesClient.List(intentPath, nil)
		err = nsxutil.TransNSXApiError(err)
		if err != nil {
			return err
		}
		entitiesRealized := 0
		extraIdsRealized := 0
		for _, result := range results.Results {
			if *result.State == model.GenericPolicyRealizedResource_STATE_REALIZED {
				for _, id := range extraIds {
					if *result.Id == id {
						extraIdsRealized++
					}
				}
				entitiesRealized++
				continue
			}
			if *result.State == model.GenericPolicyRealizedResource_STATE_ERROR {
				log.Error(nil, "Found realized state with error", "result", result)
				var errMsg []string
				for _, alarm := range result.Alarms {
					if alarm.Message != nil {
						errMsg = append(errMsg, *alarm.Message)
					}
					if nsxutil.IsRetryRealizeError(alarm) {
						return nsxutil.NewRetryRealizeError(fmt.Sprintf("%s not realized with errors: %s", intentPath, errMsg))
					}
				}
				return nsxutil.NewRealizeStateError(fmt.Sprintf("%s realized with errors: %s", intentPath, errMsg))
			}
		}
		// extraIdsRealized can be greater than extraIds length as id is not unique in result list.
		if len(results.Results) != 0 && entitiesRealized == len(results.Results) && extraIdsRealized >= len(extraIds) {
			return nil
		}
		return fmt.Errorf("%s not realized", intentPath)
	})
}
