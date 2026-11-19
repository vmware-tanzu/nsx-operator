/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"reflect"

	netop "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/inspection"
)

const (
	sourceSNATOwnerLabel   = "migration.network.vmware.com/transition-uid"
	sourceSNATNetworkLabel = "migration.network.vmware.com/source-network-uid"
)

var errVIPUnavailable = errors.New("VIP address unavailable")

// +kubebuilder:rbac:groups=netoperator.vmware.com,resources=ipaddressallocations,verbs=get;list;create

// SourceNetwork locates the managed Network from which Net Operator can reserve
// an address. The Network and allocation must live in the same Namespace.
type SourceNetwork interface {
	ForBacking(context.Context, string) (client.ObjectKey, error)
}

// NetOperatorSNAT uses a stable allocation name, so retrying a lost status
// update finds the same source reservation instead of taking another address.
type NetOperatorSNAT struct {
	Client   client.Client
	Reader   client.Reader
	Networks SourceNetwork
}

func (service NetOperatorSNAT) Ensure(ctx context.Context, transition *api.ControlPlaneNetworkTransition, decision inspection.LBSNATDecision) (api.IPAllocationStatus, bool, error) {
	if service.Client == nil || service.Reader == nil || transition == nil || transition.UID == "" {
		return api.IPAllocationStatus{}, false, fmt.Errorf("source SNAT allocation services and transition identity are required")
	}
	if transition.Status != nil && len(transition.Status.LBSNATIPAllocations) > 0 {
		accepted := transition.Status.LBSNATIPAllocations[0]
		if accepted.AllocationRef == nil {
			return accepted, true, nil
		}
		allocation, err := service.readAllocation(ctx, transition, client.ObjectKey{Namespace: accepted.AllocationRef.Namespace, Name: accepted.AllocationRef.Name}, "")
		if err != nil {
			return api.IPAllocationStatus{}, false, err
		}
		if allocation.Status.IPAddress != accepted.IPAddress {
			return api.IPAllocationStatus{}, false, fmt.Errorf("accepted LB SNAT source allocation changed address")
		}
		if !sourceAllocationReady(allocation) {
			return api.IPAllocationStatus{}, false, fmt.Errorf("accepted LB SNAT source allocation is no longer Ready")
		}
		return accepted, true, nil
	}
	configured := decision.Selection == inspection.UseConfiguredLBSNATIP
	if configured {
		if ip, err := netip.ParseAddr(decision.IPAddress); err != nil || !ip.Is4() {
			return api.IPAllocationStatus{}, false, fmt.Errorf("configured LB SNAT address must be IPv4")
		}
	}
	if !configured && decision.Selection != inspection.AllocateFromBackendNetwork && decision.Selection != inspection.AllocateFromVIPRange {
		return api.IPAllocationStatus{}, false, fmt.Errorf("unsupported LB SNAT selection %q", decision.Selection)
	}
	if service.Networks == nil {
		if configured {
			return api.IPAllocationStatus{IPAddress: decision.IPAddress}, true, nil
		}
		return api.IPAllocationStatus{}, false, fmt.Errorf("source Network resolver is required")
	}
	networkKey, err := service.Networks.ForBacking(ctx, decision.AllocationSource)
	if err != nil {
		if configured && errors.Is(err, ErrNoManagedSourceNetwork) {
			return api.IPAllocationStatus{IPAddress: decision.IPAddress}, true, nil
		}
		return api.IPAllocationStatus{}, false, err
	}
	if networkKey.Name == "" || networkKey.Namespace == "" {
		return api.IPAllocationStatus{}, false, fmt.Errorf("source Network must have Namespace and name")
	}
	var network netop.Network
	if err := service.Reader.Get(ctx, networkKey, &network); err != nil {
		return api.IPAllocationStatus{}, false, err
	}
	if network.UID == "" || network.Spec.Type != netop.NetworkTypeVDS || !network.DeletionTimestamp.IsZero() {
		return api.IPAllocationStatus{}, false, fmt.Errorf("source Network is not a live VDS Network")
	}
	current := &api.ControlPlaneNetworkTransition{}
	if err := service.Reader.Get(ctx, client.ObjectKeyFromObject(transition), current); err != nil {
		return api.IPAllocationStatus{}, false, err
	}
	if current.UID != transition.UID || current.Generation != transition.Generation || current.Spec.Action != api.ActionEvaluate || !current.DeletionTimestamp.IsZero() {
		return api.IPAllocationStatus{}, false, fmt.Errorf("source reservation requires the current Evaluate request")
	}
	if configured {
		return service.ensureAllocation(ctx, transition, &network, decision.IPAddress)
	}
	if decision.Selection == inspection.AllocateFromBackendNetwork {
		return service.ensureAllocation(ctx, transition, &network, "")
	}
	for _, vipRange := range decision.VIPRanges {
		address, err := netip.ParseAddr(vipRange.Address)
		if err != nil || !address.Is4() || vipRange.Count < 1 || vipRange.Count > 4096 {
			return api.IPAllocationStatus{}, false, fmt.Errorf("invalid IPv4 VIP range")
		}
		for count := 0; count < vipRange.Count; count++ {
			if !address.Is4() {
				return api.IPAllocationStatus{}, false, fmt.Errorf("IPv4 VIP range overflows")
			}
			allocation, ready, err := service.ensureAllocation(ctx, transition, &network, address.String())
			if errors.Is(err, errVIPUnavailable) { // Net Operator rejected this specific VIP address.
				address = address.Next()
				continue
			}
			return allocation, ready, err
		}
	}
	return api.IPAllocationStatus{}, false, fmt.Errorf("no free IPv4 address remains in the FLB VIP ranges")
}

