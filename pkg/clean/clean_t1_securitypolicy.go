/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package clean

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

// t1SecurityPolicyCleaner intentionally does not embed SecurityPolicyService.
// T1 security policies live under /infra and must not participate in VPC cleanup.
type t1SecurityPolicyCleaner struct {
	service *securitypolicy.SecurityPolicyService
}

func (cleaner *t1SecurityPolicyCleaner) CleanupInfraResources(ctx context.Context) error {
	var errs []error
	if err := ctx.Err(); err != nil {
		return errors.Join(nsxutil.TimeoutFailed, err)
	}

	for _, uid := range sets.List(cleaner.service.ListSecurityPolicyID()) {
		if err := ctx.Err(); err != nil {
			errs = append(errs, errors.Join(nsxutil.TimeoutFailed, err))
			break
		}

		if err := cleaner.service.DeleteSecurityPolicy(types.UID(uid), true, common.ResourceTypeSecurityPolicy); err != nil {
			errs = append(errs, fmt.Errorf("failed to clean up T1 SecurityPolicy %q: %w", uid, err))
		}
	}
	return errors.Join(errs...)
}
