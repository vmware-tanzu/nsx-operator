/* Copyright (c) 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	extannotations "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/annotations"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
)

// parseDNSHostnamesFromAnnotation returns trimmed unique FQDNs from nsx.vmware.com/hostname using the same
// gateway-hostname-source + hostname annotation path as Gateway direct DNS (comma-separated tokens are supported).
func parseDNSHostnamesFromAnnotation(annotations map[string]string) []string {
	if len(annotations) == 0 {
		return nil
	}

	if _, ok := annotations[servicecommon.AnnotationsDNSSkip]; ok {
		return nil
	}
	return extannotations.HostnamesFromAnnotations(annotations, servicecommon.AnnotationDNSHostnameKey)
}

// buildGatewayDNSBatch builds owner-scoped DNS rows for a Gateway: annotation hostnames,
// targets from Gateway IPs, then ValidateEndpointsByZone for namespace VPC policy.
func buildGatewayDNSBatch(gw *gatewayv1.Gateway, w dns.DNSRecordProvider) (*dns.AggregatedDNSEndpoints, error) {
	hostnames := parseDNSHostnamesFromAnnotation(gw.GetAnnotations())
	if len(hostnames) == 0 {
		return nil, nil
	}
	ips := collectIPsFromGateway(gw)
	targets := ipsToTargets(ips)
	if len(targets) == 0 {
		log.Debug("Gateway has hostname annotation but no usable IP targets yet", "namespace", gw.Namespace, "name", gw.Name)
		return nil, nil
	}
	log.Debug("Building DNS batch for Gateway", "namespace", gw.Namespace, "name", gw.Name,
		"hostnames", len(hostnames), "targets", len(targets))
	ttl := extdns.TTL(0)
	var eps []*extdns.Endpoint
	for _, h := range hostnames {
		eps = append(eps, extdns.EndpointsForHostname(h, targets, ttl)...)
	}
	if len(eps) == 0 {
		return nil, nil
	}
	owner := &dns.ResourceRef{Kind: dns.ResourceKindGateway, Object: gw.GetObjectMeta()}
	rows, _, err := w.ValidateEndpointsByZone(gw.Namespace, owner, eps)

	if len(rows) == 0 {
		return nil, err
	}
	log.Info("DNS batch built for Gateway", "namespace", gw.Namespace, "name", gw.Name, "rows", len(rows))
	return dns.NewOwnerScopedAggregatedRouteDNS(owner, rows), err
}

// reconcileGatewayDNS applies DNS rows for the Gateway.
func (r *GatewayReconciler) reconcileGatewayDNS(ctx context.Context, gw *gatewayv1.Gateway) error {
	gwNN := types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}
	log.Info("Reconciling DNS for Gateway", "Gateway", gwNN)
	batch, err := buildGatewayDNSBatch(gw, r.DNS)
	if err != nil {
		var zoneValErr *dns.DNSZoneValidationError
		if errors.As(err, &zoneValErr) {
			log.Error(err, "Failed to validate DNS records for Gateway with the allowed DNS zones", "Gateway", gwNN)
			if uerr := r.updateGatewayDNSReadyCondition(ctx, gwNN, err); uerr != nil {
				log.Error(uerr, "Failed to update DNS conditions", "Gateway", gwNN)
				return uerr
			}
			// If there are valid rows, we should still apply them
			if batch != nil && len(batch.Rows) > 0 {
				_, uErr := r.DNS.CreateOrUpdateRecords(ctx, batch)
				if uErr != nil {
					log.Error(uErr, "Failed to reconcile valid DNS records despite validation errors", "Gateway", gwNN)
					return uErr
				}
			} else {
				if _, dErr := r.DNS.DeleteRecordByOwnerNN(ctx, dns.ResourceKindGateway, gw.Namespace, gw.Name); dErr != nil {
					log.Error(dErr, "Failed to delete stale DNS records", "Gateway", gwNN)
					return dErr
				}
			}
			// For validation errors, we do not automatically requeue. We wait for the user to update the gateway.
			return nil
		}
		log.Error(err, "Failed to build DNS endpoints for Gateway", "Gateway", gwNN)
		if uerr := r.updateGatewayDNSReadyCondition(ctx, gwNN, err); uerr != nil {
			log.Error(uerr, "Failed to update DNS conditions", "Gateway", gwNN)
			return uerr
		}
		return err
	}

	if batch == nil || len(batch.Rows) == 0 {
		return r.clearDNSAndConditionForGateway(ctx, gwNN, "stale DNS records")
	}

	_, uErr := r.DNS.CreateOrUpdateRecords(ctx, batch)
	if uErr != nil {
		log.Error(uErr, "Failed to reconcile DNS records", "Gateway", gwNN)
	}
	if condErr := r.updateGatewayDNSReadyCondition(ctx, gwNN, uErr); condErr != nil {
		log.Error(condErr, "Failed to update DNS ready condition", "Gateway", gwNN)
		if uErr != nil {
			return fmt.Errorf("updating condition: %v, reconciling DNS: %w", condErr, uErr)
		}
		return fmt.Errorf("updating condition: %w", condErr)
	}
	return uErr
}

func (r *GatewayReconciler) clearDNSAndConditionForGateway(ctx context.Context, reqNN types.NamespacedName, op string) error {
	if _, err := r.DNS.DeleteRecordByOwnerNN(ctx, dns.ResourceKindGateway, reqNN.Namespace, reqNN.Name); err != nil {
		log.Error(err, "Failed to delete DNS records for Gateway", "Namespace", reqNN.Namespace, "Name", reqNN.Name, "Operation", op)
		return fmt.Errorf("deleting DNS records for %s: %w", op, err)
	}
	if uerr := r.removeGatewayDNSConfigCondition(ctx, reqNN); uerr != nil {
		log.Error(uerr, "Failed to clear Gateway DNS Ready condition", "Gateway", reqNN.String(), "Operation", op)
		return fmt.Errorf("clearing DNS condition for %s: %w", op, uerr)
	}
	return nil
}
