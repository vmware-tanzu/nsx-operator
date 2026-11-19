/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/nsxworkflow"
	migrationstatus "github.com/vmware-tanzu/nsx-operator/pkg/migration/status"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/topology"
	migrationservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/migration"
)

const (
	vmOperatorAPIGroup   = "vmoperator.vmware.com"
	vmOperatorAPIVersion = "v1alpha1"
	vmOperatorVMListKind = "VirtualMachineList"
)

var deviceNameReplacer = strings.NewReplacer("-", "", "_", "", " ", "", ".", "")

// NamespaceTopologyDHCPDiscovery adapts the source inventory from the current
// Evaluate preflight to the shared WBS 1.1.5 DHCP input collector.
type NamespaceTopologyDHCPDiscovery struct{}

func (NamespaceTopologyDHCPDiscovery) DiscoverDHCPNetworks(_ context.Context, req Request) ([]string, error) {
	networks, err := batchSourceNetworks(req)
	if err != nil {
		return nil, err
	}
	var vdns []string
	for _, network := range networks {
		if network.DHCPBacked {
			vdns = append(vdns, network.VDNName)
		}
	}
	return vdns, nil
}

// NamespaceWorkloadCompatibility evaluates the immutable namespace batch before
// returning DHCP NeedsInput diagnostics. It reads live VM CRs and only inspects
// source configuration; it does not create or mutate network resources.
type NamespaceWorkloadCompatibility struct {
	Reader client.Reader
}

func (h NamespaceWorkloadCompatibility) Reconcile(ctx context.Context, req Request) (Result, error) {
	batch, ok := req.Object.(*api.NamespaceNetworkTransition)
	if !ok {
		return Result{}, fmt.Errorf("namespace compatibility check requires a NamespaceNetworkTransition")
	}
	setWorkloadCompatibilityCondition(req.Status, req.Generation, metav1.ConditionFalse, "WorkloadCompatibilityCheckInProgress", "Inspecting source VDNs and batch VirtualMachines")
	if h.Reader == nil {
		return Result{}, fmt.Errorf("VirtualMachine reader is not configured")
	}
	networks, err := batchSourceNetworks(req)
	if err != nil {
		return Result{}, err
	}
	names := make(map[string]struct{}, len(batch.Spec.Namespaces))
	for _, name := range batch.Spec.Namespaces {
		names[name] = struct{}{}
	}
	networkNames := make(map[string][]string, len(batch.Spec.Namespaces))
	for _, network := range networks {
		for _, namespace := range network.Namespaces {
			networkNames[namespace] = append(networkNames[namespace], network.VDNName)
		}
	}
	for namespace := range names {
		if len(networkNames[namespace]) == 0 {
			return Result{}, fmt.Errorf("source topology inventory has no VDN association for Namespace %q", namespace)
		}
		sort.Strings(networkNames[namespace])
	}

	incompatible := make(map[string][]string)
	vmCounts := make(map[string]int)
	for _, namespace := range batch.Spec.Namespaces {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(schema.GroupVersionKind{
			Group: vmOperatorAPIGroup, Version: vmOperatorAPIVersion, Kind: vmOperatorVMListKind,
		})
		if err := h.Reader.List(ctx, list, client.InNamespace(namespace)); err != nil {
			return Result{}, fmt.Errorf("listing VirtualMachines in Namespace %q: %w", namespace, err)
		}
		vmCounts[namespace] = len(list.Items)
		for i := range list.Items {
			vm := &list.Items[i]
			reasons := incompatibleVMReasons(vm)
			if len(reasons) != 0 {
				incompatible[namespace] = append(incompatible[namespace], fmt.Sprintf("VM %q (%s)", vm.GetName(), strings.Join(reasons, ", ")))
			}
		}
	}

	if batch.Status == nil {
		batch.Status = &api.NamespaceNetworkTransitionStatus{}
	}
	var messages []string
	for _, namespace := range batch.Spec.Namespaces {
		if workloads := incompatible[namespace]; len(workloads) != 0 {
			sort.Strings(workloads)
			message := fmt.Sprintf("Found unsupported workloads in Namespace %q: %s.", namespace, strings.Join(workloads, "; "))
			messages = append(messages, message)
			upsertNamespaceStatus(batch.Status, api.NamespaceStatus{
				Name: namespace, Phase: api.PhaseFailed, Reason: "IncompatibleWorkloadDetected", Message: message,
			})
			continue
		}
		message := fmt.Sprintf("Source VDNs: %s. Checked %d VirtualMachine CRs; no incompatible network devices found.", strings.Join(networkNames[namespace], ", "), vmCounts[namespace])
		upsertNamespaceStatus(batch.Status, api.NamespaceStatus{
			Name: namespace, Phase: api.PhaseEvaluating, Reason: "WorkloadsCompatible", Message: message,
		})
	}
	if len(messages) != 0 {
		message := strings.Join(messages, " ")
		setWorkloadCompatibilityCondition(req.Status, req.Generation, metav1.ConditionFalse, "IncompatibleWorkloadDetected", message)
		return Result{}, &IncompatibleWorkloadError{Message: message}
	}
	setWorkloadCompatibilityCondition(req.Status, req.Generation, metav1.ConditionTrue, "WorkloadsCompatible", "All batch VirtualMachines are compatible with VPC SubnetPort networking")
	return Result{Outcome: OutcomeSucceeded}, nil
}

