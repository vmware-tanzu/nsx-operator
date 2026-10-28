/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	extensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	crdvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	validation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"sigs.k8s.io/yaml"
)

// Exercise generated admission rules using the API server's CEL implementation,
// including oldSelf correlation by list-map key and its production cost budgets.
func TestInputAdmission(t *testing.T) {
	for _, stage := range []string{"controlplane", "namespaces"} {
		t.Run(stage, func(t *testing.T) {
			kind, key := "controlplanenetworktransitions", "controlPlaneAndServiceDHCPNetworks"
			if stage == "namespaces" {
				kind, key = "namespacenetworktransitions", "namespaceDHCPNetworks"
			}
			raw, err := os.ReadFile("../../../../build/yaml/crd/migration/migration.network.vmware.com_" + kind + ".yaml")
			require.NoError(t, err)
			var crd extensionsv1.CustomResourceDefinition
			require.NoError(t, yaml.Unmarshal(raw, &crd))
			extensionsv1.SetDefaults_CustomResourceDefinition(&crd)
			crd.Status.StoredVersions = []string{"v1alpha1"}
			var internalCRD apiextensions.CustomResourceDefinition
			require.NoError(t, extensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(&crd, &internalCRD, nil))
			require.Empty(t, crdvalidation.ValidateCustomResourceDefinition(context.Background(), &internalCRD), "the target API server must accept the generated CRD")
			if stage == "controlplane" {
				properties := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties
				require.NotContains(t, properties["spec"].Properties["networkConfiguration"].Properties, "aviConfig")
				allocation := properties["status"].Properties["lbSnatReservations"].Items.Schema.Properties["allocationRef"]
				require.NotContains(t, allocation.Properties, "uid")
				require.ElementsMatch(t, []string{"name", "namespace"}, allocation.Required)
			}
			var props apiextensions.JSONSchemaProps
			require.NoError(t, extensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(crd.Spec.Versions[0].Schema.OpenAPIV3Schema, &props, nil))
			subnetSchema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["networkConfiguration"].Properties[key].Items.Schema
			require.NotContains(t, subnetSchema.Properties, "subnetCidr")
			structural, err := schema.NewStructural(&props)
			require.NoError(t, err)
			validator := cel.NewValidator(structural, true, celconfig.PerCallLimit)
			require.NotNil(t, validator)
			openAPIValidator, _, err := validation.NewSchemaValidator(&props)
			require.NoError(t, err)
			raw, err = os.ReadFile("testdata/" + stage + ".yaml")
			require.NoError(t, err)
			var example map[string]any
			require.NoError(t, yaml.Unmarshal(raw, &example))
			delete(example, "status") // Illustrative conditions omit mandatory metav1 fields.
			// Return the original nested maps: NestedMap copies would discard test mutations.
			object := func(value any, path string) map[string]any {
				t.Helper()
				m, ok := value.(map[string]any)
				require.True(t, ok, "%s must be an object", path)
				return m
			}
			spec := func(o map[string]any) map[string]any { return object(o["spec"], "spec") }
			inputs := func(o map[string]any) map[string]any {
				return object(spec(o)["networkConfiguration"], "spec.networkConfiguration")
			}
			network := func(o map[string]any) map[string]any {
				t.Helper()
				entries, ok := inputs(o)[key].([]any)
				require.True(t, ok, "spec.networkConfiguration.%s must be a list", key)
				require.NotEmpty(t, entries, "spec.networkConfiguration.%s must contain a subnet", key)
				return object(entries[0], "spec.networkConfiguration."+key+"[0]")
			}
			subnet := func(o map[string]any) map[string]any {
				return network(o)
			}
			rangeInput := func(o map[string]any) map[string]any {
				return subnet(o)["dhcpRange"].(map[string]any)
			}
			identityKey := "vdnName"
			check := func(name string, old map[string]any, change func(map[string]any), valid bool) {
				t.Helper()
				t.Run(name, func(t *testing.T) {
					obj := runtime.DeepCopyJSON(example)
					if old != nil {
						obj = runtime.DeepCopyJSON(old)
					}
					change(obj)
					errs := validation.ValidateCustomResource(field.NewPath("resource"), obj, openAPIValidator)
					celErrors, _ := validator.Validate(context.Background(), field.NewPath("resource"), structural, obj, old, celconfig.RuntimeCELCostBudget)
					errs = append(errs, celErrors...)
					if valid {
						require.Empty(t, errs)
					} else {
						require.NotEmpty(t, errs)
					}
				})
			}
			// Supported release examples must also pass admission.
			for _, path := range []string{"testdata/" + stage + ".yaml", "../../../../build/yaml/samples/migration/" + stage + ".yaml"} {
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				var supported map[string]any
				require.NoError(t, yaml.Unmarshal(data, &supported))
				delete(supported, "status") // Creation inputs; design fixtures below validate complete handler outputs.
				check("supported example "+path, supported, func(map[string]any) {}, true)
			}
			fixtures, err := os.ReadDir("testdata")
			require.NoError(t, err)
			for _, fixture := range fixtures {
				prefix := "design-controlplane"
				if stage == "namespaces" {
					prefix = "design-namespaces"
				}
				if !strings.HasPrefix(fixture.Name(), prefix) {
					continue
				}
				raw, err := os.ReadFile("testdata/" + fixture.Name())
				require.NoError(t, err)
				var snapshot map[string]any
				require.NoError(t, yaml.Unmarshal(raw, &snapshot))
				check(fixture.Name(), nil, func(o map[string]any) {
					for k := range o {
						delete(o, k)
					}
					for k, v := range snapshot {
						o[k] = v
					}
				}, true)
			}
			check("unchanged", example, func(map[string]any) {}, true)

			check("creation", nil, func(map[string]any) {}, true)
			check("missing VDNName", nil, func(o map[string]any) { delete(network(o), "vdnName") }, false)
			check("invalid gateway", nil, func(o map[string]any) { subnet(o)["gatewayAddress"] = "invalid" }, false)
			check("invalid DHCP address", nil, func(o map[string]any) { rangeInput(o)["start"] = "invalid" }, false)
			for _, mask := range []string{"not-a-mask", "255.0.255.0", "255.255.255", "255.255.255.256", "ffff:ffff::", "24", "255.255.255.01"} {
				check("invalid mask "+mask, nil, func(o map[string]any) { subnet(o)["subnetMask"] = mask }, false)
			}
			for prefix := 0; prefix <= 32; prefix++ {
				mask := uint32(0)
				if prefix > 0 {
					mask = ^uint32(0) << (32 - prefix)
				}
				value := fmt.Sprintf("%d.%d.%d.%d", byte(mask>>24), byte(mask>>16), byte(mask>>8), byte(mask))
				check("valid mask "+value, nil, func(o map[string]any) { subnet(o)["subnetMask"] = value }, true)
			}

			if stage == "namespaces" {
				check("namespace status outside batch", nil, func(o map[string]any) {
					o["status"] = map[string]any{"namespaceStatuses": []any{map[string]any{"name": "outside-batch", "phase": "Completed"}}}
				}, false)
				check("partial namespace status", nil, func(o map[string]any) {
					name := spec(o)["namespaces"].([]any)[0]
					o["status"] = map[string]any{"namespaceStatuses": []any{map[string]any{"name": name, "phase": "Failed", "reason": "RebindFailed", "message": "VM timeout"}}}
				}, true)
				check("full namespace status at maximum batch size", nil, func(o map[string]any) {
					names, entries := make([]any, 1000), make([]any, 1000)
					for i := range names {
						name := fmt.Sprintf("namespace-%d", i)
						names[i], entries[i] = name, map[string]any{"name": name, "phase": "Completed"}
					}
					spec(o)["namespaces"] = names
					o["status"] = map[string]any{"namespaceStatuses": entries}
				}, true)
				for _, name := range []string{"Tenant_A", "tenant.name", "-tenant", "tenant-", "tenant/name", "tenant name"} {
					check("invalid namespace "+name, nil, func(o map[string]any) { spec(o)["namespaces"] = []any{name} }, false)
				}
				for _, name := range []string{"tenant", "tenant-01", "0", "a"} {
					check("valid namespace "+name, nil, func(o map[string]any) { spec(o)["namespaces"] = []any{name} }, true)
				}
			}
			check("rollback action scope", nil, func(o map[string]any) { spec(o)["action"] = "Rollback" }, stage == "namespaces")
			for _, legacyAction := range []string{"EVALUATE", "PLAN", "DERIVE", "EXECUTE", "ROLLBACK", "COMMIT", "FINALIZE"} {
				check("reject legacy uppercase action "+legacyAction, nil, func(o map[string]any) { spec(o)["action"] = legacyAction }, false)
			}
			check("commit action scope", nil, func(o map[string]any) { spec(o)["action"] = "Commit" }, stage == "namespaces")
			check("finalize action scope", nil, func(o map[string]any) { spec(o)["action"] = "Finalize" }, stage == "controlplane")
			// Infrastructure rollback is rejected before and after every checkpoint;
			// namespace rollback remains admitted before its irreversible Commit.
			for _, action := range []Action{ActionEvaluate, ActionPlan, ActionDerive, ActionExecute} {
				for _, state := range []ActionState{ActionStateRunning, ActionStateFailed, ActionStateSucceeded} {
					previous := runtime.DeepCopyJSON(example)
					spec(previous)["action"] = string(action)
					previous["status"] = map[string]any{"lastAction": string(action), "actionState": string(state)}
					check("rollback after "+string(action)+"/"+string(state), previous, func(o map[string]any) { spec(o)["action"] = "Rollback" }, stage == "namespaces")
					check("forward retry "+string(action)+"/"+string(state), previous, func(map[string]any) {}, true)
				}
			}
			checkpointAction := "Finalize"
			if stage == "namespaces" {
				checkpointAction = "Commit"
			}
			wrongCheckpoint := "Commit"
			if stage == "namespaces" {
				wrongCheckpoint = "Finalize"
			}
			check("reject checkpoint belonging to other kind", nil, func(o map[string]any) {
				spec(o)["action"] = checkpointAction
				o["status"] = map[string]any{"irreversibleAction": wrongCheckpoint}
			}, false)
			check("checkpoint racing with action reset", example, func(o map[string]any) {
				spec(o)["action"] = "Rollback"
				o["status"] = map[string]any{"lastAction": checkpointAction, "actionState": "Running", "irreversibleAction": checkpointAction}
			}, false)
			for _, state := range []ActionState{ActionStateRunning, ActionStateFailed, ActionStateSucceeded} {
				checkpointed := runtime.DeepCopyJSON(example)
				spec(checkpointed)["action"] = checkpointAction
				checkpointed["status"] = map[string]any{"lastAction": checkpointAction, "actionState": string(state), "irreversibleAction": checkpointAction}
				check(string(state)+" resume irreversible action", checkpointed, func(map[string]any) {}, true)
				check(string(state)+" cannot clear PONR", checkpointed, func(o map[string]any) { delete(o["status"].(map[string]any), "irreversibleAction") }, false)
				check(string(state)+" cannot replace PONR", checkpointed, func(o map[string]any) { o["status"].(map[string]any)["irreversibleAction"] = "Rollback" }, false)
				preflight := runtime.DeepCopyJSON(checkpointed)
				delete(preflight["status"].(map[string]any), "irreversibleAction")
				check(string(state)+" cancel before PONR", preflight, func(o map[string]any) { spec(o)["action"] = "Rollback" }, stage == "namespaces")
				for _, action := range []Action{ActionEvaluate, ActionPlan, ActionDerive, ActionExecute, ActionRollback} {
					check(string(state)+" reject reset to "+string(action), checkpointed, func(o map[string]any) { spec(o)["action"] = string(action) }, false)
				}
			}
			// Corrections and removals are permitted throughout evaluation. The
			// same mutations must be rejected once Plan is requested or checkpointed.
			corrections := []struct {
				name  string
				apply func(map[string]any)
			}{
				{"replace gateway", func(o map[string]any) { subnet(o)["gatewayAddress"] = "10.2.1.254" }},
				{"replace mask", func(o map[string]any) { subnet(o)["subnetMask"] = "255.255.0.0" }},
				{"replace DHCP start", func(o map[string]any) { rangeInput(o)["start"] = "10.2.1.110" }},
				{"replace DHCP end", func(o map[string]any) { rangeInput(o)["end"] = "10.2.1.210" }},
				{"remove DHCP start", func(o map[string]any) { delete(rangeInput(o), "start") }},
				{"remove DHCP end", func(o map[string]any) { delete(rangeInput(o), "end") }},
				{"remove range", func(o map[string]any) { delete(subnet(o), "dhcpRange") }},
				{"remove mask", func(o map[string]any) { delete(subnet(o), "subnetMask") }},
				{"remove gateway", func(o map[string]any) { delete(subnet(o), "gatewayAddress") }},
				{"replace VDN", func(o map[string]any) { subnet(o)[identityKey] = "corrected-network" }},
				{"remove network entry", func(o map[string]any) { inputs(o)[key] = []any{} }},
				{"remove network list", func(o map[string]any) { delete(inputs(o), key) }},
				{"remove configuration", func(o map[string]any) { delete(spec(o), "networkConfiguration") }},
			}
			if stage == "controlplane" {
				corrections = append(corrections, struct {
					name  string
					apply func(map[string]any)
				}{
					"replace frontend port group", func(o map[string]any) { inputs(o)["legacyLbFrontendDvpgs"] = []any{"corrected-portgroup"} },
				}, struct {
					name  string
					apply func(map[string]any)
				}{
					"remove frontend port groups", func(o map[string]any) { delete(inputs(o), "legacyLbFrontendDvpgs") },
				})
			}
			for _, state := range []ActionState{"", ActionStateRunning, ActionStateFailed, ActionStateSucceeded} {
				evaluating := runtime.DeepCopyJSON(example)
				spec(evaluating)["action"] = "Evaluate"
				if state != "" {
					evaluating["status"] = map[string]any{"lastAction": "Evaluate", "actionState": string(state)}
				}
				for _, correction := range corrections {
					check("Evaluate/"+string(state)+"/"+correction.name, evaluating, correction.apply, true)
				}
				check("Evaluate/"+string(state)+" rejects invalid correction", evaluating, func(o map[string]any) { subnet(o)["gatewayAddress"] = "invalid" }, false)
			}
			// Requested actions freeze inputs before the controller checkpoints Plan.
			for _, pending := range []bool{false, true} {
				for _, withStatus := range []bool{false, true} {
					base := runtime.DeepCopyJSON(example)
					spec(base)["action"] = "Evaluate"
					if pending {
						spec(base)["action"] = "Plan"
					}
					if withStatus {
						base["status"] = map[string]any{"lastAction": "Evaluate", "actionState": "Succeeded"}
					}
					label := fmt.Sprintf("request freeze pending=%t status=%t", pending, withStatus)
					for _, correction := range corrections {
						check(label+"/"+correction.name, base, func(o map[string]any) { spec(o)["action"] = "Plan"; correction.apply(o) }, false)
					}
					check(label+" unchanged Plan", base, func(o map[string]any) { spec(o)["action"] = "Plan" }, true)
					check(label+" append subnet", base, func(o map[string]any) {
						spec(o)["action"] = "Plan"
						inputs(o)[key] = append(inputs(o)[key].([]any), map[string]any{"vdnName": "new"})
					}, false)
					partial := runtime.DeepCopyJSON(base)
					delete(subnet(partial), "gatewayAddress")
					check(label+" fill subnet", partial, func(o map[string]any) { spec(o)["action"] = "Plan"; subnet(o)["gatewayAddress"] = "10.2.1.1" }, false)
					empty := runtime.DeepCopyJSON(base)
					delete(spec(empty), "networkConfiguration")
					check(label+" initial inputs", empty, func(o map[string]any) { spec(o)["action"] = "Plan"; spec(o)["networkConfiguration"] = inputs(example) }, false)
					if stage == "controlplane" {
						for _, selection := range []struct{ field, value string }{{"targetLbProvider", "NSX_LB"}, {"nsxProjectId", "different-project"}} {
							omitted := runtime.DeepCopyJSON(base)
							delete(spec(omitted), selection.field)
							check(label+" select "+selection.field, omitted, func(o map[string]any) { spec(o)["action"] = "Plan"; spec(o)[selection.field] = selection.value }, false)
							check(label+" evaluate "+selection.field, omitted, func(o map[string]any) { spec(o)["action"] = "Evaluate"; spec(o)[selection.field] = selection.value }, true)
						}
						check(label+" remove provider", base, func(o map[string]any) { spec(o)["action"] = "Plan"; delete(spec(o), "targetLbProvider") }, false)
					}
				}
			}
			lockedInputs := runtime.DeepCopyJSON(example)
			lockedInputs["status"] = map[string]any{"lastAction": "Plan", "actionState": "Succeeded"}
			delete(subnet(lockedInputs), "gatewayAddress")
			check("returning to Evaluate does not unlock inputs", lockedInputs, func(o map[string]any) { spec(o)["action"] = "Evaluate"; subnet(o)["gatewayAddress"] = "10.2.1.1" }, false)

			// Once Plan starts, inputs stay fixed except when Evaluate follows a
			// successfully completed namespace rollback.
			for _, action := range []Action{ActionPlan, ActionDerive, ActionExecute, ActionRollback, ActionCommit, ActionFinalize} {
				if (stage == "namespaces" && action == ActionFinalize) || (stage == "controlplane" && (action == ActionCommit || action == ActionRollback)) {
					continue
				}
				for _, state := range []ActionState{ActionStateRunning, ActionStateSucceeded, ActionStateFailed} {
					locked := runtime.DeepCopyJSON(example)
					spec(locked)["action"] = string(action)
					locked["status"] = map[string]any{"lastAction": string(action), "actionState": string(state)}
					label := string(action) + "/" + string(state)
					canReopen := stage == "namespaces" && action == ActionRollback && state == ActionStateSucceeded
					for _, correction := range corrections {
						check(label+"/"+correction.name, locked, correction.apply, false)
						check(label+"/return Evaluate/"+correction.name, locked, func(o map[string]any) { spec(o)["action"] = "Evaluate"; correction.apply(o) }, canReopen)
					}
					check(label+" unchanged", locked, func(map[string]any) {}, true)
					check(label+" append subnet", locked, func(o map[string]any) {
						inputs(o)[key] = append(inputs(o)[key].([]any), map[string]any{identityKey: "new"})
					}, false)
					partial := runtime.DeepCopyJSON(locked)
					delete(subnet(partial), "gatewayAddress")
					check(label+" fill subnet", partial, func(o map[string]any) { subnet(o)["gatewayAddress"] = "10.2.1.1" }, false)
					missing := runtime.DeepCopyJSON(locked)
					delete(spec(missing), "networkConfiguration")
					check(label+" supply inputs", missing, func(o map[string]any) { spec(o)["networkConfiguration"] = inputs(example) }, false)
					if stage == "controlplane" {
						delete(spec(locked), "nsxProjectId")
						check(label+" select different project", locked, func(o map[string]any) { spec(o)["nsxProjectId"] = "different-project"; spec(o)["action"] = "Execute" }, false)
						check(label+" remove provider", locked, func(o map[string]any) { delete(spec(o), "targetLbProvider") }, false)

					}
				}
			}

			if stage == "namespaces" {
				for _, state := range []ActionState{"", ActionStateRunning, ActionStateFailed, ActionStateSucceeded} {
					rolledBack := runtime.DeepCopyJSON(example)
					spec(rolledBack)["action"] = "Rollback"
					rolledBack["status"] = map[string]any{"lastAction": "Rollback"}
					if state != "" {
						rolledBack["status"].(map[string]any)["actionState"] = string(state)
					}
					label := "rollback recovery/" + string(state)
					check(label+" add network during Evaluate", rolledBack, func(o map[string]any) {
						spec(o)["action"] = "Evaluate"
						inputs(o)[key] = append(inputs(o)[key].([]any), map[string]any{identityKey: "new"})
					}, state == ActionStateSucceeded)
					check(label+" cannot claim success while editing", rolledBack, func(o map[string]any) {
						spec(o)["action"] = "Evaluate"
						o["status"].(map[string]any)["actionState"] = "Succeeded"
						subnet(o)["gatewayAddress"] = "10.2.1.254"
					}, state == ActionStateSucceeded)
					// Support changing the action first and correcting inputs in a later update.
					spec(rolledBack)["action"] = "Evaluate"
					for _, correction := range corrections {
						check(label+" already requested Evaluate/"+correction.name, rolledBack, correction.apply, state == ActionStateSucceeded)
					}
					check(label+" invalid correction", rolledBack, func(o map[string]any) { subnet(o)["gatewayAddress"] = "invalid" }, false)
					check(label+" cannot edit while requesting Plan", rolledBack, func(o map[string]any) {
						spec(o)["action"] = "Plan"
						subnet(o)["gatewayAddress"] = "10.2.1.254"
					}, false)
					delete(spec(rolledBack), "networkConfiguration")
					check(label+" supply missing configuration", rolledBack, func(o map[string]any) {
						spec(o)["networkConfiguration"] = inputs(example)
					}, state == ActionStateSucceeded)
				}
				// Even an inconsistent stored rollback-success status must not bypass Commit.
				committed := runtime.DeepCopyJSON(example)
				spec(committed)["action"] = "Commit"
				committed["status"] = map[string]any{"lastAction": "Rollback", "actionState": "Succeeded", "irreversibleAction": "Commit"}
				check("rollback success cannot bypass irreversible checkpoint", committed, func(o map[string]any) {
					spec(o)["action"] = "Evaluate"
					subnet(o)["gatewayAddress"] = "10.2.1.254"
				}, false)
			}

			partial := runtime.DeepCopyJSON(example)
			partial["status"] = map[string]any{"lastAction": "Evaluate", "actionState": "Running"}
			delete(subnet(partial), "gatewayAddress")
			delete(rangeInput(partial), "end")
			check("fill partial subnet", partial, func(o map[string]any) {
				spec(o)["action"] = "Evaluate"
				subnet(o)["gatewayAddress"] = "10.2.1.1"
				rangeInput(o)["end"] = "10.2.1.200"
			}, true)
			check("remove supplied DHCP bound", partial, func(o map[string]any) { spec(o)["action"] = "Evaluate"; delete(rangeInput(o), "start") }, true)
			empty := runtime.DeepCopyJSON(example)
			delete(spec(empty), "networkConfiguration")
			check("supply initial inputs", empty, func(o map[string]any) {
				spec(o)["action"] = "Evaluate"
				spec(o)["networkConfiguration"] = inputs(example)
			}, true)
			check("append subnet", example, func(o map[string]any) {
				spec(o)["action"] = "Evaluate"
				inputs(o)[key] = append(inputs(o)[key].([]any), map[string]any{identityKey: "new"})
			}, true)
			// At the schema's maximum size, immutability must still allow legitimate updates.
			large := runtime.DeepCopyJSON(example)
			list := make([]any, 1000)
			for i := range list {
				list[i] = map[string]any{identityKey: fmt.Sprintf("vdn-%d", i), "gatewayAddress": "10.2.1.1"}
			}
			inputs(large)[key] = list
			large["status"] = map[string]any{"lastAction": "Plan", "actionState": "Succeeded"}
			check("maximum list unchanged", large, func(map[string]any) {}, true)
			check("maximum list reordered", large, func(o map[string]any) {
				l := inputs(o)[key].([]any)
				for i, j := 0, len(l)-1; i < j; i, j = i+1, j-1 {
					l[i], l[j] = l[j], l[i]
				}
			}, true)
			check("maximum list changed", large, func(o map[string]any) { subnet(o)["gatewayAddress"] = "10.2.1.254" }, false)
			if stage == "controlplane" {
				for _, tc := range []struct {
					field   string
					valid   string
					other   string
					invalid []string
				}{
					{"lbSnatIPv4", "10.2.1.50", "10.2.1.51", []string{"", "invalid", "2001:db8::50", "10.2.1.50/24", "256.1.1.1"}},
					{"lbSnatIPv6", "2001:db8:2::50", "2001:db8:2::51", []string{"", "invalid", "10.2.1.50", "2001:db8:2::50/64", "fe80::50%eth0", "::ffff:10.2.1.50"}},
				} {
					check(tc.field+" valid alone", nil, func(o map[string]any) {
						spec(o)["targetLbProvider"] = "NSX_LB"
						inputs(o)[tc.field] = tc.valid
					}, true)
					for _, invalid := range tc.invalid {
						check(tc.field+" invalid "+invalid, nil, func(o map[string]any) {
							spec(o)["targetLbProvider"] = "NSX_LB"
							inputs(o)[tc.field] = invalid
						}, false)
					}
					check(tc.field+" rejects unsupported target", nil, func(o map[string]any) { spec(o)["targetLbProvider"] = "AVI"; inputs(o)[tc.field] = tc.valid }, false)
					check(tc.field+" permits unresolved target", nil, func(o map[string]any) {
						delete(spec(o), "targetLbProvider")
						inputs(o)[tc.field] = tc.valid
					}, true)
					fallback := runtime.DeepCopyJSON(example)
					spec(fallback)["targetLbProvider"] = "NSX_LB"
					check(tc.field+" add during evaluation", fallback, func(o map[string]any) { inputs(o)[tc.field] = tc.valid }, true)
					for _, action := range []string{"Plan", "Derive", "Execute"} {
						check(tc.field+" cannot add with "+action, fallback, func(o map[string]any) {
							spec(o)["action"] = action
							inputs(o)[tc.field] = tc.valid
						}, false)
					}
					inputs(fallback)[tc.field] = tc.valid
					check(tc.field+" unchanged", fallback, func(map[string]any) {}, true)
					check(tc.field+" correct before reservation", fallback, func(o map[string]any) { inputs(o)[tc.field] = tc.other }, true)
					check(tc.field+" remove before reservation", fallback, func(o map[string]any) { delete(inputs(o), tc.field) }, true)
					family, peerField := "IPv4", "lbSnatIPv6"
					if tc.field == "lbSnatIPv6" {
						family, peerField = "IPv6", "lbSnatIPv4"
					}
					reservation := map[string]any{"ipFamily": family, "ipAddress": tc.valid,
						"allocationRef": map[string]any{"namespace": "vmware-system-netop", "name": "lb-snat-" + family}}
					accepted := runtime.DeepCopyJSON(fallback)
					accepted["status"] = map[string]any{"lbSnatReservations": []any{reservation}}
					check(tc.field+" reservation acceptance", nil, func(o map[string]any) {
						spec(o)["targetLbProvider"] = "NSX_LB"
						inputs(o)[tc.field] = tc.valid
						o["status"] = accepted["status"]
					}, true)
					check(tc.field+" accepted unchanged", accepted, func(map[string]any) {}, true)
					check(tc.field+" accepted cannot replace", accepted, func(o map[string]any) { inputs(o)[tc.field] = tc.other }, false)
					check(tc.field+" accepted cannot remove", accepted, func(o map[string]any) { delete(inputs(o), tc.field) }, false)
					check(tc.field+" accepted cannot remove configuration", accepted, func(o map[string]any) { delete(spec(o), "networkConfiguration") }, false)
					check(tc.field+" accepted permits unrelated network correction", accepted, func(o map[string]any) { subnet(o)["gatewayAddress"] = "10.2.1.254" }, true)
					check(tc.field+" accepted cannot clear status", accepted, func(o map[string]any) { delete(o, "status") }, false)
					check(tc.field+" accepted cannot clear reservations", accepted, func(o map[string]any) { delete(object(o["status"], "status"), "lbSnatReservations") }, false)
					check(tc.field+" accepted cannot remove entry", accepted, func(o map[string]any) { object(o["status"], "status")["lbSnatReservations"] = []any{} }, false)
					check(tc.field+" accepted cannot rebind allocation", accepted, func(o map[string]any) {
						r := object(o["status"], "status")["lbSnatReservations"].([]any)[0].(map[string]any)
						r["allocationRef"].(map[string]any)["name"] = "replacement-allocation"
					}, false)
					check(tc.field+" accepted cannot change project", accepted, func(o map[string]any) { spec(o)["nsxProjectId"] = "other" }, false)
					check(tc.field+" accepted cannot change provider", accepted, func(o map[string]any) {
						delete(spec(o), "targetLbProvider")
					}, false)
					check(tc.field+" unreserved peer can be supplied", accepted, func(o map[string]any) {
						value := "10.2.1.52"
						if peerField == "lbSnatIPv6" {
							value = "2001:db8::52"
						}
						inputs(o)[peerField] = value
					}, true)
					automatic := runtime.DeepCopyJSON(accepted)
					delete(inputs(automatic), tc.field)
					check(tc.field+" automatic reservation prevents new fallback", automatic, func(o map[string]any) { inputs(o)[tc.field] = tc.valid }, false)
					withoutInputs := runtime.DeepCopyJSON(automatic)
					delete(spec(withoutInputs), "networkConfiguration")
					check(tc.field+" automatic reservation without inputs unchanged", withoutInputs, func(map[string]any) {}, true)
					check(tc.field+" automatic reservation permits peer with new inputs object", withoutInputs, func(o map[string]any) {
						value := "10.2.1.52"
						if peerField == "lbSnatIPv6" {
							value = "2001:db8::52"
						}
						spec(o)["networkConfiguration"] = map[string]any{peerField: value}
					}, true)
					check(tc.field+" automatic reservation rejects fallback with new inputs object", withoutInputs, func(o map[string]any) {
						spec(o)["networkConfiguration"] = map[string]any{tc.field: tc.valid}
					}, false)
					missingProject := runtime.DeepCopyJSON(accepted)
					delete(spec(missingProject), "nsxProjectId")
					check(tc.field+" accepted cannot select project", missingProject, func(o map[string]any) { spec(o)["nsxProjectId"] = "other" }, false)
					unresolved := runtime.DeepCopyJSON(accepted)
					delete(spec(unresolved), "targetLbProvider")
					check(tc.field+" accepted cannot select provider", unresolved, func(o map[string]any) { spec(o)["targetLbProvider"] = "NSX_LB" }, false)
					delete(inputs(fallback), tc.field)
					fallback["status"] = map[string]any{"lastAction": "Plan", "actionState": "Succeeded"}
					check(tc.field+" cannot add after Plan", fallback, func(o map[string]any) { inputs(o)[tc.field] = tc.valid }, false)
				}
				reservationData, err := os.ReadFile("testdata/controlplane-nsx-lb-reserved.yaml")
				require.NoError(t, err)
				var reserved map[string]any
				require.NoError(t, yaml.Unmarshal(reservationData, &reserved))
				check("dual stack reserved fixture", reserved, func(map[string]any) {}, true)
				check("dual stack reservation reorder", reserved, func(o map[string]any) {
					list := object(o["status"], "status")["lbSnatReservations"].([]any)
					list[0], list[1] = list[1], list[0]
				}, true)
				check("cannot remove accepted family", reserved, func(o map[string]any) {
					status := object(o["status"], "status")
					status["lbSnatReservations"] = status["lbSnatReservations"].([]any)[:1]
				}, false)
				check("dual stack LB SNAT", nil, func(o map[string]any) {
					spec(o)["targetLbProvider"] = "NSX_LB"
					inputs(o)["lbSnatIPv4"] = "10.2.1.50"
					inputs(o)["lbSnatIPv6"] = "2001:db8:2::50"
				}, true)
				for _, provider := range []string{"AVI", "NSX_LB", "VSPHERE_FOUNDATION"} {
					valid := provider == "NSX_LB"
					check("target provider "+provider, nil, func(o map[string]any) { spec(o)["targetLbProvider"] = provider }, valid)
					check("resolved target provider "+provider, nil, func(o map[string]any) { o["status"] = map[string]any{"targetLbProvider": provider} }, valid)
				}
				check("reject unsupported source provider", nil, func(o map[string]any) { o["status"] = map[string]any{"detectedSourceLbProvider": "AVI"} }, false)
				check("FLB source provider", nil, func(o map[string]any) { o["status"] = map[string]any{"detectedSourceLbProvider": "VSPHERE_FOUNDATION"} }, true)
				check("singleton", example, func(o map[string]any) { o["metadata"].(map[string]any)["name"] = "another" }, false)
				check("change project", example, func(o map[string]any) { spec(o)["nsxProjectId"] = "other" }, false)
				check("remove project", example, func(o map[string]any) { delete(spec(o), "nsxProjectId") }, false)
				empty := runtime.DeepCopyJSON(example)
				delete(spec(empty), "nsxProjectId")
				check("supply project", empty, func(o map[string]any) { spec(o)["nsxProjectId"] = "project" }, true)
				for _, path := range [][]string{{"legacyLbFrontendDvpgs"}} {
					check("remove "+fmt.Sprint(path), example, func(o map[string]any) { unstructured.RemoveNestedField(inputs(o), path...) }, true)
					check("replace "+fmt.Sprint(path), example, func(o map[string]any) {
						require.NoError(t, unstructured.SetNestedSlice(inputs(o), []any{"replacement"}, path...))
					}, true)
					check("append "+fmt.Sprint(path), example, func(o map[string]any) {
						l, _, err := unstructured.NestedSlice(inputs(o), path...)
						require.NoError(t, err)
						require.NoError(t, unstructured.SetNestedSlice(inputs(o), append(l, "new"), path...))
					}, true)
				}
			}
		})
	}
}
