/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package realizestate

import (
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

type RealizeStateService struct {
	common.Service
}

type RealizeStateError struct {
	message string
}

func (e *RealizeStateError) Error() string {
	return e.message
}

func NewRealizeStateError(msg string) *RealizeStateError {
	return &RealizeStateError{message: msg}
}

func InitializeRealizeState(service common.Service) *RealizeStateService {
	return &RealizeStateService{
		Service: service,
	}
}

func IsRealizeStateError(err error) bool {
	_, ok := err.(*RealizeStateError)
	return ok
}

// CheckRealizeState allows the caller to check realize status of intentPath with retries.
// Backoff defines the maximum retries and the wait interval between two retries.
// Check all the entities, all entities should be in the REALIZED state to be treated as REALIZED
func (service *RealizeStateService) CheckRealizeState(backoff wait.Backoff, intentPath string) error {
	// TODO， ask NSX if there were multiple realize states could we check only the latest one?
	return retry.OnError(backoff, func(err error) bool {
		// Won't retry when realized state is `ERROR`.
		return !IsRealizeStateError(err)
	}, func() error {
		results, err := service.NSXClient.RealizedEntitiesClient.List(intentPath, nil)
		err = nsxutil.TransNSXApiError(err)
		if err != nil {
			return err
		}
		entitiesRealized := 0
		for _, result := range results.Results {
			if *result.State == model.GenericPolicyRealizedResource_STATE_REALIZED {
				entitiesRealized++
				continue
			}
			if *result.State == model.GenericPolicyRealizedResource_STATE_ERROR {
				var errMsg []string
				for _, alarm := range result.Alarms {
					if alarm.Message != nil {
						errMsg = append(errMsg, *alarm.Message)
					}
				}
				return NewRealizeStateError(fmt.Sprintf("%s realized with errors: %s", intentPath, errMsg))
			}
		}
		if len(results.Results) != 0 && entitiesRealized == len(results.Results) {
			return nil
		}
		return fmt.Errorf("%s not realized", intentPath)
	})
}