// NamespaceEvaluate runs source-only NSX validation after source discovery and
// DHCP input validation. It does not prepare or require target VPC resources.
type NamespaceEvaluate struct {
	NSXService *migrationservice.Service
}

func (h NamespaceEvaluate) Reconcile(ctx context.Context, req Request) (Result, error) {
	if err := h.validate(ctx, req); err != nil {
		return Result{}, err
	}
	if batch, ok := req.Object.(*api.NamespaceNetworkTransition); ok && batch.Status != nil {
		for i := range batch.Status.NamespaceStatuses {
			batch.Status.NamespaceStatuses[i].Phase = api.PhaseEvaluated
			batch.Status.NamespaceStatuses[i].Reason = "EvaluateCompleted"
		}
	}
	return Result{Outcome: OutcomeSucceeded}, nil
}

func (h NamespaceEvaluate) validate(ctx context.Context, req Request) error {
	batch, ok := req.Object.(*api.NamespaceNetworkTransition)
	if !ok || req.Status == nil {
		return fmt.Errorf("source-only validation requires a NamespaceNetworkTransition and status")
	}
	networks, err := batchSourceNetworks(req)
	if err != nil {
		return err
	}
	if h.NSXService == nil {
		return fmt.Errorf("NSX migration service is not configured")
	}
	nsxClient, err := h.NSXService.Client()
	if err != nil {
		return fmt.Errorf("initializing NSX source-validation client: %w", err)
	}
	workflowClient := nsxworkflow.NewClient(nsxClient)
	if workflowClient == nil {
		return fmt.Errorf("NSX workflow client is not configured")
	}

	previewByVDN := make(map[string][]dvpgPreview, len(networks))
	for _, network := range networks {
		workflowID := namespaceValidationWorkflowID(batch, network.VDNName)
		if existing := recordedWorkflowID(req.Status, network.VDNName); existing != "" {
			workflowID = existing
		}
		if err := migrationstatus.RecordWorkflow(req.Status, api.NSXWorkflowStatus{VDNName: network.VDNName, WorkflowID: workflowID}); err != nil {
			return fmt.Errorf("checkpointing source validation workflow for VDN %q: %w", network.VDNName, err)
		}
		desiredState := nsxworkflow.DesiredStateValidation
		vdsRef := network.VDSRef
		request := model.DvpgToSubnetWorkflow{
			Id:           &workflowID,
			DesiredState: &desiredState,
			VdsRef:       &vdsRef,
			// VpcPath is intentionally nil: Evaluate performs source-only validation.
		}
		if err := workflowClient.Put(ctx, request); err != nil {
			setPreviewCondition(req.Status, req.Generation, metav1.ConditionFalse, "SourceValidationFailed", fmt.Sprintf("NSX VALIDATION request for VDN %q failed", network.VDNName))
			return fmt.Errorf("submitting source-only NSX VALIDATION for VDN %q: %w", network.VDNName, err)
		}
		waitCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		workflowStatus, err := workflowClient.WaitForCompletion(waitCtx, workflowID, "", nsxworkflow.DesiredStateValidation)
		cancel()
		if err != nil {
			setPreviewCondition(req.Status, req.Generation, metav1.ConditionFalse, "SourceValidationFailed", fmt.Sprintf("NSX VALIDATION for VDN %q did not complete successfully", network.VDNName))
			return fmt.Errorf("waiting for source-only NSX VALIDATION for VDN %q: %w", network.VDNName, err)
		}
		if err := nsxworkflow.ValidateSourceOnlyScope(workflowStatus); err != nil {
			setPreviewCondition(req.Status, req.Generation, metav1.ConditionFalse, "SourceValidationFailed", fmt.Sprintf("NSX VALIDATION for VDN %q returned a target VPC path", network.VDNName))
			return err
		}
		results, err := workflowClient.ListValidationResults(ctx, workflowID, 1000)
		if err != nil {
			setPreviewCondition(req.Status, req.Generation, metav1.ConditionFalse, "SourceValidationFailed", fmt.Sprintf("reading NSX VALIDATION results for VDN %q failed", network.VDNName))
			return fmt.Errorf("listing source-only NSX validation results for VDN %q: %w", network.VDNName, err)
		}
		previewByVDN[network.VDNName], err = auditDVPGValidationResults(network, results)
		if err != nil {
			setPreviewCondition(req.Status, req.Generation, metav1.ConditionFalse, "SourceValidationFailed", fmt.Sprintf("NSX VALIDATION results for VDN %q are incomplete or contain errors", network.VDNName))
			return err
		}
	}

	previewCount := 0
	for _, network := range networks {
		previewCount += len(previewByVDN[network.VDNName])
	}
	for _, namespace := range batch.Spec.Namespaces {
		message := namespacePreviewMessage(batch.Status, namespace, networks, previewByVDN)
		upsertNamespaceStatus(batch.Status, api.NamespaceStatus{
			Name: namespace, Phase: api.PhaseEvaluating, Reason: "MigrationPreviewAvailable", Message: message,
		})
	}
	setPreviewCondition(req.Status, req.Generation, metav1.ConditionTrue, "SourceValidationSucceeded", fmt.Sprintf("Source-only NSX validation succeeded for %d VDNs and %d DVPGs; per-VDN workflow IDs and validation results are available for the read-only migration preview", len(networks), previewCount))
	return nil
}

