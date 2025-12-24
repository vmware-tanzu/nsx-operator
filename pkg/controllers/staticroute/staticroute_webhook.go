/*
Copyright © 2025 VMware, Inc. All Rights Reserved.

	SPDX-License-Identifier: Apache-2.0
*/
package staticroute

import (
	"context"
	"fmt"
	"net/http"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Create validator instead of using the existing one in controller-runtime because the existing one can't
// inspect admission.Request in Handle function.

//+kubebuilder:webhook:path=/validate-crd-nsx-vmware-com-v1alpha1-staticroute,mutating=false,failurePolicy=fail,sideEffects=None,groups=crd.nsx.vmware.com,resources=staticroutes,verbs=create;update,versions=v1alpha1,name=staticroute.validating.crd.nsx.vmware.com,admissionReviewVersions=v1

type StaticRouteValidator struct {
	Client  client.Client
	decoder admission.Decoder
}

func (v *StaticRouteValidator) CheckNetworkStack(ctx context.Context, ns string) error {
	networkInfoList := &v1alpha1.NetworkInfoList{}
	err := v.Client.List(ctx, networkInfoList, client.InNamespace(ns))
	if err != nil {
		return fmt.Errorf("failed to list NetworkInfo in namespace %s: %v", ns, err)
	}
	if len(networkInfoList.Items) == 0 {
		return fmt.Errorf("no NetworkInfo found in namespace %s", ns)
	}
	for _, vpc := range networkInfoList.Items[0].VPCs {
		if vpc.NetworkStack == v1alpha1.VLANBackedVPC {
			log.Debug("Check network statck", "networkstack", vpc.NetworkStack)
			return fmt.Errorf("StaticRoute is not supported in VLANBackedVPC VPC")
		}
	}
	return nil
}

func (v *StaticRouteValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	sr := &v1alpha1.StaticRoute{}
	if req.Operation == admissionv1.Delete {
		return admission.Allowed("")
	} else {
		err := v.decoder.Decode(req, sr)
		if err != nil {
			log.Error(err, "error while decoding StaticRoute", "StaticRoute", req.Namespace+"/"+req.Name)
			return admission.Errored(http.StatusBadRequest, err)
		}
	}
	if req.Operation != admissionv1.Delete {
		if err := v.CheckNetworkStack(ctx, req.Namespace); err != nil {
			log.Error(err, "StaticRoute validation failed", "StaticRoute", req.Namespace+"/"+req.Name)
			return admission.Denied("StaticRoute is not supported in VLANBackedVPC VPC")
		}
	}
	return admission.Allowed("")
}
