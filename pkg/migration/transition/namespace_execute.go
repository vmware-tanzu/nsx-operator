/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/nsxworkflow"
	migrationstatus "github.com/vmware-tanzu/nsx-operator/pkg/migration/status"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/topology"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	migrationservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/migration"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/realizestate"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

const namespaceMigrationOwnerTag = "migration.network.vmware.com/namespace-transition"
const namespaceMigrationVPCKeyTag = "migration.network.vmware.com/source-vds-key"

// NamespaceExecute prepares one stable VPC container per source DVS, then runs
// NSX target VALIDATION and PLAN before any cutover handler is allowed to run.
// Target TGW/DVC/subnet topology resolution remains owned by the topology
// preparation workstream; this handler groups VPCs by source DVS and consumes
// the Stage 1 target project.
type NamespaceExecute struct {
	NSXService *migrationservice.Service
}

func (h NamespaceExecute) Reconcile(ctx context.Context, req Request) (Result, error) {
	batch, ok := req.Object.(*api.NamespaceNetworkTransition)
	if !ok || batch.Status == nil || batch.Status.EvaluationPreview == nil {
		return Result{}, fmt.Errorf("namespace Execute requires a current evaluation preview")
	}
	if req.Stage1Transition == nil || req.Stage1Transition.Status == nil || req.Stage1Transition.Status.TargetNSXProjectPath == "" {
		return Result{}, fmt.Errorf("namespace Execute requires the Stage 1 target NSX project path")
	}
	preview := batch.Status.EvaluationPreview
	if !namespacePreviewMatchesExecute(preview, req.Generation) || preview.ReviewRequired {
		return Result{}, fmt.Errorf("namespace Execute requires a current preview that has passed review")
	}
	networks, err := batchSourceNetworks(req)
	if err != nil {
		return Result{}, err
	}
	if len(networks) == 0 {
		return Result{}, fmt.Errorf("namespace Execute has no discovered source VDNs")
	}
	if h.NSXService == nil {
		return Result{}, fmt.Errorf("NSX migration service is not configured")
	}
	nsxClient, err := h.NSXService.Client()
	if err != nil {
		return Result{}, fmt.Errorf("initializing NSX namespace Execute client: %w", err)
	}
	if nsxClient.VPCClient == nil {
		return Result{}, fmt.Errorf("NSX VPC client is unavailable")
	}
	projectPath := req.Stage1Transition.Status.TargetNSXProjectPath
	orgID, projectID, err := common.NSXProjectPathToId(projectPath)
	if err != nil {
		return Result{}, fmt.Errorf("parsing Stage 1 target project path %q: %w", projectPath, err)
	}
	vpcsByDVSKey, err := listNamespaceMigrationVPCs(ctx, nsxClient, orgID, projectID)
	if err != nil {
		return Result{}, fmt.Errorf("listing target VPCs for DVS reuse: %w", err)
	}
	workflowClient := nsxworkflow.NewClient(nsxClient)
	if workflowClient == nil {
		return Result{}, fmt.Errorf("NSX workflow client is unavailable")
	}
	oldPreview := preview.DeepCopy()
	actual := preview.DeepCopy()
	actual.MaterialChanges = nil
	actual.PendingChecks = nil
	actual.Networks = make([]api.NamespaceNetworkPreview, 0, len(networks))
	for _, network := range networks {
		prior := namespaceNetworkPreview(oldPreview.Networks, network.VDNName)
		if prior == nil {
			return Result{}, fmt.Errorf("reviewed preview has no entry for source VDN %q", network.VDNName)
		}
		vpcName, err := namespaceTargetVPCName(batch, projectPath, network.VDNName, network.VDSRef, networks)
		if err != nil {
			return Result{}, err
		}
		vpc, vpcResource, err := ensureNamespaceVPC(ctx, nsxClient, batch, req, network.VDNName, network.VDSRef, vpcName, vpcsByDVSKey, *prior)
		if err != nil {
			return Result{}, err
		}
		current := *prior.DeepCopy()
		// Rebuild PLAN-owned resources each time so removed resources are
		// visible to the comparison instead of surviving from the old preview.
		current.TargetResources = nil
		for _, resource := range prior.TargetResources {
			if resource.Kind == "VPC" || resource.Decision == "Pending" {
				current.TargetResources = append(current.TargetResources, resource)
			}
		}
		upsertTargetResource(&current, vpcResource)
		workflowID := recordedWorkflowID(req.Status, network.VDNName)
		if workflowID == "" {
			workflowID = namespaceValidationWorkflowID(batch, network.VDNName)
		}
		if err := migrationstatus.RecordWorkflow(req.Status, api.NSXWorkflowStatus{VDNName: network.VDNName, WorkflowID: workflowID}); err != nil {
			return Result{}, fmt.Errorf("checkpointing target validation workflow for VDN %q: %w", network.VDNName, err)
		}
		vdsRef := network.VDSRef
		vpcPath := *vpc.Path
		validationState := nsxworkflow.DesiredStateValidation
		validationRequest := model.DvpgToSubnetWorkflow{
			Id: &workflowID, DesiredState: &validationState, VdsRef: &vdsRef, VpcPath: &vpcPath,
		}
		if err := workflowClient.Put(ctx, validationRequest); err != nil {
			setPreviewCondition(req.Status, req.Generation, metav1.ConditionFalse, "TargetValidationFailed", fmt.Sprintf("NSX target VALIDATION request for VDN %q failed", network.VDNName))
			return Result{}, fmt.Errorf("submitting NSX target VALIDATION for VDN %q: %w", network.VDNName, err)
		}
		waitCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		_, err = workflowClient.WaitForCompletion(waitCtx, workflowID, vpcPath, nsxworkflow.DesiredStateValidation)
		cancel()
		if err != nil {
			setPreviewCondition(req.Status, req.Generation, metav1.ConditionFalse, "TargetValidationFailed", fmt.Sprintf("NSX target VALIDATION for VDN %q did not complete successfully", network.VDNName))
			return Result{}, fmt.Errorf("waiting for NSX target VALIDATION for VDN %q: %w", network.VDNName, err)
		}
		results, err := workflowClient.ListValidationResults(ctx, workflowID, 1000)
		if err != nil {
			return Result{}, fmt.Errorf("listing NSX target validation results for VDN %q: %w", network.VDNName, err)
		}
		validatedDVPGs, err := auditDVPGValidationResults(network, results)
		if err != nil {
			setPreviewCondition(req.Status, req.Generation, metav1.ConditionFalse, "TargetValidationFailed", fmt.Sprintf("NSX target VALIDATION results for VDN %q are incomplete or incompatible", network.VDNName))
			return Result{}, err
		}
		current.DVPGs = make([]api.NamespaceDVPGPreview, 0, len(validatedDVPGs))
		for _, dvpg := range validatedDVPGs {
			current.DVPGs = append(current.DVPGs, api.NamespaceDVPGPreview{
				Reference: dvpg.DVPGRef, Name: dvpg.Name, VLANID: dvpg.VLANID,
				Compatibility: dvpg.Compatibility, Warnings: append([]string(nil), dvpg.Warnings...),
			})
		}
		vpcResource.Reason = targetValidationSummary(current.DVPGs)
		upsertTargetResource(&current, vpcResource)
		planState := nsxworkflow.DesiredStatePlan
		dvpgSpecs := make([]model.DvpgToSubnetDvpgSpec, 0, len(network.DVPGRefs))
		for _, ref := range network.DVPGRefs {
			dvpg := ref
			dvpgSpecs = append(dvpgSpecs, model.DvpgToSubnetDvpgSpec{DvpgRef: &dvpg})
		}
		planRequest := model.DvpgToSubnetWorkflow{
			Id: &workflowID, DesiredState: &planState, VdsRef: &vdsRef,
			VpcPath: &vpcPath, DvpgSpec: dvpgSpecs,
		}
		if err := workflowClient.Put(ctx, planRequest); err != nil {
			return Result{}, fmt.Errorf("submitting NSX target PLAN for VDN %q: %w", network.VDNName, err)
		}
		waitCtx, cancel = context.WithTimeout(ctx, 3*time.Minute)
		planStatus, err := workflowClient.WaitForCompletion(waitCtx, workflowID, vpcPath, nsxworkflow.DesiredStatePlan)
		cancel()
		if err != nil {
			setPreviewCondition(req.Status, req.Generation, metav1.ConditionFalse, "TargetPlanFailed", fmt.Sprintf("NSX target PLAN for VDN %q did not complete successfully", network.VDNName))
			return Result{}, fmt.Errorf("waiting for NSX target PLAN for VDN %q: %w", network.VDNName, err)
		}
		plannedResources, err := plannedNamespaceResources(planStatus.ConversionPayload)
		if err != nil {
			return Result{}, fmt.Errorf("reading NSX PLAN result for VDN %q: %w", network.VDNName, err)
		}
		if len(plannedResources) == 0 {
			return Result{}, fmt.Errorf("NSX PLAN for VDN %q returned no target resource decisions", network.VDNName)
		}
		for _, resource := range plannedResources {
			if resource.Kind == "VPC" {
				if resource.Path != "" && resource.Path != vpcPath {
					return Result{}, fmt.Errorf("NSX PLAN for VDN %q returned VPC path %q, expected validated VPC %q", network.VDNName, resource.Path, vpcPath)
				}
				// The VPC decision and its target VALIDATION summary were already
				// recorded above. PLAN must not replace that review information.
				continue
			}
			if resource.Path != "" && !strings.HasPrefix(resource.Path, vpcPath+"/") {
				return Result{}, fmt.Errorf("NSX PLAN for VDN %q returned %s path %q outside target VPC %q", network.VDNName, resource.Kind, resource.Path, vpcPath)
			}
			upsertTargetResource(&current, resource)
		}
		for _, kind := range []string{"TGW", "DVC", "Subnet"} {
			if targetResource(current, kind) == nil {
				upsertTargetResource(&current, api.NamespaceTargetResourcePreview{
					Kind: kind, Decision: "Pending", Reason: "Target resource decision is not available from NSX PLAN",
				})
			}
		}
		actual.Networks = append(actual.Networks, current)
	}
	for _, network := range actual.Networks {
		for _, resource := range network.TargetResources {
			if resource.Decision == "Pending" {
				actual.PendingChecks = []string{"target TGW/VPC/DVC/Subnet reuse or creation decisions"}
				break
			}
		}
		if len(actual.PendingChecks) != 0 {
			break
		}
	}

	changed, err := PublishNamespaceExecutionPreview(batch, req.Generation, actual)
	if err != nil {
		return Result{}, err
	}
	if changed {
		return Result{Outcome: OutcomeNeedsReview}, nil
	}
	meta.SetStatusCondition(&req.Status.Conditions, metav1.Condition{
		Type: api.ConditionMigrationPreviewAvailable, Status: metav1.ConditionTrue,
		ObservedGeneration: req.Generation, Reason: "TargetValidationAndPlanSucceeded",
		Message: "NSX target VALIDATION and PLAN succeeded; reviewed resource decisions are unchanged",
	})
	// Execute continues with the remaining migration phases in later handlers.
	// Keep the action checkpoint running so Commit cannot treat PLAN as cutover.
	return Result{Outcome: OutcomeInProgress, RequeueAfter: 30 * time.Second}, nil
}

