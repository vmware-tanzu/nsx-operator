/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	extannotations "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/annotations"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
)

const (
	// serviceDNSReadyConditionType is the Service status condition type for LoadBalancer DNS publish state (mirrors Gateway DNSConfig semantics, name per product request).
	serviceDNSReadyConditionType     = "Ready"
	reasonServiceDNSRecordConfigured = "DNSRecordConfigured"
	reasonServiceDNSRecordFailed     = "DNSRecordFailed"
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

// targetsFromLoadBalancerIngress collects IP and Hostname values from Service.Status.LoadBalancer.Ingress.
func targetsFromLoadBalancerIngress(ingress []v1.LoadBalancerIngress) extdns.Targets {
	vals := make([]string, 0, len(ingress)*2)
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

// buildLoadBalancerServiceDNSBatch builds owner-scoped DNS rows for a LoadBalancer Service: annotation hostnames,
// targets from LB ingress, then ValidateEndpointsByZone for namespace VPC policy.
func buildLoadBalancerServiceDNSBatch(svc *v1.Service, w dns.DNSRecordProvider) (*dns.AggregatedDNSEndpoints, error) {
	hostnames := parseDNSHostnamesFromAnnotation(svc.GetAnnotations())
	if len(hostnames) == 0 {
		return nil, nil
	}
	targets := targetsFromLoadBalancerIngress(svc.Status.LoadBalancer.Ingress)
	if len(targets) == 0 {
		log.Debug("LB service has hostname annotation but no ingress targets yet", "namespace", svc.Namespace, "name", svc.Name)
		return nil, nil
	}
	log.Debug("Building DNS batch for LB service", "namespace", svc.Namespace, "name", svc.Name,
		"hostnames", len(hostnames), "targets", len(targets))
	ttl := extdns.TTL(0)
	var eps []*extdns.Endpoint
	for _, h := range hostnames {
		for _, ep := range extdns.EndpointsForHostname(h, targets, ttl) {
			if ep == nil {
				log.Info("Skipping invalid DNS hostname", "hostname", h, "namespace", svc.Namespace, "name", svc.Name)
				continue
			}
			eps = append(eps, ep)
		}
	}
	if len(eps) == 0 {
		return nil, nil
	}
	owner := &dns.ResourceRef{Kind: dns.ResourceKindService, Object: svc.GetObjectMeta()}
	rows, _, err := w.ValidateEndpointsByZone(svc.Namespace, owner, eps)

	if len(rows) == 0 {
		return nil, err
	}
	log.Info("DNS batch built for LB service", "namespace", svc.Namespace, "name", svc.Name, "rows", len(rows))
	return dns.NewOwnerScopedAggregatedRouteDNS(owner, rows), err
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

// reconcileLoadBalancerServiceDNS applies DNS rows for the Service.
func (r *ServiceLbReconciler) reconcileLoadBalancerServiceDNS(ctx context.Context, svc *v1.Service) error {
	svcNN := types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name}
	log.Info("Reconciling DNS for LB service", "Service", svcNN)
	batch, err := buildLoadBalancerServiceDNSBatch(svc, r.DNS)
	if err != nil {
		var zoneValErr *dns.DNSZoneValidationError
		if errors.As(err, &zoneValErr) {
			log.Error(err, "Failed to validate DNS records for Service with the allowed DNS zones", "Service", svcNN)
			if uerr := r.updateServiceDNSReadyCondition(ctx, svcNN, err); uerr != nil {
				log.Error(uerr, "Failed to update DNS conditions", "Service", svcNN)
				return uerr
			}
			// If there are valid rows, we should still apply them
			if batch != nil && len(batch.Rows) > 0 {
				_, uErr := r.DNS.CreateOrUpdateRecords(ctx, batch)
				if uErr != nil {
					log.Error(uErr, "Failed to reconcile valid DNS records despite validation errors", "Service", svcNN)
					return uErr
				}
			} else {
				if _, dErr := r.DNS.DeleteRecordByOwnerNN(ctx, dns.ResourceKindService, svc.Namespace, svc.Name); dErr != nil {
					log.Error(dErr, "Failed to delete stale DNS records", "Service", svcNN)
					return dErr
				}
			}
			// For validation errors, we do not automatically requeue. We wait for the user to update the service.
			return nil
		}
		log.Error(err, "Failed to build DNS endpoints for Service", "Service", svcNN)
		if uerr := r.updateServiceDNSReadyCondition(ctx, svcNN, err); uerr != nil {
			log.Error(uerr, "Failed to update DNS conditions", "Service", svcNN)
			return uerr
		}
		return err
	}

	if batch == nil || len(batch.Rows) == 0 {
		return r.clearDNSAndConditionForService(ctx, svcNN, "stale DNS records")
	}

	_, uErr := r.DNS.CreateOrUpdateRecords(ctx, batch)
	if uErr != nil {
		log.Error(uErr, "Failed to reconcile DNS records", "Service", svcNN)
	}
	if condErr := r.updateServiceDNSReadyCondition(ctx, svcNN, uErr); condErr != nil {
		log.Error(condErr, "Failed to update DNS ready condition", "Service", svcNN)
		if uErr != nil {
			return fmt.Errorf("updating condition: %v, reconciling DNS: %w", condErr, uErr)
		}
		return fmt.Errorf("updating condition: %w", condErr)
	}
	return uErr
}

// getLoadBalancerServicesWithDNS returns LoadBalancer Services that should have DNS records.
func getLoadBalancerServicesWithDNS(ctx context.Context, c client.Client, listOpts ...client.ListOption) ([]v1.Service, error) {
	svcList := &v1.ServiceList{}
	if err := c.List(ctx, svcList, listOpts...); err != nil {
		return nil, err
	}
	var filtered []v1.Service
	for i := range svcList.Items {
		svc := svcList.Items[i]
		if svc.Spec.Type != v1.ServiceTypeLoadBalancer || !svc.ObjectMeta.DeletionTimestamp.IsZero() {
			continue
		}
		if len(parseDNSHostnamesFromAnnotation(svc.GetAnnotations())) == 0 {
			continue
		}
		filtered = append(filtered, svc)
	}
	return filtered, nil
}

// enqueueLBServiceRequestsFromNetworkInfo requeues LoadBalancer Services that publish DNS when namespace AllowedDNSDomains change.
func (r *ServiceLbReconciler) enqueueLBServiceRequestsFromNetworkInfo(ctx context.Context, obj client.Object) []reconcile.Request {
	ni, ok := obj.(*v1alpha1.NetworkInfo)
	if !ok || ni == nil {
		return nil
	}
	svcs, err := getLoadBalancerServicesWithDNS(ctx, r.Client, client.InNamespace(ni.Namespace))
	if err != nil {
		log.Error(err, "Failed to list Services for NetworkInfo DNS domain change", "Namespace", ni.Namespace)
		return nil
	}
	var reqs []reconcile.Request
	for _, svc := range svcs {
		reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name}})
	}
	return reqs
}

