/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"context"
	"fmt"
	"reflect"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	extdnssrc "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/source"
)

// utils: list iteration, RouteObject wrappers (HTTP/GRPC/TLS), test status updater hook.

type Object[O any] interface {
	client.Object
	*O
}

// StatusUpdater is the status/metrics surface used by GatewayReconciler (tests use mocks or spies).
type StatusUpdater interface {
	UpdateSuccess(ctx context.Context, obj client.Object, setStatusFn common.UpdateSuccessStatusFn, args ...interface{})
	UpdateFail(ctx context.Context, obj client.Object, err error, msg string, setStatusFn common.UpdateFailStatusFn, args ...interface{})
	DeleteSuccess(namespacedName types.NamespacedName, obj client.Object)
	IncreaseSyncTotal()
	IncreaseUpdateTotal()
	IncreaseDeleteTotal()
	IncreaseDeleteSuccessTotal()
	IncreaseDeleteFailTotal()
	DeleteFail(namespacedName types.NamespacedName, obj client.Object, err error)
}

// loopObjectList runs c.List into list then calls fn for each Items element.
func loopObjectList[T any](
	ctx context.Context,
	c client.Client,
	list client.ObjectList,
	fn func(*T),
	opts ...client.ListOption,
) error {
	if err := c.List(ctx, list, opts...); err != nil {
		return err
	}
	rv := reflect.ValueOf(list)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("loopObjectList: list must be non-nil pointer, got %T", list)
	}
	items := rv.Elem().FieldByName("Items")
	if !items.IsValid() || items.Kind() != reflect.Slice {
		return fmt.Errorf("loopObjectList: %T has no Items slice field", list)
	}
	for i := 0; i < items.Len(); i++ {
		el := items.Index(i).Addr().Interface().(*T)
		fn(el)
	}
	return nil
}

// RouteObject is the Route DNS subreconciler surface for HTTP/GRPC/TLS wrappers.
type RouteObject[O any] interface {
	client.Object
	GetBaseObject() client.Object
	GetSpecHostnames() []gatewayv1.Hostname
	GetParentRefs() []gatewayv1.ParentReference
	IsParentReady(types.NamespacedName) bool
	GetResourceRef() *dns.ResourceRef
	GetRouteParentStatus() []gatewayv1.RouteParentStatus
	GetObjectMeta() *v1.ObjectMeta
}

type HTTPRoute struct{ gatewayv1.HTTPRoute }

func (r *HTTPRoute) GetBaseObject() client.Object {
	return &r.HTTPRoute
}

func (r *HTTPRoute) GetObjectMeta() *v1.ObjectMeta {
	return &r.ObjectMeta
}

func (r *HTTPRoute) GetRouteParentStatus() []gatewayv1.RouteParentStatus {
	return r.Status.Parents
}

func (r *HTTPRoute) GetSpecHostnames() []gatewayv1.Hostname {
	return r.Spec.Hostnames
}

func (r *HTTPRoute) GetParentRefs() []gatewayv1.ParentReference {
	return r.Spec.ParentRefs
}

func (r *HTTPRoute) IsParentReady(nn types.NamespacedName) bool {
	return extdnssrc.HTTPRouteParentReadyForGateway(&r.HTTPRoute, nn)
}

func (r *HTTPRoute) GetResourceRef() *dns.ResourceRef {
	return &dns.ResourceRef{
		Kind:   dns.ResourceKindHTTPRoute,
		Object: r.GetObjectMeta(),
	}
}

type GRPCRoute struct{ gatewayv1.GRPCRoute }

func (r *GRPCRoute) GetBaseObject() client.Object {
	return &r.GRPCRoute
}

func (r *GRPCRoute) GetSpecHostnames() []gatewayv1.Hostname {
	return r.Spec.Hostnames
}

func (r *GRPCRoute) GetParentRefs() []gatewayv1.ParentReference {
	return r.Spec.ParentRefs
}

func (r *GRPCRoute) IsParentReady(nn types.NamespacedName) bool {
	return extdnssrc.GRPCRouteParentReadyForGateway(&r.GRPCRoute, nn)
}

func (r *GRPCRoute) GetResourceRef() *dns.ResourceRef {
	return &dns.ResourceRef{
		Kind:   dns.ResourceKindGRPCRoute,
		Object: r.GetObjectMeta(),
	}
}

func (r *GRPCRoute) GetRouteParentStatus() []gatewayv1.RouteParentStatus {
	return r.Status.Parents
}

func (r *GRPCRoute) GetObjectMeta() *v1.ObjectMeta {
	return &r.ObjectMeta
}

type TLSRoute struct{ gatewayv1.TLSRoute }

func (r *TLSRoute) GetBaseObject() client.Object {
	return &r.TLSRoute
}

func (r *TLSRoute) GetSpecHostnames() []gatewayv1.Hostname {
	return r.Spec.Hostnames
}

func (r *TLSRoute) GetParentRefs() []gatewayv1.ParentReference {
	return r.Spec.ParentRefs
}

func (r *TLSRoute) IsParentReady(nn types.NamespacedName) bool {
	return extdnssrc.TLSRouteParentReadyForGateway(&r.TLSRoute, nn)
}

func (r *TLSRoute) GetResourceRef() *dns.ResourceRef {
	return &dns.ResourceRef{
		Kind:   dns.ResourceKindTLSRoute,
		Object: r.GetObjectMeta(),
	}
}

func (r *TLSRoute) GetRouteParentStatus() []gatewayv1.RouteParentStatus {
	return r.Status.Parents
}

func (r *TLSRoute) GetObjectMeta() *v1.ObjectMeta {
	return &r.ObjectMeta
}
