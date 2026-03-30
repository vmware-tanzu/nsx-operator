/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"context"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
)

var listenerSetToGateway = handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
	parentGateway := findParentGatewayFromListenerSet(obj)
	if parentGateway == nil {
		return nil
	}
	// Check if the parent Gateway satisfies the watch conditions

	return []reconcile.Request{{NamespacedName: *parentGateway}}
})

func findParentGatewayFromListenerSet(obj client.Object) *types.NamespacedName {
	ls, ok := obj.(*gatewayv1.ListenerSet)
	if !ok || ls == nil {
		return nil
	}

	parent := ls.Spec.ParentRef
	// Check if the ListenerSet is referring to a Gateway
	if parent.Kind != nil && string(*parent.Kind) != dns.ResourceKindGateway {
		return nil
	}
	if parent.Group != nil && string(*parent.Group) != gatewayv1.GroupName {
		return nil
	}
	// Ignore the ListenerSet if it does not refer to any K8s Gateway resource.
	// This is for security purpose.
	if parent.Name == "" {
		return nil
	}

	// Parse the parent Gateway's namespace. If the Namespace is not specified in the parent reference,
	// use the ListenerSet's namespace.
	gwNS := ls.Namespace
	if parent.Namespace != nil && string(*parent.Namespace) != "" {
		gwNS = string(*parent.Namespace)
	}

	return &types.NamespacedName{Namespace: gwNS, Name: string(parent.Name)}
}

func collectHostnamesFromListenerSet(ls gatewayv1.ListenerSet) []string {
	var hostnames []string
	for _, l := range ls.Spec.Listeners {
		if l.Hostname != nil {
			h := strings.TrimSpace(string(*l.Hostname))
			if h != "" {
				hostnames = append(hostnames, h)
			}
		}
	}
	return hostnames
}

func listenerSetParentGatewayIndexFunc(obj client.Object) []string {
	parentGateway := findParentGatewayFromListenerSet(obj)
	if parentGateway == nil {
		return []string{}
	}
	return []string{parentGateway.String()}
}