func ensureNamespaceVPC(ctx context.Context, client *nsx.Client, batch *api.NamespaceNetworkTransition, req Request, vdnName, vdsRef, name string, vpcsByDVSKey map[string][]model.Vpc, preview api.NamespaceNetworkPreview) (model.Vpc, api.NamespaceTargetResourcePreview, error) {
	projectPath := req.Stage1Transition.Status.TargetNSXProjectPath
	orgID, projectID, err := common.NSXProjectPathToId(projectPath)
	if err != nil {
		return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("parsing Stage 1 target project path %q: %w", projectPath, err)
	}
	key := namespaceMigrationVPCKey(projectPath, vdsRef)
	resource := targetResource(preview, "VPC")
	if resource == nil {
		return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("reviewed preview has no VPC decision for VDN %q", vdnName)
	}
	if resource.Decision == "Reuse" {
		if resource.Path == "" {
			return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("reused VPC decision for VDN %q has no path", vdnName)
		}
		if !strings.HasPrefix(resource.Path, projectPath+"/vpcs/") {
			return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("reused VPC path %q is outside target project %q", resource.Path, projectPath)
		}
		indexedVPCs := uniqueVPCsByPath(vpcsByDVSKey[key])
		if len(indexedVPCs) > 1 {
			return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("multiple target VPCs carry source vDS identity %q", key)
		}
		if len(indexedVPCs) == 1 && ptr.Deref(indexedVPCs[0].Path, "") != resource.Path {
			return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("VDNs on source VDS %q resolved to different VPC paths %q and %q", vdsRef, ptr.Deref(indexedVPCs[0].Path, ""), resource.Path)
		}
		id := path.Base(resource.Path)
		vpc, err := client.VPCClient.Get(orgID, projectID, id)
		if err != nil {
			return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("reading reused VPC %q for VDN %q: %w", resource.Path, vdnName, err)
		}
		if ptr.Deref(vpc.Path, "") != resource.Path {
			return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("NSX returned VPC path %q for requested reused VPC %q", ptr.Deref(vpc.Path, ""), resource.Path)
		}
		if err := checkNamespaceVPCRealization(client, resource.Path); err != nil {
			return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("checking realization of reused target VPC %q: %w", resource.Path, err)
		}
		vpcsByDVSKey[key] = []model.Vpc{vpc}
		return vpc, *resource, nil
	}
	if resource.Decision != "Create" && resource.Decision != "Pending" {
		return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("target VPC decision for VDN %q is %q; Execute requires a reviewed Create or Reuse decision", vdnName, resource.Decision)
	}
	owner := string(batch.UID)
	if owner == "" {
		owner = batch.Name
	}
	if client.NsxConfig == nil || client.NsxConfig.CoeConfig == nil || strings.TrimSpace(client.NsxConfig.CoeConfig.Cluster) == "" {
		return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("NSX cluster identity is required to tag target VPC %q", name)
	}
	tags := util.BuildClusterTags(client.NsxConfig.CoeConfig.Cluster)
	tags = append(tags,
		model.Tag{Scope: ptr.To(common.TagScopeManagedBy), Tag: ptr.To(common.AutoCreatedTagValue)},
		model.Tag{Scope: ptr.To(namespaceMigrationOwnerTag), Tag: ptr.To(owner)},
		model.Tag{Scope: ptr.To(namespaceMigrationVPCKeyTag), Tag: ptr.To(key)},
	)
	decision := "Create"
	matches := uniqueVPCsByPath(vpcsByDVSKey[key])
	if len(matches) > 1 {
		return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("multiple target VPCs carry source vDS identity %q", key)
	}
	var vpc model.Vpc
	exists := len(matches) == 1
	if exists {
		vpc = matches[0]
		vpcPath := ptr.Deref(vpc.Path, "")
		if vpcPath == "" || !strings.HasPrefix(vpcPath, projectPath+"/vpcs/") {
			return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("VPC indexed for source vDS %q has invalid target project path %q", vdsRef, vpcPath)
		}
		vpc, err = client.VPCClient.Get(orgID, projectID, path.Base(vpcPath))
		if err != nil {
			return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("reading VPC indexed for source vDS %q at %q: %w", vdsRef, vpcPath, err)
		}
		indexedKey, foundKey, conflictKey := namespaceMigrationVPCKeyFromTags(vpc.Tags)
		if conflictKey || !foundKey || indexedKey != key {
			return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("VPC %q no longer carries the expected source vDS identity", vpcPath)
		}
		if !hasNamespaceMigrationOwner(vpc.Tags, owner) {
			decision = "Reuse"
		}
	} else {
		vpc, err = client.VPCClient.Get(orgID, projectID, name)
		if err != nil {
			var notFound *vapierrors.NotFound
			if !errors.As(err, &notFound) {
				return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("checking deterministic target VPC %q: %w", name, err)
			}
			ipType := model.Vpc_IP_ADDRESS_TYPE_IPV4
			vpc = model.Vpc{Id: ptr.To(name), DisplayName: ptr.To(name), IpAddressType: &ipType, Tags: tags}
			if err = client.VPCClient.Patch(orgID, projectID, name, vpc); err != nil {
				return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("creating target VPC %q for source vDS %q: %w", name, vdsRef, err)
			}
			vpc, err = client.VPCClient.Get(orgID, projectID, name)
			if err != nil {
				return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("reading created target VPC %q for source vDS %q: %w", name, vdsRef, err)
			}
			createdKey, foundKey, conflictKey := namespaceMigrationVPCKeyFromTags(vpc.Tags)
			if !hasNamespaceMigrationOwner(vpc.Tags, owner) || conflictKey || !foundKey || createdKey != key {
				return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("created target VPC %q is missing its NamespaceNetworkTransition or source vDS ownership tag", name)
			}
		} else {
			existingKey, foundKey, conflictKey := namespaceMigrationVPCKeyFromTags(vpc.Tags)
			if conflictKey {
				return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("target VPC %q has conflicting source vDS identity tags", name)
			}
			if foundKey && existingKey != key {
				return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("target VPC %q is already associated with a different source vDS", name)
			}
			if foundKey {
				if !hasNamespaceMigrationOwner(vpc.Tags, owner) {
					decision = "Reuse"
				}
			} else if hasNamespaceMigrationOwner(vpc.Tags, owner) {
				vpc.Tags = append(vpc.Tags, model.Tag{Scope: ptr.To(namespaceMigrationVPCKeyTag), Tag: ptr.To(key)})
				if err := client.VPCClient.Patch(orgID, projectID, name, vpc); err != nil {
					return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("adding source vDS identity to target VPC %q: %w", name, err)
				}
				vpc, err = client.VPCClient.Get(orgID, projectID, name)
				if err != nil {
					return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("verifying source vDS identity on target VPC %q: %w", name, err)
				}
				updatedKey, hasUpdatedKey, hasConflictingKeys := namespaceMigrationVPCKeyFromTags(vpc.Tags)
				if hasConflictingKeys || !hasUpdatedKey || updatedKey != key || !hasNamespaceMigrationOwner(vpc.Tags, owner) {
					return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("target VPC %q did not retain the expected source vDS and transition ownership tags", name)
				}
			} else {
				return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("target VPC %q already exists without this source vDS identity or NamespaceNetworkTransition ownership", name)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return model.Vpc{}, api.NamespaceTargetResourcePreview{}, err
	}
	if ptr.Deref(vpc.Path, "") == "" {
		vpc, err = client.VPCClient.Get(orgID, projectID, name)
		if err != nil {
			return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("reading created target VPC %q: %w", name, err)
		}
	}
	vpcPath := ptr.Deref(vpc.Path, "")
	if vpcPath == "" || !strings.HasPrefix(vpcPath, projectPath+"/vpcs/") {
		return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("created target VPC %q has invalid NSX path %q", name, vpcPath)
	}
	if err := checkNamespaceVPCRealization(client, vpcPath); err != nil {
		return model.Vpc{}, api.NamespaceTargetResourcePreview{}, fmt.Errorf("checking realization of target VPC %q: %w", vpcPath, err)
	}
	vpcsByDVSKey[key] = []model.Vpc{vpc}
	resource.Decision, resource.Name, resource.Path = decision, ptr.Deref(vpc.Id, name), vpcPath
	if decision == "Reuse" {
		resource.Reason = "Reused target VPC with the same target project and source vDS"
	} else {
		resource.Reason = "Created by namespace Execute for target validation and PLAN"
	}
	return vpc, *resource, nil
}

func checkNamespaceVPCRealization(client *nsx.Client, vpcPath string) error {
	realizeService := realizestate.InitializeRealizeState(common.Service{NSXClient: client})
	return realizeService.CheckRealizeState(util.NSXTRealizeRetry, vpcPath, []string{common.GatewayInterfaceId})
}

func namespaceTargetVPCName(batch *api.NamespaceNetworkTransition, projectPath, vdnName, vdsRef string, sourceNetworks []topology.SourceNetwork) (string, error) {
	names := make(map[string]struct{})
	addName := func(name string) {
		if name != "" {
			names[name] = struct{}{}
		}
	}
	foundVDS := false
	for _, source := range sourceNetworks {
		if source.VDSRef != vdsRef {
			continue
		}
		foundVDS = true
		if batch.Spec.NetworkConfiguration != nil && batch.Spec.NetworkConfiguration.ResourceNaming != nil {
			for _, item := range batch.Spec.NetworkConfiguration.ResourceNaming.Networks {
				if item.VDNName == source.VDNName {
					addName(string(item.VPCName))
				}
			}
		}
		if batch.Status != nil && batch.Status.EvaluationPreview != nil {
			if network := namespaceNetworkPreview(batch.Status.EvaluationPreview.Networks, source.VDNName); network != nil {
				if resource := targetResource(*network, "VPC"); resource != nil {
					addName(resource.Name)
				}
			}
		}
	}
	if !foundVDS || vdsRef == "" {
		return "", fmt.Errorf("source VDN %q has no stable VDS identity for target VPC naming", vdnName)
	}
	if len(names) > 1 {
		ordered := make([]string, 0, len(names))
		for name := range names {
			ordered = append(ordered, name)
		}
		sort.Strings(ordered)
		return "", fmt.Errorf("VDNs on source VDS %q specify conflicting VPC names %v", vdsRef, ordered)
	}
	for name := range names {
		return name, nil
	}
	digest := sha256.Sum256([]byte(projectPath + "\x00" + vdsRef))
	return "migration-" + hex.EncodeToString(digest[:8]), nil
}

func namespaceMigrationVPCKey(projectPath, vdsRef string) string {
	digest := sha256.Sum256([]byte(projectPath + "\x00" + vdsRef))
	return hex.EncodeToString(digest[:])
}

func namespaceMigrationVPCKeyFromTags(tags []model.Tag) (string, bool, bool) {
	key := ""
	found := false
	for _, tag := range tags {
		if ptr.Deref(tag.Scope, "") != namespaceMigrationVPCKeyTag {
			continue
		}
		value := ptr.Deref(tag.Tag, "")
		if found && value != key {
			return "", false, true
		}
		key, found = value, true
	}
	return key, found, false
}

func namespaceMigrationVPCKeysFromTags(tags []model.Tag) []string {
	keys := make([]string, 0, 1)
	seen := make(map[string]struct{})
	for _, tag := range tags {
		if ptr.Deref(tag.Scope, "") != namespaceMigrationVPCKeyTag {
			continue
		}
		key := ptr.Deref(tag.Tag, "")
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func uniqueVPCsByPath(vpcs []model.Vpc) []model.Vpc {
	unique := make([]model.Vpc, 0, len(vpcs))
	seen := make(map[string]struct{}, len(vpcs))
	for _, vpc := range vpcs {
		identity := ptr.Deref(vpc.Path, ptr.Deref(vpc.Id, ""))
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		unique = append(unique, vpc)
	}
	return unique
}

func listNamespaceMigrationVPCs(ctx context.Context, client *nsx.Client, orgID, projectID string) (map[string][]model.Vpc, error) {
	pageSize := int64(1000)
	includeConflicts, includeMarkedForDelete := false, false
	cursor := (*string)(nil)
	seenCursors := make(map[string]struct{})
	byKey := make(map[string][]model.Vpc)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := client.VPCClient.List(orgID, projectID, cursor, &includeConflicts, &includeMarkedForDelete, nil, &pageSize, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("listing VPCs in org %q project %q: %w", orgID, projectID, err)
		}
		for _, vpc := range page.Results {
			for _, key := range namespaceMigrationVPCKeysFromTags(vpc.Tags) {
				byKey[key] = append(byKey[key], vpc)
			}
		}
		if page.Cursor == nil || *page.Cursor == "" {
			return byKey, nil
		}
		if _, seen := seenCursors[*page.Cursor]; seen {
			return nil, fmt.Errorf("NSX VPC listing repeated pagination cursor %q", *page.Cursor)
		}
		seenCursors[*page.Cursor] = struct{}{}
		cursor = page.Cursor
	}
}

func hasNamespaceMigrationOwner(tags []model.Tag, owner string) bool {
	for _, tag := range tags {
		if ptr.Deref(tag.Scope, "") == namespaceMigrationOwnerTag && ptr.Deref(tag.Tag, "") == owner {
			return true
		}
	}
	return false
}

func namespaceNetworkPreview(networks []api.NamespaceNetworkPreview, vdnName string) *api.NamespaceNetworkPreview {
	for i := range networks {
		if networks[i].VDNName == vdnName {
			return &networks[i]
		}
	}
	return nil
}

func targetResource(network api.NamespaceNetworkPreview, kind string) *api.NamespaceTargetResourcePreview {
	for i := range network.TargetResources {
		if network.TargetResources[i].Kind == kind {
			return &network.TargetResources[i]
		}
	}
	return nil
}

func upsertTargetResource(network *api.NamespaceNetworkPreview, resource api.NamespaceTargetResourcePreview) {
	for i := range network.TargetResources {
		existing := network.TargetResources[i]
		if existing.Kind == resource.Kind && (existing.Decision == "Pending" || resource.Kind == "VPC" ||
			(existing.Path == resource.Path && existing.Name == resource.Name)) {
			network.TargetResources[i] = resource
			return
		}
	}
	network.TargetResources = append(network.TargetResources, resource)
}

func targetValidationSummary(dvpgs []api.NamespaceDVPGPreview) string {
	parts := make([]string, 0, len(dvpgs))
	for _, dvpg := range dvpgs {
		part := fmt.Sprintf("%s=%s", dvpg.Reference, dvpg.Compatibility)
		if len(dvpg.Warnings) != 0 {
			part += " (" + strings.Join(dvpg.Warnings, "; ") + ")"
		}
		parts = append(parts, part)
	}
	sort.Strings(parts)
	summary := "Target validation: " + strings.Join(parts, "; ")
	if runes := []rune(summary); len(runes) > 1800 {
		digest := sha256.Sum256([]byte(summary))
		return string(runes[:1700]) + fmt.Sprintf("… [%x]", digest[:8])
	}
	return summary
}

func plannedNamespaceResources(root *model.OrgRoot) ([]api.NamespaceTargetResourcePreview, error) {
	if root == nil {
		return nil, fmt.Errorf("NSX PLAN returned no conversion payload")
	}
	type resourceKey struct{ kind, path, name string }
	resources := make(map[resourceKey]api.NamespaceTargetResourcePreview)
	var walk func(data.DataValue)
	walk = func(value data.DataValue) {
		switch typed := value.(type) {
		case *data.StructValue:
			fields := typed.Fields()
			resourceType := stringField(fields, "resource_type")
			id := stringField(fields, "id")
			name := stringField(fields, "display_name")
			resourcePath := stringField(fields, "path")
			kind := migrationResourceKind(resourceType, typed.Name())
			if kind != "" && (id != "" || name != "" || resourcePath != "") {
				if name == "" {
					name = id
				}
				resources[resourceKey{kind: kind, path: resourcePath, name: name}] = api.NamespaceTargetResourcePreview{Kind: kind, Decision: "Create", Name: name, Path: resourcePath, Reason: "Proposed by NSX internal PLAN"}
			}
			for _, child := range fields {
				walk(child)
			}
		case *data.ListValue:
			for _, child := range typed.List() {
				walk(child)
			}
		case *data.OptionalValue:
			if typed.IsSet() {
				walk(typed.Value())
			}
		}
	}
	for _, child := range root.Children {
		walk(child)
	}
	orderedKeys := make([]resourceKey, 0, len(resources))
	for key := range resources {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Slice(orderedKeys, func(i, j int) bool {
		if orderedKeys[i].kind != orderedKeys[j].kind {
			return orderedKeys[i].kind < orderedKeys[j].kind
		}
		if orderedKeys[i].path != orderedKeys[j].path {
			return orderedKeys[i].path < orderedKeys[j].path
		}
		return orderedKeys[i].name < orderedKeys[j].name
	})
	result := make([]api.NamespaceTargetResourcePreview, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		result = append(result, resources[key])
	}
	return result, nil
}

func stringField(fields map[string]data.DataValue, name string) string {
	value, ok := fields[name].(*data.StringValue)
	if !ok {
		return ""
	}
	return value.Value()
}

func migrationResourceKind(resourceType, modelName string) string {
	value := strings.ToLower(resourceType + " " + modelName)
	switch {
	case strings.Contains(value, "distributedvlanconnection"):
		return "DVC"
	case strings.Contains(value, "vpcsubnet"):
		return "Subnet"
	case strings.Contains(value, "transitgateway"):
		return "TGW"
	case strings.Contains(value, "vpc"):
		return "VPC"
	default:
		return ""
	}
}
