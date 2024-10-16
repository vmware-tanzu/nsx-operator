/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package main

import (
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log = logger.Log
)

func main() {
	log.Info("Generating webhook certificates...")
	if err := util.GenerateWebhookCerts(); err != nil {
		panic(err)
	}
}
