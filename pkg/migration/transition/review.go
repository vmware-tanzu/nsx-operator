/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transition

import (
	"fmt"
	"math"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

// RequireNamespacePreviewReview marks a materially changed Execute-time preview
// for a new caller review. The Execute handler must call this before returning
// OutcomeNeedsReview, and must not begin source cutover until the current
// reviewRevision is acknowledged in metadata.annotations.
func RequireNamespacePreviewReview(batch *api.NamespaceNetworkTransition, generation int64) error {
	if batch == nil || batch.Spec.Action != api.ActionExecute || batch.Status == nil || batch.Status.EvaluationPreview == nil {
		return fmt.Errorf("namespace Execute review requires an existing evaluation preview")
	}
	preview := batch.Status.EvaluationPreview
	if !namespacePreviewMatchesExecute(preview, generation) || preview.ReviewRevision < 1 {
		return fmt.Errorf("namespace Execute review requires a current, versioned evaluation preview")
	}
	if preview.ReviewRevision == math.MaxInt64 {
		return fmt.Errorf("namespace evaluation preview reviewRevision is exhausted")
	}
	preview.ReviewRevision++
	preview.ReviewRequired = true
	return nil
}

// PublishNamespaceExecutionPreview compares target-specific results with the
// Evaluate preview, writes the latest view to status, and requests another
// review when a target resource decision materially changed. Execute handlers
// must call it before any source cutover.
func PublishNamespaceExecutionPreview(batch *api.NamespaceNetworkTransition, generation int64, actual *api.NamespaceEvaluationPreview) (bool, error) {
	if batch == nil || batch.Status == nil || batch.Status.EvaluationPreview == nil || actual == nil {
		return false, fmt.Errorf("NamespaceNetworkTransition and both evaluation previews are required")
	}
	previous := batch.Status.EvaluationPreview
	if !namespacePreviewMatchesExecute(previous, generation) || actual.Generation != previous.Generation || previous.ReviewRevision < 1 {
		return false, fmt.Errorf("cannot compare stale or unversioned namespace evaluation previews")
	}
	actual.ReviewRevision = previous.ReviewRevision
	actual.ReviewRequired = false
	actual.MaterialChanges = CompareNamespaceTargetResources(previous.Networks, actual.Networks)
	if len(actual.MaterialChanges) != 0 {
		batch.Status.EvaluationPreview = actual
		if err := RequireNamespacePreviewReview(batch, generation); err != nil {
			return false, err
		}
		return true, nil
	}
	batch.Status.EvaluationPreview = actual
	return false, nil
}

// The action update from Evaluate to Execute advances the CR generation once.
// Admission requires the Evaluate checkpoint to match the previous generation,
// and the CRD freezes the evaluated network configuration during Execute.
func namespacePreviewMatchesExecute(preview *api.NamespaceEvaluationPreview, generation int64) bool {
	return preview != nil && generation > 1 && preview.Generation == generation-1
}

// CompareNamespaceTargetResources reports additions, removals and changes to
// target reuse/create decisions. A resource's explanation text may change
// without making its identity or decision materially different, except for the
// VPC's stable target-validation summary, whose change requires renewed review.
func CompareNamespaceTargetResources(previous, current []api.NamespaceNetworkPreview) []api.NamespacePreviewChange {
	type resourceKey struct{ vdnName, kind string }
	collect := func(networks []api.NamespaceNetworkPreview) map[resourceKey][]api.NamespaceTargetResourcePreview {
		resources := make(map[resourceKey][]api.NamespaceTargetResourcePreview)
		for _, network := range networks {
			for _, resource := range network.TargetResources {
				key := resourceKey{vdnName: network.VDNName, kind: resource.Kind}
				resources[key] = append(resources[key], resource)
			}
		}
		return resources
	}
	before, after := collect(previous), collect(current)
	keys := make(map[resourceKey]struct{}, len(before)+len(after))
	for key := range before {
		keys[key] = struct{}{}
	}
	for key := range after {
		keys[key] = struct{}{}
	}
	ordered := make([]resourceKey, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].vdnName == ordered[j].vdnName {
			return ordered[i].kind < ordered[j].kind
		}
		return ordered[i].vdnName < ordered[j].vdnName
	})
	changes := make([]api.NamespacePreviewChange, 0)
	for _, key := range ordered {
		oldResources, newResources := before[key], after[key]
		less := func(a, b api.NamespaceTargetResourcePreview) bool {
			if a.Path != b.Path {
				return a.Path < b.Path
			}
			if a.Name != b.Name {
				return a.Name < b.Name
			}
			return a.Decision < b.Decision
		}
		sort.Slice(oldResources, func(i, j int) bool { return less(oldResources[i], oldResources[j]) })
		sort.Slice(newResources, func(i, j int) bool { return less(newResources[i], newResources[j]) })
		matched := make([]bool, len(newResources))
		var removed []api.NamespaceTargetResourcePreview
		for _, old := range oldResources {
			found := false
			for i, current := range newResources {
				validationChanged := key.kind == "VPC" && strings.HasPrefix(old.Reason, "Target validation:") && strings.HasPrefix(current.Reason, "Target validation:") && old.Reason != current.Reason
				if !matched[i] && old.Decision == current.Decision && old.Name == current.Name && old.Path == current.Path && !validationChanged {
					matched[i], found = true, true
					break
				}
			}
			if !found {
				removed = append(removed, old)
			}
		}
		var added []api.NamespaceTargetResourcePreview
		for i, current := range newResources {
			if !matched[i] {
				added = append(added, current)
			}
		}
		for i := 0; i < len(removed) || i < len(added); i++ {
			change := api.NamespacePreviewChange{VDNName: key.vdnName, Kind: key.kind}
			if i < len(removed) {
				old := removed[i]
				change.Previous = &old
			}
			if i < len(added) {
				current := added[i]
				change.Current = &current
			}
			changes = append(changes, change)
		}
	}
	return changes
}

// AcknowledgeNamespacePreviewReview writes the current preview revision as the
// user's review checkpoint. Callers normally acknowledge by setting the
// reviewed-preview-revision metadata annotation; this helper is for integrations
// that build the transition update in-process.
func AcknowledgeNamespacePreviewReview(batch *api.NamespaceNetworkTransition) error {
	if batch == nil || batch.Status == nil || batch.Status.EvaluationPreview == nil {
		return fmt.Errorf("namespace evaluation preview is required")
	}
	preview := batch.Status.EvaluationPreview
	if !preview.ReviewRequired || preview.ReviewRevision < 1 {
		return fmt.Errorf("namespace evaluation preview is not awaiting review")
	}
	if batch.Annotations == nil {
		batch.Annotations = make(map[string]string)
	}
	batch.Annotations[api.AnnotationReviewedPreviewRevision] = fmt.Sprintf("%d", preview.ReviewRevision)
	return nil
}

// ReviewCondition describes whether the current evaluation preview can proceed.
func ReviewCondition(generation int64, awaiting bool, revision int64) metav1.Condition {
	condition := metav1.Condition{
		Type: api.ConditionMigrationPreviewAvailable, Status: metav1.ConditionTrue,
		ObservedGeneration: generation,
	}
	if awaiting {
		condition.Reason = "ReviewRequired"
		condition.Message = fmt.Sprintf("A material target difference is available in status.evaluationPreview revision %d; set metadata.annotations[%q] to %d after review before cutover", revision, api.AnnotationReviewedPreviewRevision, revision)
	} else {
		condition.Reason = "ReviewAcknowledged"
		condition.Message = fmt.Sprintf("Evaluation preview revision %d was acknowledged", revision)
	}
	return condition
}
