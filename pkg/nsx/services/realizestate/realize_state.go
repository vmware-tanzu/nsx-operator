/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package realizestate

import (
	"errors"
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type RealizeStateService struct {
	common.Service
}

type RealizeError struct {
}

func InitializeRealizeState(service common.Service) *RealizeStateService {
	return &RealizeStateService{
		Service: service,
	}
}

func IsRealizeStateError(err error) bool {
	return err.Error() == model.GenericPolicyRealizedResource_STATE_ERROR
}

// CheckRealizeState allows the caller to check realize status of entityType with retries.
// backoff defines the maximum retries and the wait interval between two retries.
func (service *RealizeStateService) CheckRealizeState(backoff wait.Backoff, intentPath, entityType string) error {
	vpcInfo, err := common.ParseVPCResourcePath(intentPath)
	if err != nil {
		return err
	}
	return retry.OnError(backoff, func(err error) bool {
		// Won't retry when realized state is `ERROR`.
		return !IsRealizeStateError(err)
	}, func() error {
		results, err := service.NSXClient.RealizedEntitiesClient.List(vpcInfo.OrgID, vpcInfo.ProjectID, intentPath, nil)
		if err != nil {
			return err
		}
		for _, result := range results.Results {
			if *result.EntityType != entityType {
				continue
			}
			if *result.State == model.GenericPolicyRealizedResource_STATE_REALIZED {
				return nil
			}
			return errors.New(*result.State)
		}
		return errors.New(fmt.Sprintf("%s not realized", entityType))
	})
}
