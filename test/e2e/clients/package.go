// Copyright © 2019-2021 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

// Package clients provides client implementations for interacting with external services
// like NSX and vCenter in e2e tests.
package clients

import (
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
)

var log = &logger.Log