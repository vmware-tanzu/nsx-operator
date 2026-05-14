/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package mockgateway holds generated mocks for gateway controller test interfaces.
//
//go:generate go run github.com/golang/mock/mockgen@v1.6.0 -destination=mock_status_updater.go -package=mockgateway github.com/vmware-tanzu/nsx-operator/pkg/controllers/gateway StatusUpdater
package mockgateway
