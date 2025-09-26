/* Copyright © 2025 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"context"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	wcpSystemResource                   = "vmware-system-shared-t1"
	AnnotationSharedVPCNamespace string = "nsx.vmware.com/shared_vpc_namespace"
)

func IsSystemNamespace(c client.Client, ns string, obj *v1.Namespace, vpcMode bool) (bool, error) {
	// Only check VPC system namespace if VPC mode is enabled
	if vpcMode {
		isSysNs, err := IsVPCSystemNamespace(c, ns, obj)
		if err != nil {
			return false, err
		}
		if isSysNs {
			return true, nil
		}
	} else {
		// Only check T1 system namespace if VPC mode is disabled
		isSysNs, err := IsT1Namespace(c, ns, obj)
		if err != nil {
			return false, err
		}
		if isSysNs {
			return true, nil
		}
	}
	return false, nil
}

func IsT1Namespace(c client.Client, ns string, obj *v1.Namespace) (bool, error) {
	nsObj := &v1.Namespace{}
	if obj != nil {
		nsObj = obj
	} else if err := c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: ns}, nsObj); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	if isSysNs, ok := nsObj.Annotations[wcpSystemResource]; ok && strings.ToLower(isSysNs) == "true" {
		return true, nil
	}
	return false, nil
}

func IsVPCSystemNamespace(c client.Client, ns string, obj *v1.Namespace) (bool, error) {
	nsObj := &v1.Namespace{}
	if obj != nil {
		nsObj = obj
	} else if err := c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: ns}, nsObj); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	if sharedVPCNs, ok := nsObj.Annotations[AnnotationSharedVPCNamespace]; ok && sharedVPCNs == "kube-system" {
		return true, nil
	}
	return false, nil
}
