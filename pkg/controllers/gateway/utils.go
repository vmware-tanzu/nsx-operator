/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"context"
	"fmt"
	"reflect"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
)

// utils: list iteration, RouteObject wrappers (HTTP/GRPC/TLS), test status updater hook.

// allowedZonePathSet converts a zone-path→domain map to a set of zone paths.
func allowedZonePathSet(m map[string]string) sets.Set[string] {
	s := make(sets.Set[string], len(m))
	for k := range m {
		s.Insert(k)
	}
	return s
}

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
func loopObjectList[T any, PT Object[T]](
	ctx context.Context,
	c client.Client,
	list client.ObjectList,
	fn func(PT),
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
		el := items.Index(i).Addr().Interface().(PT)
		fn(el)
	}
	return nil
}

// RouteObject is the Route DNS subreconciler surface for HTTP/GRPC/TLS wrappers.
// These getters/setters abstract over the different Route types which do not share a common
// Go interface for their Spec.Hostnames, Spec.ParentRefs, and Status.Parents fields.
type RouteObject[O any] interface {
	client.Object
	GetObject() client.Object
	GetSpecHostnames() []gatewayv1.Hostname
	GetParentRefs() []gatewayv1.ParentReference
	GetResourceRef() *dns.ResourceRef
	GetRouteParentStatus() []gatewayv1.RouteParentStatus
	SetRouteParentStatus([]gatewayv1.RouteParentStatus)
	GetObjectMeta() *v1.ObjectMeta
}

type HTTPRoute struct{ gatewayv1.HTTPRoute }

func newHTTPRoute(v *gatewayv1.HTTPRoute) *HTTPRoute {
	if v == nil {
		return &HTTPRoute{}
	}
	return &HTTPRoute{HTTPRoute: *v}
}

func (r *HTTPRoute) GetObject() client.Object {
	return &r.HTTPRoute
}

func (r *HTTPRoute) GetObjectMeta() *v1.ObjectMeta {
	return &r.ObjectMeta
}

func (r *HTTPRoute) GetRouteParentStatus() []gatewayv1.RouteParentStatus {
	return r.Status.Parents
}

func (r *HTTPRoute) SetRouteParentStatus(parents []gatewayv1.RouteParentStatus) {
	r.Status.Parents = parents
}

func (r *HTTPRoute) GetSpecHostnames() []gatewayv1.Hostname {
	return r.Spec.Hostnames
}

func (r *HTTPRoute) GetParentRefs() []gatewayv1.ParentReference {
	return r.Spec.ParentRefs
}

func (r *HTTPRoute) GetResourceRef() *dns.ResourceRef {
	return &dns.ResourceRef{
		Kind:   dns.ResourceKindHTTPRoute,
		Object: r.GetObjectMeta(),
	}
}

type GRPCRoute struct{ gatewayv1.GRPCRoute }

func newGRPCRoute(v *gatewayv1.GRPCRoute) *GRPCRoute {
	if v == nil {
		return &GRPCRoute{}
	}
	return &GRPCRoute{GRPCRoute: *v}
}

func (r *GRPCRoute) GetObject() client.Object {
	return &r.GRPCRoute
}

func (r *GRPCRoute) GetSpecHostnames() []gatewayv1.Hostname {
	return r.Spec.Hostnames
}

func (r *GRPCRoute) GetParentRefs() []gatewayv1.ParentReference {
	return r.Spec.ParentRefs
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

func (r *GRPCRoute) SetRouteParentStatus(parents []gatewayv1.RouteParentStatus) {
	r.Status.Parents = parents
}

func (r *GRPCRoute) GetObjectMeta() *v1.ObjectMeta {
	return &r.ObjectMeta
}

type TLSRoute struct{ gatewayv1.TLSRoute }

func newTLSRoute(v *gatewayv1.TLSRoute) *TLSRoute {
	if v == nil {
		return &TLSRoute{}
	}
	return &TLSRoute{TLSRoute: *v}
}

func (r *TLSRoute) GetObject() client.Object {
	return &r.TLSRoute
}

func (r *TLSRoute) GetSpecHostnames() []gatewayv1.Hostname {
	return r.Spec.Hostnames
}

func (r *TLSRoute) GetParentRefs() []gatewayv1.ParentReference {
	return r.Spec.ParentRefs
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

func (r *TLSRoute) SetRouteParentStatus(parents []gatewayv1.RouteParentStatus) {
	r.Status.Parents = parents
}

func (r *TLSRoute) GetObjectMeta() *v1.ObjectMeta {
	return &r.ObjectMeta
}
