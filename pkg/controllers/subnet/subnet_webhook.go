package subnet

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	controllercommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var NSXOperatorSA = "system:serviceaccount:vmware-system-nsx:ncp-svc-account"

// Create a validator instead of using the existing one in controller-runtime because the existing one can't
// inspect admission.Request in Handle function.

// +kubebuilder:webhook:path=/validate-crd-nsx-vmware-com-v1alpha1-subnet,mutating=false,failurePolicy=fail,sideEffects=None,groups=crd.nsx.vmware.com,resources=subnets,verbs=create;update;delete,versions=v1alpha1,name=subnet.validating.crd.nsx.vmware.com,admissionReviewVersions=v1

type SubnetValidator struct {
	Client    client.Client
	decoder   admission.Decoder
	nsxClient *nsx.Client
}

// Handle handles admission requests.
func (v *SubnetValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	log.Info("Handling request", "user", req.UserInfo.Username, "operation", req.Operation)
	subnet := &v1alpha1.Subnet{}

	var err error
	if req.Operation == admissionv1.Delete {
		err = v.decoder.DecodeRaw(req.OldObject, subnet)
	} else {
		err = v.decoder.Decode(req, subnet)
	}
	if err != nil {
		log.Error(err, "error while decoding Subnet", "Subnet", req.Namespace+"/"+req.Name)
		return admission.Errored(http.StatusBadRequest, err)
	}
	switch req.Operation {
	case admissionv1.Create:
		valid, msg := util.ValidateSubnetSize(v.nsxClient, subnet.Spec.IPv4SubnetSize)
		if !valid {
			return admission.Denied(fmt.Sprintf("Subnet %s/%s has invalid size %d: %s", subnet.Namespace, subnet.Name, subnet.Spec.IPv4SubnetSize, msg))
		}
		// Shared Subnet can only be updated by NSX Operator
		if (common.IsSharedSubnet(subnet)) && req.UserInfo.Username != NSXOperatorSA {
			return admission.Denied(fmt.Sprintf("Shared Subnet %s/%s can only be created by NSX Operator", subnet.Namespace, subnet.Name))
		}

		// Prevent users from setting spec.vpcName and spec.vlanConnectionName
		if req.UserInfo.Username != NSXOperatorSA {
			if subnet.Spec.VPCName != "" {
				return admission.Denied(fmt.Sprintf("Subnet %s/%s: spec.vpcName can only be set by NSX Operator", subnet.Namespace, subnet.Name))
			}
			if subnet.Spec.VLANConnectionName != "" {
				return admission.Denied(fmt.Sprintf("Subnet %s/%s: spec.vlanConnectionName can only be set by NSX Operator", subnet.Namespace, subnet.Name))
			}
			if subnet.Spec.AccessMode == v1alpha1.AccessMode(v1alpha1.AccessModeL2Only) {
				return admission.Denied(fmt.Sprintf("Subnet %s/%s: spec.accessMode L2Only is not supported", subnet.Namespace, subnet.Name))
			}
		}

	case admissionv1.Update:
		oldSubnet := &v1alpha1.Subnet{}
		if err := v.decoder.DecodeRaw(req.OldObject, oldSubnet); err != nil {
			log.Error(err, "Failed to decode old Subnet", "Subnet", req.Namespace+"/"+req.Name)
			return admission.Errored(http.StatusBadRequest, err)
		}

		log.Trace("Decoded old Subnet", "oldSubnet", oldSubnet)
		log.Trace("Decoded new Subnet", "subnet", subnet)
		log.Trace("User info", "username", req.UserInfo.Username, "isNSXOperator", req.UserInfo.Username == NSXOperatorSA)
		log.Trace("VPCName comparison", "oldVPCName", oldSubnet.Spec.VPCName, "newVPCName", subnet.Spec.VPCName, "isEqual", oldSubnet.Spec.VPCName == subnet.Spec.VPCName)

		// Shared Subnet can only be updated by NSX Operator
		if (common.IsSharedSubnet(oldSubnet) || common.IsSharedSubnet(subnet)) && req.UserInfo.Username != NSXOperatorSA {
			return admission.Denied(fmt.Sprintf("Shared Subnet %s/%s can only be updated by NSX Operator", subnet.Namespace, subnet.Name))
		}

		// Prevent users from updating spec.vpcName and spec.vlanConnectionName
		if req.UserInfo.Username != NSXOperatorSA {
			// Check if vpcName is being added or changed
			if oldSubnet.Spec.VPCName != subnet.Spec.VPCName {
				log.Trace("Denying update to vpcName", "oldVPCName", oldSubnet.Spec.VPCName, "newVPCName", subnet.Spec.VPCName)
				return admission.Denied(fmt.Sprintf("Subnet %s/%s: spec.vpcName can only be updated by NSX Operator", subnet.Namespace, subnet.Name))
			}
			if oldSubnet.Spec.VLANConnectionName != subnet.Spec.VLANConnectionName {
				return admission.Denied(fmt.Sprintf("Subnet %s/%s: spec.vlanConnectionName can only be updated by NSX Operator", subnet.Namespace, subnet.Name))
			}
			if !nsxutil.CompareArraysWithoutOrder(oldSubnet.Spec.IPAddresses, subnet.Spec.IPAddresses) {
				return admission.Denied("ipAddresses is immutable")
			}
		}
	case admissionv1.Delete:
		oldSubnet := &v1alpha1.Subnet{}
		if err := v.decoder.DecodeRaw(req.OldObject, oldSubnet); err != nil {
			log.Error(err, "Failed to decode old Subnet", "Subnet", req.Namespace+"/"+req.Name)
			return admission.Errored(http.StatusBadRequest, err)
		}

		// Shared Subnet can only be deleted by NSX Operator
		if (common.IsSharedSubnet(oldSubnet) || common.IsSharedSubnet(subnet)) && req.UserInfo.Username != NSXOperatorSA {
			return admission.Denied(fmt.Sprintf("Shared Subnet %s/%s can only be deleted by NSX Operator", subnet.Namespace, subnet.Name))
		}

		referredBySubnetSet, err := v.checkSubnetSet(ctx, subnet.Namespace, subnet.Name)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		if referredBySubnetSet {
			return admission.Denied(fmt.Sprintf("Subnet %s/%s used by SubnetSet cannot be deleted", subnet.Namespace, subnet.Name))
		}

		if req.UserInfo.Username != NSXOperatorSA {
			hasSubnetPort, err := v.checkSubnetPort(ctx, subnet.Namespace, subnet.Name)
			if err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			if hasSubnetPort {
				return admission.Denied(fmt.Sprintf("Subnet %s/%s with stale SubnetPorts cannot be deleted", subnet.Namespace, subnet.Name))
			}
			hasSubnetIPReservation, err := v.checkSubnetIPReservation(ctx, subnet.Namespace, subnet.Name)
			if err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			if hasSubnetIPReservation {
				return admission.Denied(fmt.Sprintf("Subnet %s/%s with stale SubnetIPReservations cannot be deleted", subnet.Namespace, subnet.Name))
			}
		}
	}
	if req.Operation != admissionv1.Delete {
		err := controllercommon.CheckAccessModeOrVisibility(v.Client, ctx, subnet.Namespace, string(subnet.Spec.AccessMode), "subnet")
		if err != nil {
			if errors.Is(err, controllercommon.ErrFailedToListNetworkInfo) {
				return admission.Errored(http.StatusServiceUnavailable, err)
			}
			log.Error(err, "AccessMode not supported", "AccessMode", subnet.Spec.AccessMode, "namespace", subnet.Namespace)
			return admission.Denied(err.Error())
		}
		if msg := validateStaticIPAllocation(subnet); msg != "" {
			return admission.Denied(msg)
		}
	}
	return admission.Allowed("")
}