// IncompatibleWorkloadError allows the action engine to publish the design
// contract phase Failed rather than the generic EvaluationFailed phase.
type IncompatibleWorkloadError struct {
	Message string
}

func (e *IncompatibleWorkloadError) Error() string { return e.Message }

type dvpgPreview struct {
	DVPGRef       string
	Name          string
	VLANID        string
	Compatibility string
	Warnings      []string
}

func namespaceValidationWorkflowID(batch *api.NamespaceNetworkTransition, vdnName string) string {
	sum := sha256.Sum256([]byte(string(batch.UID) + "/" + vdnName))
	return "ncp-" + hex.EncodeToString(sum[:16])
}

func recordedWorkflowID(status *api.TransitionStatus, vdnName string) string {
	for _, workflow := range status.NSXWorkflows {
		if workflow.VDNName == vdnName {
			return workflow.WorkflowID
		}
	}
	return ""
}

func auditDVPGValidationResults(network topology.SourceNetwork, results []model.DvpgValidationResult) ([]dvpgPreview, error) {
	expected := make(map[string]struct{}, len(network.DVPGRefs))
	for _, ref := range network.DVPGRefs {
		expected[ref] = struct{}{}
	}
	seen := make(map[string]struct{}, len(results))
	preview := make([]dvpgPreview, 0, len(results))
	for _, result := range results {
		ref := stringValue(result.DvpgRef)
		if ref == "" {
			return nil, fmt.Errorf("NSX validation result for VDN %q has no DVPG reference", network.VDNName)
		}
		if _, ok := expected[ref]; !ok {
			return nil, fmt.Errorf("NSX validation returned unexpected DVPG %q for VDN %q", ref, network.VDNName)
		}
		if _, duplicate := seen[ref]; duplicate {
			return nil, fmt.Errorf("NSX validation returned duplicate DVPG %q for VDN %q", ref, network.VDNName)
		}
		seen[ref] = struct{}{}
		compatibility := strings.ToUpper(stringValue(result.Compatibility))
		switch compatibility {
		case "SUPPORTED", "PARTIALLY_SUPPORTED":
		case "UNSUPPORTED":
			return nil, fmt.Errorf("NSX validation marks DVPG %q in VDN %q unsupported", ref, network.VDNName)
		default:
			return nil, fmt.Errorf("NSX validation returned unknown compatibility %q for DVPG %q", compatibility, ref)
		}
		vlanID := stringValue(result.VlanId)
		if vlanID == "" {
			return nil, fmt.Errorf("NSX validation returned no VLAN ID for DVPG %q in VDN %q", ref, network.VDNName)
		}
		name := stringValue(result.DvpgName)
		if name == "" {
			name = ref
		}
		entry := dvpgPreview{DVPGRef: ref, Name: name, VLANID: vlanID, Compatibility: compatibility}
		for _, issue := range result.Issues {
			severity := strings.ToUpper(stringValue(issue.Severity))
			summary := stringValue(issue.Summary)
			if summary == "" {
				summary = stringValue(issue.Setting)
			}
			switch severity {
			case "WARNING":
				entry.Warnings = append(entry.Warnings, summary)
			case "ERROR":
				return nil, fmt.Errorf("NSX validation found an error on DVPG %q: %s", ref, summary)
			default:
				return nil, fmt.Errorf("NSX validation returned unknown issue severity %q on DVPG %q", severity, ref)
			}
		}
		sort.Strings(entry.Warnings)
		preview = append(preview, entry)
	}
	for ref := range expected {
		if _, ok := seen[ref]; !ok {
			return nil, fmt.Errorf("NSX validation omitted DVPG %q for VDN %q", ref, network.VDNName)
		}
	}
	sort.Slice(preview, func(i, j int) bool { return preview[i].DVPGRef < preview[j].DVPGRef })
	return preview, nil
}

