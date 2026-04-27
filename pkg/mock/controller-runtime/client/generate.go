/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package mock_client

//go:generate go run github.com/golang/mock/mockgen@v1.6.0 -destination=mock_field_indexer.go -package=mock_client sigs.k8s.io/controller-runtime/pkg/client FieldIndexer
