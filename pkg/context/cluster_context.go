/* Copyright © 2021 VMware, Inc. All Rights Reserved.

   SPDX-License-Identifier: Apache-2.0 */

package context

import (
	"context"
	"github.com/nsx-operator/pkg/util"
)

type ClusterContext struct {
	context.Context

	// NSX operator config struct
	Config *util.NSXOperatorConfig

	// NSX operator cluster name
	ClusterName string

	// NSX operator cluster uuid
	ClusterID string

	// NSX operator version
	Version string
}
