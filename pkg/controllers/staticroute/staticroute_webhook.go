/*
Copyright © 2026 VMware, Inc. All Rights Reserved.

	SPDX-License-Identifier: Apache-2.0
*/
package staticroute

import (
	"context"
	"errors"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
)

// Create validator instead of using the existing one in controller-runtime because the existing one can't
// inspect admission.Request in Handle function.

//+kubebuilder:webhook:path=/validate-crd-nsx-vmware-com-v1alpha1-staticroute,mutating=false,failurePolicy=fail,sideEffects=None,groups=crd.nsx.vmware.com,resources=staticroutes,verbs=create;update,versions=v1alpha1,name=staticroute.validating.crd.nsx.vmware.com,admissionReviewVersions=v1

type StaticRouteValidator struct {
	Client  client.Client
	decoder admission.Decoder
}

func (v *StaticRouteValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	sr := &v1alpha1.StaticRoute{}
	if req.Operation == admissionv1.Delete {
		return admission.Allowed("")
	}

	err := v.decoder.Decode(req, sr)
	if err != nil {
		log.Error(err, "error while decoding StaticRoute", "StaticRoute", req.Namespace+"/"+req.Name)
		return admission.Errored(http.StatusBadRequest, err)
	}

	if err := common.CheckNetworkStack(v.Client, ctx, req.Namespace, "StaticRoute"); err != nil {
		log.Error(err, "StaticRoute validation failed", "StaticRoute", req.Namespace+"/"+req.Name)

		if errors.Is(err, common.ErrFailedToListNetworkInfo) {
			return admission.Errored(http.StatusServiceUnavailable, err)
		}
		return admission.Denied(err.Error())
	}

	return admission.Allowed("")
}