// validateStaticIPAllocation enforces the semantic rules for
// spec.advancedConfig.staticIPAllocation.poolRanges that CEL cannot express:
//   - each range parses; start and end share an address family; start <= end
//   - each range is contained within some spec.ipAddresses CIDR of matching family
//   - ranges do not overlap each other
//   - ranges do not overlap reservedIPRanges
//   - DHCPRelay rejects both enabled=true and non-empty poolRanges
//
// An empty string return value means "allowed".
func validateStaticIPAllocation(subnet *v1alpha1.Subnet) string {
	staticEnabled := util.CRSubnetStaticIPAllocationEnabled(subnet)
	ranges := subnet.Spec.AdvancedConfig.StaticIPAllocation.PoolRanges
	mode := subnet.Spec.SubnetDHCPConfig.Mode

	// CEL already rejects these, but re-check so the user sees a precise message
	// that names both fields (enabled + poolRanges) when DHCPRelay is configured.
	if mode == v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeRelay) && (staticEnabled || len(ranges) > 0) {
		return fmt.Sprintf("Subnet %s/%s: staticIPAllocation (enabled and poolRanges) is not supported when subnetDHCPConfig.mode is DHCPRelay", subnet.Namespace, subnet.Name)
	}

	if len(ranges) == 0 {
		return ""
	}
	if !staticEnabled {
		return fmt.Sprintf("Subnet %s/%s: staticIPAllocation.poolRanges can only be set when staticIPAllocation.enabled is true", subnet.Namespace, subnet.Name)
	}

	// Parse and validate each range.
	type parsedRange struct {
		start, end netip.Addr
	}
	parsed := make([]parsedRange, 0, len(ranges))
	for i, r := range ranges {
		raw := r.Start
		if r.End != "" && r.End != r.Start {
			raw = fmt.Sprintf("%s-%s", r.Start, r.End)
		}
		start, end, err := util.ParseIPRange(raw)
		if err != nil {
			return fmt.Sprintf("Subnet %s/%s: staticIPAllocation.poolRanges[%d] invalid: %v", subnet.Namespace, subnet.Name, i, err)
		}
		parsed = append(parsed, parsedRange{start: start, end: end})
	}

	// Each range must be contained in some spec.ipAddresses CIDR of matching family.
	prefixes := make([]netip.Prefix, 0, len(subnet.Spec.IPAddresses))
	for i, cidr := range subnet.Spec.IPAddresses {
		p, err := netip.ParsePrefix(cidr)
		if err != nil {
			return fmt.Sprintf("Subnet %s/%s: spec.ipAddresses[%d]=%q is not a valid CIDR: %v", subnet.Namespace, subnet.Name, i, cidr, err)
		}
		prefixes = append(prefixes, p.Masked())
	}
	for i, p := range parsed {
		if !rangeWithinAnyPrefix(p.start, p.end, prefixes) {
			return fmt.Sprintf("Subnet %s/%s: staticIPAllocation.poolRanges[%d] (%s-%s) is not contained within any spec.ipAddresses CIDR", subnet.Namespace, subnet.Name, i, p.start, p.end)
		}
	}

	// No pairwise overlap among poolRanges.
	for i := 0; i < len(parsed); i++ {
		for j := i + 1; j < len(parsed); j++ {
			if rangesOverlap(parsed[i].start, parsed[i].end, parsed[j].start, parsed[j].end) {
				return fmt.Sprintf("Subnet %s/%s: staticIPAllocation.poolRanges[%d] and [%d] overlap", subnet.Namespace, subnet.Name, i, j)
			}
		}
	}

	// No overlap with reservedIPRanges.
	for i, raw := range subnet.Spec.SubnetDHCPConfig.DHCPServerAdditionalConfig.ReservedIPRanges {
		rStart, rEnd, err := util.ParseIPRange(raw)
		if err != nil {
			return fmt.Sprintf("Subnet %s/%s: subnetDHCPConfig.dhcpServerAdditionalConfig.reservedIPRanges[%d]=%q invalid: %v", subnet.Namespace, subnet.Name, i, raw, err)
		}
		for j, p := range parsed {
			if rangesOverlap(p.start, p.end, rStart, rEnd) {
				return fmt.Sprintf("Subnet %s/%s: staticIPAllocation.poolRanges[%d] overlaps reservedIPRanges[%d]", subnet.Namespace, subnet.Name, j, i)
			}
		}
	}
	return ""
}

