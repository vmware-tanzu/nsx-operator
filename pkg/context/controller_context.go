/* Copyright © 2021 VMware, Inc. All Rights Reserved.

   SPDX-License-Identifier: Apache-2.0 */

package context

type ControllerContext struct {
	*ClusterContext

	// Name of the controller
	Name string

	// Key of the resource being reconciled
	ResKey string

	// NameSpace of the resource being reconciled
	// Empty when reconcile delete or garbage collection
	ResNamespace string

	// Name of the resource being reconciled
	// Empty when reconcile delete or garbage collection
	ResName string

	// Operation type: C -> Create, U -> Update, R -> Retry, D -> Delete, G -> GC
	Verb string
}