func predicateNetworkInfoAllowedDNSDomainsChanged() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(event.CreateEvent) bool {
			return false
		},
		DeleteFunc: func(event.DeleteEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNI, ok1 := e.ObjectOld.(*v1alpha1.NetworkInfo)
			newNI, ok2 := e.ObjectNew.(*v1alpha1.NetworkInfo)
			if !ok1 || !ok2 {
				return false
			}
			return !slices.Equal(oldNI.AllowedDNSDomains, newNI.AllowedDNSDomains)
		},
		GenericFunc: func(event.GenericEvent) bool {
			return false
		},
	}
}

// collectDNSGarbage performs DNS record garbage collection for LoadBalancer Services.
func (r *ServiceLbReconciler) collectDNSGarbage(ctx context.Context) error {
	if r.DNS == nil {
		return nil
	}
	apiSet := sets.New[types.NamespacedName]()
	svcs, err := getLoadBalancerServicesWithDNS(ctx, r.Client)
	if err != nil {
		log.Error(err, "Service LB GC: failed to list Services")
		return err
	}
	for _, svc := range svcs {
		apiSet.Insert(types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name})
	}
	ownersByKind := r.DNS.ListRecordOwnerResource()
	cachedServices := ownersByKind[dns.ResourceKindService]
	var errs []error
	for nn := range cachedServices {
		if apiSet.Has(nn) {
			continue
		}
		if err := r.clearDNSAndConditionForService(ctx, nn, "GC: missing or ineligible Service owner"); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("service LB garbage collection encountered %d error(s): %w", len(errs), errors.Join(errs...))
	}
	return nil
}

func (r *ServiceLbReconciler) clearDNSAndConditionForService(ctx context.Context, reqNN types.NamespacedName, op string) error {
	if delErr := r.deleteDNSForService(ctx, reqNN.Namespace, reqNN.Name, op); delErr != nil {
		return delErr
	}
	if uerr := r.removeServiceDNSReadyCondition(ctx, reqNN); uerr != nil {
		log.Error(uerr, "Failed to clear Service DNS Ready condition", "Service", reqNN.String(), "Operation", op)
		return fmt.Errorf("clearing DNS condition for %s: %w", op, uerr)
	}
	return nil
}