func (service NetOperatorSNAT) ensureAllocation(ctx context.Context, transition *api.ControlPlaneNetworkTransition, network *netop.Network, requestedIP string) (api.IPAllocationStatus, bool, error) {
	identity := sha256.Sum256([]byte(string(transition.UID) + ":" + network.Namespace + "/" + network.Name + ":" + requestedIP))
	key := client.ObjectKey{Namespace: network.Namespace, Name: "migration-snat-" + hex.EncodeToString(identity[:12])}
	desired := netop.IPAddressAllocationSpec{PoolRef: corev1.TypedLocalObjectReference{APIGroup: ptr.To(netop.GroupName), Kind: "Network", Name: network.Name}, RequestedIP: requestedIP}
	allocation := &netop.IPAddressAllocation{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, Labels: map[string]string{sourceSNATOwnerLabel: string(transition.UID), sourceSNATNetworkLabel: string(network.UID)}}, Spec: desired}
	if err := service.Client.Create(ctx, allocation); err != nil && !apierrors.IsAlreadyExists(err) {
		return api.IPAllocationStatus{}, false, err
	}
	allocation, err := service.readAllocation(ctx, transition, key, requestedIP)
	if err != nil {
		return api.IPAllocationStatus{}, false, err
	}
	if !reflect.DeepEqual(allocation.Spec, desired) {
		return api.IPAllocationStatus{}, false, fmt.Errorf("source allocation request conflicts with the existing allocation")
	}
	for _, condition := range allocation.Status.Conditions {
		if condition.Type == netop.IPAddressAllocationFail && condition.Status == corev1.ConditionTrue {
			if condition.Reason == netop.IPAddressAllocationConditionFailureReasonCannotAllocIP && requestedIP != "" {
				return api.IPAllocationStatus{}, false, errVIPUnavailable
			}
			return api.IPAllocationStatus{}, false, fmt.Errorf("source allocation failed: %s", condition.Reason)
		}
		if condition.Type == netop.IPAddressAllocationReady && condition.Status == corev1.ConditionTrue {
			ip, err := netip.ParseAddr(allocation.Status.IPAddress)
			if err != nil || !ip.Is4() || (requestedIP != "" && allocation.Status.IPAddress != requestedIP) {
				return api.IPAllocationStatus{}, false, fmt.Errorf("source allocation returned an unexpected IPv4 address")
			}
			return api.IPAllocationStatus{IPAddress: ip.String(), AllocationRef: &api.NamespacedName{Namespace: key.Namespace, Name: key.Name}}, true, nil
		}
	}
	return api.IPAllocationStatus{}, false, nil
}

func sourceAllocationReady(allocation *netop.IPAddressAllocation) bool {
	for _, condition := range allocation.Status.Conditions {
		if condition.Type == netop.IPAddressAllocationReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func (service NetOperatorSNAT) readAllocation(ctx context.Context, transition *api.ControlPlaneNetworkTransition, key client.ObjectKey, requestedIP string) (*netop.IPAddressAllocation, error) {
	allocation := &netop.IPAddressAllocation{}
	if err := service.Reader.Get(ctx, key, allocation); err != nil {
		return nil, err
	}
	if allocation.Labels[sourceSNATOwnerLabel] != string(transition.UID) || !allocation.DeletionTimestamp.IsZero() {
		return nil, fmt.Errorf("source allocation is not owned by this transition")
	}
	if allocation.Spec.PoolRef.Kind != "Network" || allocation.Spec.PoolRef.Name == "" {
		return nil, fmt.Errorf("source allocation has no Network pool")
	}
	var network netop.Network
	if err := service.Reader.Get(ctx, client.ObjectKey{Namespace: key.Namespace, Name: allocation.Spec.PoolRef.Name}, &network); err != nil {
		return nil, err
	}
	if network.UID == "" || allocation.Labels[sourceSNATNetworkLabel] != string(network.UID) || network.Spec.Type != netop.NetworkTypeVDS || !network.DeletionTimestamp.IsZero() {
		return nil, fmt.Errorf("source allocation Network identity changed")
	}
	if requestedIP != "" && allocation.Spec.RequestedIP != requestedIP {
		return nil, fmt.Errorf("source allocation requested IP changed")
	}
	return allocation, nil
}
