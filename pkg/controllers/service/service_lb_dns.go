/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package service

import (
	"context"
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
	extdnssrc "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/source"
)

const (
	// serviceDNSReadyConditionType is the Service status condition type for LoadBalancer DNS publish state (mirrors Gateway DNSConfig semantics, name per product request).
	serviceDNSReadyConditionType     = "Ready"
	reasonServiceDNSRecordConfigured = "DNSRecordConfigured"
	reasonServiceDNSRecordFailed     = "DNSRecordFailed"
)

// parseDNSHostnamesFromServiceAnnotation returns trimmed unique FQDNs from nsx.vmware.com/hostname using the same
// gateway-hostname-source + hostname annotation path as Gateway direct DNS (comma-separated tokens are supported).
func parseDNSHostnamesFromServiceAnnotation(annotations map[string]string) []string {
	if annotations == nil {
		return nil
	}
	raw := annotations[servicecommon.AnnotationDNSHostnameKey]
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	anno := map[string]string{
		servicecommon.AnnotationDNSHostnameSourceKey: extdnssrc.GatewayHostnameSourceAnnotationOnly,
		servicecommon.AnnotationDNSHostnameKey:       raw,
	}
	hosts := extdnssrc.RouteHostnames(&metav1.ObjectMeta{Annotations: anno}, nil,
		servicecommon.AnnotationDNSHostnameSourceKey, servicecommon.AnnotationDNSHostnameKey, false)
	seen := sets.New[string]()
	var out []string
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" || seen.Has(h) {
			continue
		}
		seen.Insert(h)
		out = append(out, h)
	}
	return out
}

// targetsFromLoadBalancerIngress collects IP and Hostname values from Service.Status.LoadBalancer.Ingress.
func targetsFromLoadBalancerIngress(ingress []v1.LoadBalancerIngress) extdns.Targets {
	var vals []string
	for i := range ingress {
		ing := ingress[i]
		if ip := strings.TrimSpace(ing.IP); ip != "" {
			vals = append(vals, ip)
		}
		if hn := strings.TrimSpace(ing.Hostname); hn != "" {
			vals = append(vals, hn)
		}
	}
	return extdns.NewTargets(vals...)
}

func serviceDNSParentLabel(svc *v1.Service) string {
	return fmt.Sprintf("%s/%s", svc.Namespace, svc.Name)
}

// buildLoadBalancerServiceDNSBatch builds owner-scoped DNS rows for a LoadBalancer Service: annotation hostnames,
// targets from LB ingress, parent label for store tagging, then ValidateEndpointsByDNSZone for namespace VPC policy.
func buildLoadBalancerServiceDNSBatch(svc *v1.Service, w dns.RouteDNSWrite) (*dns.AggregatedDNSEndponts, error) {
	hostnames := parseDNSHostnamesFromServiceAnnotation(svc.GetAnnotations())
	if len(hostnames) == 0 {
		return nil, nil
	}
	targets := targetsFromLoadBalancerIngress(svc.Status.LoadBalancer.Ingress)
	if len(targets) == 0 {
		return nil, nil
	}
	ttl := extdns.TTL(0)
	parent := serviceDNSParentLabel(svc)
	var eps []*extdns.Endpoint
	for _, h := range hostnames {
		for _, ep := range extdns.EndpointsForHostname(h, targets, ttl) {
			if ep == nil {
				continue
			}
			ep.WithLabel(dns.EndpointLabelParentGateway, parent)
			eps = append(eps, ep)
		}
	}
	if len(eps) == 0 {
		return nil, nil
	}
	owner := &dns.ResourceRef{Kind: dns.ResourceKindService, Object: svc.GetObjectMeta()}
	rows, err := w.ValidateEndpointsByDNSZone(svc.Namespace, owner, eps)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return dns.NewOwnerScopedAggregatedRouteDNS(owner, rows), nil
}

func buildServiceDNSReadyCondition(err error) metav1.Condition {
	cond := metav1.Condition{
		Type:               serviceDNSReadyConditionType,
		LastTransitionTime: metav1.Now(),
	}
	if err != nil {
		cond.Status = metav1.ConditionFalse
		cond.Reason = reasonServiceDNSRecordFailed
		cond.Message = err.Error()
	} else {
		cond.Status = metav1.ConditionTrue
		cond.Reason = reasonServiceDNSRecordConfigured
	}
	return cond
}

// updateServiceDNSReadyCondition sets Service status condition type Ready from DNS reconcile outcome (True when err is nil).
func (r *ServiceLbReconciler) updateServiceDNSReadyCondition(ctx context.Context, ownerKey types.NamespacedName, err error) error {
	cond := buildServiceDNSReadyCondition(err)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		svc := &v1.Service{}
		if getErr := r.Client.Get(ctx, ownerKey, svc); getErr != nil {
			return client.IgnoreNotFound(getErr)
		}
		c := cond
		c.ObservedGeneration = svc.GetGeneration()
		if !meta.SetStatusCondition(&svc.Status.Conditions, c) {
			return nil
		}
		return r.Client.Status().Update(ctx, svc)
	})
}

// removeServiceDNSReadyCondition removes the Ready DNS status condition; ignores NotFound.
func (r *ServiceLbReconciler) removeServiceDNSReadyCondition(ctx context.Context, key types.NamespacedName) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		svc := &v1.Service{}
		if err := r.Client.Get(ctx, key, svc); err != nil {
			return client.IgnoreNotFound(err)
		}
		if !meta.RemoveStatusCondition(&svc.Status.Conditions, serviceDNSReadyConditionType) {
			return nil
		}
		return r.Client.Status().Update(ctx, svc)
	})
}

// reconcileLoadBalancerServiceDNS applies DNS rows for the Service. published is true when DNS records were created or updated (not delete-only).
func (r *ServiceLbReconciler) reconcileLoadBalancerServiceDNS(ctx context.Context, svc *v1.Service) (published bool, err error) {
	batch, err := buildLoadBalancerServiceDNSBatch(svc, r.DNSWriter)
	if err != nil {
		return false, err
	}
	if batch == nil || len(batch.Rows) == 0 {
		_, err = r.DNSWriter.DeleteDNSRecordByOwnerNN(ctx, dns.ResourceKindService, svc.Namespace, svc.Name)
		return false, err
	}
	_, err = r.DNSWriter.CreateOrUpdateDNSRecords(ctx, batch)
	if err != nil {
		return false, err
	}
	return true, nil
}
