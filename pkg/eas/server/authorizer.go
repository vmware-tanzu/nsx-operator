/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package server

import (
	"context"

	"k8s.io/apiserver/pkg/authorization/authorizer"
)

// easAuthorizer is the authorization backend for the EAS server.
//
// # Authorization model
//
// EAS exposes read-only resources (VPCIPAddressUsage, IPBlockUsage,
// SubnetIPPools, SubnetDHCPServerStats).  All write paths are absent by design.
//
// In the Kubernetes aggregated-API-server model, every request reaches the EAS
// server only after the kube-apiserver has already:
//  1. Authenticated the caller (TokenReview / bearer-token validation).
//  2. Checked the caller's RBAC permissions against the nsx-eas-reader
//     ClusterRole (or equivalent).
//
// Delegating authorization back to kube-apiserver via SubjectAccessReview
// (the standard webhook.go path) would require the EAS service account to hold
// "create subjectaccessreviews" — a privilege that is not available in all
// WCP/Tanzu environments.  Since the upstream proxy has already performed an
// equivalent check, a second SAR round-trip would be redundant.
//
// This authorizer therefore allows every request that arrives here, trusting
// that the kube-apiserver aggregation layer has already enforced access control.
type easAuthorizer struct{}

// Authorize always returns DecisionAllow.  The upstream kube-apiserver
// aggregation layer is the authoritative enforcement point for EAS resources.
func (easAuthorizer) Authorize(_ context.Context, _ authorizer.Attributes) (authorizer.Decision, string, error) {
	return authorizer.DecisionAllow, "", nil
}