func rangeWithinAnyPrefix(start, end netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if start.Is4() != p.Addr().Is4() {
			continue
		}
		if p.Contains(start) && p.Contains(end) {
			return true
		}
	}
	return false
}

// rangesOverlap returns true if [a1, a2] and [b1, b2] share any IP. Callers
// must ensure all four addresses belong to the same family; pairs of different
// families never overlap.
func rangesOverlap(a1, a2, b1, b2 netip.Addr) bool {
	if a1.Is4() != b1.Is4() {
		return false
	}
	// !(a2 < b1 || b2 < a1) == overlap
	if a2.Less(b1) || b2.Less(a1) {
		return false
	}
	return true
}

func (v *SubnetValidator) checkSubnetPort(ctx context.Context, ns string, subnetName string) (bool, error) {
	crdSubnetPorts := &v1alpha1.SubnetPortList{}
	err := v.Client.List(ctx, crdSubnetPorts, client.InNamespace(ns), client.MatchingFields{"spec.subnet": subnetName})
	if err != nil {
		return false, fmt.Errorf("failed to list SubnetPort: %v", err)
	}
	if len(crdSubnetPorts.Items) > 0 {
		return true, nil
	}
	return false, nil
}

func (v *SubnetValidator) checkSubnetIPReservation(ctx context.Context, ns string, subnetName string) (bool, error) {
	crdSubnetIPReservations := &v1alpha1.SubnetIPReservationList{}
	err := v.Client.List(ctx, crdSubnetIPReservations, client.InNamespace(ns), client.MatchingFields{"spec.subnet": subnetName})
	if err != nil {
		return false, fmt.Errorf("failed to list SubnetIPReservations: %v", err)
	}
	if len(crdSubnetIPReservations.Items) > 0 {
		return true, nil
	}
	return false, nil
}

func (v *SubnetValidator) checkSubnetSet(ctx context.Context, ns string, subnetName string) (bool, error) {
	crdSubnetSets := &v1alpha1.SubnetSetList{}
	err := v.Client.List(ctx, crdSubnetSets, client.InNamespace(ns))
	if err != nil {
		return false, fmt.Errorf("failed to list SubnetSet: %v", err)
	}
	for _, crdSubnetSet := range crdSubnetSets.Items {
		if crdSubnetSet.Spec.SubnetNames == nil {
			continue
		}
		for _, associatedSubnet := range *crdSubnetSet.Spec.SubnetNames {
			if associatedSubnet == subnetName {
				return true, nil
			}
		}
	}
	return false, nil
}
