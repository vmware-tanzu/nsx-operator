/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package mock_manager holds generated mocks for controller-runtime manager.Manager.
//
//go:generate go run github.com/golang/mock/mockgen@v1.6.0 -destination=mock_manager.go -package=mock_manager sigs.k8s.io/controller-runtime/pkg/manager Manager
package mock_manager