func namespacePreviewMessage(status *api.NamespaceNetworkTransitionStatus, namespace string, networks []topology.SourceNetwork, previewByVDN map[string][]dvpgPreview) string {
	var details []string
	for _, network := range networks {
		if !containsString(network.Namespaces, namespace) {
			continue
		}
		for _, dvpg := range previewByVDN[network.VDNName] {
			line := fmt.Sprintf("VDN %s: DVPG %s (ref %s), VLAN %s, %s", network.VDNName, dvpg.Name, dvpg.DVPGRef, dvpg.VLANID, dvpg.Compatibility)
			if len(dvpg.Warnings) != 0 {
				line += "; warnings: " + strings.Join(dvpg.Warnings, ", ")
			}
			details = append(details, line)
		}
	}
	message := "Source-only NSX validation succeeded. " + strings.Join(details, "; ")
	if status != nil {
		for _, entry := range status.NamespaceStatuses {
			if entry.Name == namespace && entry.Message != "" {
				message = entry.Message + " " + message
				break
			}
		}
	}
	return limitStatusMessage(message, 4096)
}

func limitStatusMessage(message string, maxBytes int) string {
	if len(message) <= maxBytes {
		return message
	}
	const suffix = " … (details remain in the NSX workflow validation results)"
	var builder strings.Builder
	for _, r := range message {
		if builder.Len()+utf8.RuneLen(r)+len(suffix) > maxBytes {
			break
		}
		builder.WriteRune(r)
	}
	return builder.String() + suffix
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func setPreviewCondition(status *api.TransitionStatus, generation int64, conditionStatus metav1.ConditionStatus, reason, message string) {
	if status == nil {
		return
	}
	meta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type: api.ConditionMigrationPreviewAvailable, Status: conditionStatus,
		ObservedGeneration: generation, Reason: reason, Message: message, LastTransitionTime: metav1.Now(),
	})
}

