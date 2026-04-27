/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
	extdnssrc "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/source"
)

// Route DNS: hostnames + ipCache → extdns.Endpoint; Gateway annotation DNS → owner-scoped batches (no ListenerSet annotation DNS).
func (r *GatewayReconciler) buildEndpointsFromAnnotations(obj client.Object, targets extdns.Targets, owner *dns.ResourceRef, gwNN types.NamespacedName) (*dns.AggregatedDNSEndponts, error) {
	parseHostnamesFromAnnotation := func(rawAnno map[string]string) []string {
		if rawAnno == nil {
			return nil
		}
		hostnames := rawAnno[servicecommon.AnnotationDNSHostnameKey]
		if strings.TrimSpace(hostnames) == "" {
			return nil
		}
		anno := map[string]string{
			servicecommon.AnnotationDNSHostnameSourceKey: extdnssrc.GatewayHostnameSourceAnnotationOnly,
			servicecommon.AnnotationDNSHostnameKey:       hostnames,
		}
		hosts := extdnssrc.RouteHostnames(&metav1.ObjectMeta{Annotations: anno}, nil,
			servicecommon.AnnotationDNSHostnameSourceKey, servicecommon.AnnotationDNSHostnameKey, false)
		return hosts
	}

	hosts := parseHostnamesFromAnnotation(obj.GetAnnotations())
	if len(hosts) == 0 {
		return nil, nil
	}
	var eps []*extdns.Endpoint
	buildEndpoints(&eps, hosts, targets, gwNN.String())
	if len(eps) == 0 {
		return nil, nil
	}
	endpointRows, err := r.Service.ValidateEndpointsByDNSZone(obj.GetNamespace(), owner, eps)
	if err != nil {
		return nil, err
	}
	return dns.NewOwnerScopedAggregatedRouteDNS(owner, endpointRows), nil
}

type parentGatewayMatch struct {
	nn     types.NamespacedName
	filter string
	ips    extdns.Targets
}

func mergeTargetsUnion(a, b extdns.Targets) extdns.Targets {
	return extdns.NewTargets(append(append([]string(nil), a...), b...)...)
}

// buildRouteDNSMergedEndpoints builds owner-scoped Route DNS rows (Gateway or ListenerSet→root Gateway, same rules as aggregation).
func (a routeReconcilerAdapter[PT, T]) buildRouteDNSMergedEndpoints(route PT) (*dns.AggregatedDNSEndponts, error) {
	owner := route.GetResourceRef()
	eps, err := a.reconciler.buildRouteDNSEndpointsForAggregation(a.dnsWriter, route.GetNamespace(), owner, route.GetParentRefs(), route.GetRouteParentStatus(), route.GetObjectMeta(), route.GetSpecHostnames())
	if err != nil || len(eps) == 0 {
		return nil, err
	}
	return dns.NewOwnerScopedAggregatedRouteDNS(owner, eps), nil
}

// buildEndpoints appends extdns endpoints per hostname; sets EndpointLabelParentGateway to gatewayLabel (comma-separated if merged).
func buildEndpoints(out *[]*extdns.Endpoint, hostnames []string, targets extdns.Targets, parentGatewayLabel string) {
	ttl := extdns.TTL(0)
	for _, h := range hostnames {
		if h == "" {
			continue
		}
		for _, ep := range extdns.EndpointsForHostname(h, targets, ttl) {
			if ep == nil {
				continue
			}
			if parentGatewayLabel != "" {
				ep.WithLabel(dns.EndpointLabelParentGateway, parentGatewayLabel)
			}
			*out = append(*out, ep)
		}
	}
}

// collectGatewayEndpointsByAnnotation builds Gateway owner DNS from hostname annotation + targets.
func (r *GatewayReconciler) collectGatewayEndpointsByAnnotation(gw *gatewayv1.Gateway, targets extdns.Targets) (*dns.AggregatedDNSEndponts, error) {
	owner := &dns.ResourceRef{Kind: dns.ResourceKindGateway, Object: gw.GetObjectMeta()}
	gwNN := types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}
	out, err := r.buildEndpointsFromAnnotations(gw, targets, owner, gwNN)
	if err != nil {
		log.Error(err, "Failed to build DNS Endpoints for Gateway annotations", "Gateway", gwNN.String())
		return nil, err
	}
	return out, nil
}

func filterDirectDNSEndpointsAgainstSeenHostnames(seen sets.Set[string], eps []dns.EndpointRow) []dns.EndpointRow {
	var out []dns.EndpointRow
	for _, ep := range eps {
		h := strings.TrimSpace(ep.DNSName)
		if h == "" {
			continue
		}
		if !extdnssrc.ClaimGwMatchingDNSName(seen, h) {
			continue
		}
		out = append(out, ep)
	}
	return out
}
