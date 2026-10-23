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
			kind, key := "controlplanenetworktransitions", "cpVmServiceSubnets"
			if stage == "namespaces" {
				kind, key = "namespacenetworktransitions", "tenantSubnets"
			}
			raw, err := os.ReadFile("../../../../build/yaml/crd/migration/iaas.vmware.com_" + kind + ".yaml")
			require.NoError(t, err)
			var crd extensionsv1.CustomResourceDefinition
			require.NoError(t, yaml.Unmarshal(raw, &crd))
			extensionsv1.SetDefaults_CustomResourceDefinition(&crd)
			crd.Status.StoredVersions = []string{"v1alpha1"}
			var internalCRD apiextensions.CustomResourceDefinition
			require.NoError(t, extensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(&crd, &internalCRD, nil))
			require.Empty(t, crdvalidation.ValidateCustomResourceDefinition(context.Background(), &internalCRD), "the target API server must accept the generated CRD")
			var props apiextensions.JSONSchemaProps
			require.NoError(t, extensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(crd.Spec.Versions[0].Schema.OpenAPIV3Schema, &props, nil))
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
			inputs := func(o map[string]any) map[string]any { return object(spec(o)["userInputs"], "spec.userInputs") }
			network := func(o map[string]any) map[string]any {
				t.Helper()
				entries, ok := inputs(o)[key].([]any)
				require.True(t, ok, "spec.userInputs.%s must be a list", key)
				require.NotEmpty(t, entries, "spec.userInputs.%s must contain a subnet", key)
				return object(entries[0], "spec.userInputs."+key+"[0]")
			}
			subnet := func(o map[string]any) map[string]any {
				return network(o)
			}
			rangeInput := func(o map[string]any) map[string]any {
				return subnet(o)["dhcpRange"].(map[string]any)
			}
			identityKey := "vdn"
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
			check("missing VDN", nil, func(o map[string]any) { delete(network(o), "vdn") }, false)
			check("invalid CIDR", nil, func(o map[string]any) { subnet(o)["subnetCidr"] = "invalid" }, false)
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
				for _, name := range []string{"Tenant_A", "tenant.name", "-tenant", "tenant-", "tenant/name", "tenant name"} {
					check("invalid namespace "+name, nil, func(o map[string]any) { spec(o)["namespaces"] = []any{name} }, false)
				}
				for _, name := range []string{"tenant", "tenant-01", "0", "a"} {
					check("valid namespace "+name, nil, func(o map[string]any) { spec(o)["namespaces"] = []any{name} }, true)
				}
			}
			check("commit action scope", nil, func(o map[string]any) { spec(o)["action"] = "COMMIT" }, stage == "namespaces")
			check("finalize action scope", nil, func(o map[string]any) { spec(o)["action"] = "FINALIZE" }, stage == "controlplane")
			checkpointAction := "FINALIZE"
			if stage == "namespaces" {
				checkpointAction = "COMMIT"
			}
			check("checkpoint racing with action reset", example, func(o map[string]any) {
				spec(o)["action"] = "ROLLBACK"
				o["status"] = map[string]any{"lastAction": checkpointAction, "actionState": "Running", "irreversibleAction": checkpointAction}
			}, false)
			for _, state := range []ActionState{ActionStateRunning, ActionStateFailed, ActionStateSucceeded} {
				checkpointed := runtime.DeepCopyJSON(example)
				spec(checkpointed)["action"] = checkpointAction
				checkpointed["status"] = map[string]any{"lastAction": checkpointAction, "actionState": string(state), "irreversibleAction": checkpointAction}
				check(string(state)+" resume irreversible action", checkpointed, func(map[string]any) {}, true)
				check(string(state)+" cannot clear PONR", checkpointed, func(o map[string]any) { delete(o["status"].(map[string]any), "irreversibleAction") }, false)
				check(string(state)+" cannot replace PONR", checkpointed, func(o map[string]any) { o["status"].(map[string]any)["irreversibleAction"] = "ROLLBACK" }, false)
				preflight := runtime.DeepCopyJSON(checkpointed)
				delete(preflight["status"].(map[string]any), "irreversibleAction")
				check(string(state)+" cancel before PONR", preflight, func(o map[string]any) { spec(o)["action"] = "ROLLBACK" }, true)
				for _, action := range []Action{ActionEvaluate, ActionPlan, ActionDerive, ActionExecute, ActionRollback} {
					check(string(state)+" reject reset to "+string(action), checkpointed, func(o map[string]any) { spec(o)["action"] = string(action) }, false)
				}
			}
			// Once PLAN starts, every action/state keeps the reviewed inputs fixed.
			for _, action := range []Action{ActionPlan, ActionDerive, ActionExecute, ActionRollback, ActionCommit, ActionFinalize} {
				if (stage == "namespaces" && action == ActionFinalize) || (stage == "controlplane" && action == ActionCommit) {
					continue
				}
				for _, state := range []ActionState{ActionStateRunning, ActionStateSucceeded, ActionStateFailed} {
					locked := runtime.DeepCopyJSON(example)
					spec(locked)["action"] = string(action)
					locked["status"] = map[string]any{"lastAction": string(action), "actionState": string(state)}
					label := string(action) + "/" + string(state)
					check(label+" unchanged", locked, func(map[string]any) {}, true)
					check(label+" append subnet", locked, func(o map[string]any) {
						inputs(o)[key] = append(inputs(o)[key].([]any), map[string]any{identityKey: "new"})
					}, false)
					partial := runtime.DeepCopyJSON(locked)
					delete(subnet(partial), "gatewayAddress")
					check(label+" fill subnet", partial, func(o map[string]any) { subnet(o)["gatewayAddress"] = "10.2.1.1" }, false)
					missing := runtime.DeepCopyJSON(locked)
					delete(spec(missing), "userInputs")
					check(label+" supply inputs", missing, func(o map[string]any) { spec(o)["userInputs"] = inputs(example) }, false)
					if stage == "controlplane" {
						delete(spec(locked), "nsxProjectId")
						check(label+" select different project", locked, func(o map[string]any) { spec(o)["nsxProjectId"] = "different-project"; spec(o)["action"] = "EXECUTE" }, false)
						check(label+" change provider", locked, func(o map[string]any) { spec(o)["targetLbProvider"] = "NSX_LB" }, false)
						check(label+" append Avi range", locked, func(o map[string]any) {
							avi := inputs(o)["aviConfig"].(map[string]any)
							avi["newVipRanges"] = append(avi["newVipRanges"].([]any), "192.168.101.0/24")
						}, false)
					}
				}
			}

			check("remove inputs", example, func(o map[string]any) { delete(spec(o), "userInputs") }, false)
			check("remove subnet list", example, func(o map[string]any) { delete(inputs(o), key) }, false)
			check("remove subnet entry", example, func(o map[string]any) { inputs(o)[key] = []any{} }, false)
			check("replace subnet key", example, func(o map[string]any) { network(o)[identityKey] = "other" }, false)
			fields := []string{"gatewayAddress", "subnetMask", "dhcpRange"}
			for _, f := range fields {
				check("remove "+f, example, func(o map[string]any) { delete(subnet(o), f) }, false)
			}
			check("change gateway", example, func(o map[string]any) { subnet(o)["gatewayAddress"] = "10.2.1.254" }, false)
			check("change DHCP bound", example, func(o map[string]any) { rangeInput(o)["end"] = "10.2.1.210" }, false)
			partial := runtime.DeepCopyJSON(example)
			partial["status"] = map[string]any{"lastAction": "EVALUATE", "actionState": "Running"}
			delete(subnet(partial), "gatewayAddress")
			delete(rangeInput(partial), "end")
			check("fill partial subnet", partial, func(o map[string]any) {
				subnet(o)["gatewayAddress"] = "10.2.1.1"
				rangeInput(o)["end"] = "10.2.1.200"
			}, true)
			check("remove supplied DHCP bound", partial, func(o map[string]any) { delete(rangeInput(o), "start") }, false)
			empty := runtime.DeepCopyJSON(example)
			delete(spec(empty), "userInputs")
			check("supply initial inputs", empty, func(o map[string]any) { spec(o)["userInputs"] = inputs(example) }, true)
			check("append subnet", example, func(o map[string]any) {
				inputs(o)[key] = append(inputs(o)[key].([]any), map[string]any{identityKey: "new"})
			}, true)
			// At the schema's maximum size, immutability must still allow legitimate updates.
			large := runtime.DeepCopyJSON(example)
			list := make([]any, 1000)
			for i := range list {
				list[i] = map[string]any{identityKey: fmt.Sprintf("vdn-%d", i), "gatewayAddress": "10.2.1.1"}
			}
			inputs(large)[key] = list
			large["status"] = map[string]any{"lastAction": "PLAN", "actionState": "Succeeded"}
			check("maximum list unchanged", large, func(map[string]any) {}, true)
			check("maximum list reordered", large, func(o map[string]any) {
				l := inputs(o)[key].([]any)
				for i, j := 0, len(l)-1; i < j; i, j = i+1, j-1 {
					l[i], l[j] = l[j], l[i]
				}
			}, true)
			check("maximum list changed", large, func(o map[string]any) { subnet(o)["gatewayAddress"] = "10.2.1.254" }, false)
			if stage == "controlplane" {
				for _, provider := range []string{"AVI", "NSX_LB", "VSPHERE_FOUNDATION"} {
					valid := provider != "VSPHERE_FOUNDATION"
					check("target provider "+provider, nil, func(o map[string]any) { spec(o)["targetLbProvider"] = provider }, valid)
					check("resolved target provider "+provider, nil, func(o map[string]any) { o["status"] = map[string]any{"targetLbProvider": provider} }, valid)
				}
				check("FLB source provider", nil, func(o map[string]any) { o["status"] = map[string]any{"detectedSourceLbProvider": "VSPHERE_FOUNDATION"} }, true)
				check("singleton", example, func(o map[string]any) { o["metadata"].(map[string]any)["name"] = "another" }, false)
				check("change project", example, func(o map[string]any) { spec(o)["nsxProjectId"] = "other" }, false)
				check("remove project", example, func(o map[string]any) { delete(spec(o), "nsxProjectId") }, false)
				empty := runtime.DeepCopyJSON(example)
				delete(spec(empty), "nsxProjectId")
				check("supply project", empty, func(o map[string]any) { spec(o)["nsxProjectId"] = "project" }, true)
				check("remove Avi configuration", example, func(o map[string]any) { delete(inputs(o), "aviConfig") }, false)
				for _, path := range [][]string{{"legacyLbFrontendDvpgs"}, {"aviConfig", "legacyVipRanges"}, {"aviConfig", "newVipRanges"}, {"aviConfig", "newFrontendDvpgs"}} {
					check("remove "+fmt.Sprint(path), example, func(o map[string]any) { unstructured.RemoveNestedField(inputs(o), path...) }, false)
					check("replace "+fmt.Sprint(path), example, func(o map[string]any) {
						require.NoError(t, unstructured.SetNestedSlice(inputs(o), []any{"replacement"}, path...))
					}, false)
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