func batchSourceNetworks(req Request) ([]topology.SourceNetwork, error) {
	batch, ok := req.Object.(*api.NamespaceNetworkTransition)
	if !ok {
		return nil, fmt.Errorf("source topology requires a NamespaceNetworkTransition")
	}
	if req.NamespaceTopology == nil {
		return nil, fmt.Errorf("live namespace source topology is unavailable")
	}
	if err := req.NamespaceTopology.Validate(); err != nil {
		return nil, fmt.Errorf("validating namespace source topology: %w", err)
	}
	batchNamespaces := make(map[string]struct{}, len(batch.Spec.Namespaces))
	for _, namespace := range batch.Spec.Namespaces {
		batchNamespaces[namespace] = struct{}{}
	}
	discoveredVDNs := make(map[string]struct{})
	for _, resource := range req.NamespaceTopology.Resources {
		if resource.Kind != topology.ResourceVDN {
			continue
		}
		if resource.VDNName == "" {
			return nil, fmt.Errorf("source topology inventory contains a VDN without its Kubernetes name")
		}
		discoveredVDNs[resource.VDNName] = struct{}{}
	}
	if len(req.NamespaceTopology.SourceNetworks) == 0 {
		return nil, fmt.Errorf("live topology snapshot has no source network inspection results")
	}
	networks := append([]topology.SourceNetwork(nil), req.NamespaceTopology.SourceNetworks...)
	sort.Slice(networks, func(i, j int) bool { return networks[i].VDNName < networks[j].VDNName })
	seen := make(map[string]struct{}, len(networks))
	for _, network := range networks {
		if network.VDNName == "" {
			return nil, fmt.Errorf("source network inspection returned an empty VDN name")
		}
		if _, duplicate := seen[network.VDNName]; duplicate {
			return nil, fmt.Errorf("source network inspection returned duplicate VDN %q", network.VDNName)
		}
		seen[network.VDNName] = struct{}{}
		if _, exists := discoveredVDNs[network.VDNName]; !exists {
			return nil, fmt.Errorf("source network VDN %q is absent from the topology resource inventory", network.VDNName)
		}
		if !network.DHCPKnown {
			return nil, fmt.Errorf("DHCP status for source VDN %q is unknown", network.VDNName)
		}
		if strings.TrimSpace(network.VDSRef) == "" {
			return nil, fmt.Errorf("source VDN %q has no valid vds_ref for NSX validation", network.VDNName)
		}
		if len(network.DVPGRefs) == 0 {
			return nil, fmt.Errorf("source VDN %q has no discovered DVPG references", network.VDNName)
		}
		seenDVPGs := make(map[string]struct{}, len(network.DVPGRefs))
		for _, ref := range network.DVPGRefs {
			if strings.TrimSpace(ref) == "" {
				return nil, fmt.Errorf("source VDN %q has an empty DVPG reference", network.VDNName)
			}
			if _, duplicate := seenDVPGs[ref]; duplicate {
				return nil, fmt.Errorf("source VDN %q has duplicate DVPG reference %q", network.VDNName, ref)
			}
			seenDVPGs[ref] = struct{}{}
		}
		if len(network.Namespaces) == 0 {
			return nil, fmt.Errorf("source VDN %q has no associated batch Namespace", network.VDNName)
		}
		associatedNamespaces := make(map[string]struct{}, len(network.Namespaces))
		for _, namespace := range network.Namespaces {
			if _, exists := batchNamespaces[namespace]; !exists {
				return nil, fmt.Errorf("source VDN %q references Namespace %q outside the immutable batch", network.VDNName, namespace)
			}
			if _, duplicate := associatedNamespaces[namespace]; duplicate {
				return nil, fmt.Errorf("source VDN %q has duplicate Namespace association %q", network.VDNName, namespace)
			}
			associatedNamespaces[namespace] = struct{}{}
		}
	}
	for vdn := range discoveredVDNs {
		if _, inspected := seen[vdn]; !inspected {
			return nil, fmt.Errorf("source VDN %q has no DHCP/addressing inspection result", vdn)
		}
	}
	return networks, nil
}

