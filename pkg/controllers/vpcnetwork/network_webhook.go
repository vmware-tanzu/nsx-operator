/* Copyright © 2024 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package vpcnetwork

import (
	"context"
	"fmt"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func (r *NetworkReconciler) Handle(ctx context.Context, req admission.Request) admission.Response {
	objKind := req.Kind.Kind
	switch objKind {
	case "IPPool":
		fallthrough
	case "NetworkInfo":
		fallthrough
	case "NSXServiceAccount":
		fallthrough
	case "SecurityPolicy":
		fallthrough
	case "StaticRoute":
		fallthrough
	case "SubnetPort":
		fallthrough
	case "Subnet":
		fallthrough
	case "SubnetSet":
		ns := req.Namespace
		enabled, err := r.IsVPCEnabledOnNamespace(ns)
		if err != nil {
			log.Error(err, "failed to check if VPC is enabled when validating CR creation", "Namespace", ns, objKind, req.Namespace+"/"+req.Name)
			returnedErr := fmt.Errorf("unable to check the default network type in Namespace %s: %v", ns, err)
			return admission.Errored(http.StatusBadRequest, returnedErr)
		}
		if !enabled {
			log.Info("VPC is not enabled in Namespace, reject CR", "Namespace", ns, objKind, req.Namespace+"/"+req.Name)
			return admission.Errored(http.StatusBadRequest, fmt.Errorf("VPC is not enabled in Namespace %s", ns))
		}
		return admission.Allowed("")
	default:
		log.Info("Unsupported kind in the validation, allow by default", "kind", objKind)
		return admission.Allowed("")
	}
}