func upsertNamespaceStatus(status *api.NamespaceNetworkTransitionStatus, update api.NamespaceStatus) {
	for i := range status.NamespaceStatuses {
		if status.NamespaceStatuses[i].Name == update.Name {
			status.NamespaceStatuses[i] = update
			return
		}
	}
	status.NamespaceStatuses = append(status.NamespaceStatuses, update)
}

func setWorkloadCompatibilityCondition(status *api.TransitionStatus, generation int64, conditionStatus metav1.ConditionStatus, reason, message string) {
	if status == nil {
		return
	}
	meta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type: api.ConditionWorkloadCompatibilityEvaluated, Status: conditionStatus,
		ObservedGeneration: generation, Reason: reason, Message: message, LastTransitionTime: metav1.Now(),
	})
}

// incompatibleVMReasons checks the configured network adapter type exposed by
// the VirtualMachine API version listed by NamespaceWorkloadCompatibility
// (v1alpha1). The CPVM's served v1alpha1 CRD defines
// spec.networkInterfaces[].ethernetCardType and has no spec.hardware.devices
// field. The CRD's storage version, v1alpha6, also has no
// spec.hardware.devices: spec.hardware defines CD-ROM and controller settings,
// while observed hardware devices appear under status and enumerate only CDROM
// and Disk. The recursive scan requested by the WBS would therefore walk a
// path that these CRD versions do not expose. Match all three unsupported
// adapter families against ethernetCardType and normalize it once per
// interface to avoid repeated string allocations.
//
// v1alpha6 additionally reports runtime UPTv2 state under
// status.network.interfaces[].vmxnet3.uptv2Active. This handler reads v1alpha1,
// so evaluating that runtime state requires separate version-aware support.
func incompatibleVMReasons(vm *unstructured.Unstructured) []string {
	reasons := make(map[string]struct{})
	interfaces, found, _ := unstructured.NestedSlice(vm.Object, "spec", "networkInterfaces")
	if found {
		for _, item := range interfaces {
			networkInterface, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			cardType, _, _ := unstructured.NestedString(networkInterface, "ethernetCardType")
			normalizedCardType := normalizeDeviceName(cardType)
			if strings.Contains(normalizedCardType, "sriov") {
				reasons["SR-IOV virtual NIC"] = struct{}{}
			}
			if strings.Contains(normalizedCardType, "directpath") {
				reasons["DynamicDirectPath I/O network adapter"] = struct{}{}
			}
			if strings.Contains(normalizedCardType, "pcipassthrough") {
				reasons["PCI passthrough network device"] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(reasons))
	for reason := range reasons {
		result = append(result, reason)
	}
	sort.Strings(result)
	return result
}

func normalizeDeviceName(value string) string {
	value = strings.ToLower(value)
	return deviceNameReplacer.Replace(value)
}

var _ Handler = NamespaceWorkloadCompatibility{}
var _ Handler = NamespaceEvaluate{}
