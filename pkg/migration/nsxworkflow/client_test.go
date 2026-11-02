/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsxworkflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdkerrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	policyclient "github.com/vmware/vsphere-automation-sdk-go/runtime/protocol/client"
	nsxt "github.com/vmware/vsphere-automation-sdk-go/services/nsxt"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/workflows"
	"k8s.io/utils/ptr"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

const targetVPC = "/orgs/default/projects/default/vpcs/workload-vpc"

func sdkClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	connector := policyclient.NewConnector(server.URL, policyclient.UsingRest(nil), policyclient.WithHttpClient(server.Client()))
	client := NewClient(&nsx.Client{WorkflowsClient: nsxt.NewWorkflowsClient(connector), WorkflowValidationResultsClient: workflows.NewDvpg_validation_resultsClient(connector)})
	client.PollInterval = time.Millisecond
	return client
}

func TestWorkflowLifecycleUsesSDKWireContract(t *testing.T) {
	var methods []string
	client := sdkClient(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		require.Equal(t, "/policy/api/v1/workflows/workflow-a", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodPut {
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, "DvpgToSubnetWorkflow", body["resource_type"])
			require.Equal(t, false, body["auto_commit"])
			require.Equal(t, float64(3), body["_revision"])
			specs := body["dvpg_spec"].([]any)
			spec := specs[0].(map[string]any)
			require.Equal(t, "subnet-a", spec["subnet_config"].(map[string]any)["subnet_id"])
			require.Equal(t, "dvc-a", spec["distributed_vlan_connection_config"].(map[string]any)["distributed_vlan_connection_id"])
		}
		_, _ = w.Write([]byte(`{"id":"workflow-a","resource_type":"DvpgToSubnetWorkflow","desired_state":"PLAN","workflow_state":{"status":"SUCCESS"}}`))
	})
	request := model.DvpgToSubnetWorkflow{Id: ptr.To("workflow-a"), DesiredState: ptr.To(DesiredStatePlan), Revision: ptr.To(int64(3)), DvpgSpec: []model.DvpgToSubnetDvpgSpec{{DvpgRef: ptr.To("dvpg-a"), SubnetConfig: &model.DvpgToSubnetConfig{SubnetId: ptr.To("subnet-a")}, DistributedVlanConnectionConfig: &model.DvpgToSubnetDistributedVlanConnectionConfig{DistributedVlanConnectionId: ptr.To("dvc-a")}}}}
	require.NoError(t, client.Put(context.Background(), request))
	status, err := client.GetStatus(context.Background(), "workflow-a")
	require.NoError(t, err)
	require.Equal(t, "SUCCESS", *status.WorkflowState.Status)
	require.NoError(t, client.Delete(context.Background(), "workflow-a"))
	require.Equal(t, []string{"PUT", "GET", "DELETE"}, methods)
}

func TestValidationPaginationUsesSDK(t *testing.T) {
	calls := 0
	client := sdkClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/policy/api/v1/workflows/workflow-a/dvpg_validation_results", r.URL.Path)
		require.Equal(t, "100", r.URL.Query().Get("page_size"))
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(`{"results":[{"dvpg_ref":"dvpg-1"}],"cursor":"next page"}`))
			return
		}
		require.Equal(t, "next page", r.URL.Query().Get("cursor"))
		_, _ = w.Write([]byte(`{"results":[{"dvpg_ref":"dvpg-2"}]}`))
	})
	results, err := client.ListValidationResults(context.Background(), "workflow-a", 100)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, "dvpg-2", *results[1].DvpgRef)
}

func TestValidationRejectsRepeatedCursor(t *testing.T) {
	client := sdkClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cursor":"same"}`))
	})
	_, err := client.ListValidationResults(context.Background(), "workflow-a", 100)
	require.ErrorContains(t, err, "repeated pagination cursor")
}

func TestPollingWaitsForRequestedPhaseAndResources(t *testing.T) {
	calls := 0
	client := sdkClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		desired, execution, resource := "CONVERSION", "SUCCESS", "COMPLETED"
		switch calls {
		case 1:
			desired = "VALIDATION"
		case 2:
			execution = "IN_PROGRESS"
		case 3:
			resource = "IN_PROGRESS"
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"id": "workflow-a", "resource_type": "DvpgToSubnetWorkflow", "desired_state": desired, "vpc_path": targetVPC, "workflow_state": map[string]any{"status": execution, "migration_status": map[string]any{"nsx_object": []any{map[string]any{"policy_path": targetVPC + "/subnets/subnet-a", "resource_type": "VpcSubnet", "state": resource}}}}}))
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := client.WaitForCompletion(ctx, "workflow-a", targetVPC, DesiredStateConversion)
	require.NoError(t, err)
	require.Equal(t, 4, calls)
}

func TestValidationCompletesWithoutResourceProgress(t *testing.T) {
	client := sdkClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"workflow-a","resource_type":"DvpgToSubnetWorkflow","desired_state":"VALIDATION","vpc_path":"` + targetVPC + `","workflow_state":{"status":"SUCCESS"}}`))
	})
	_, err := client.WaitForCompletion(context.Background(), "workflow-a", targetVPC, DesiredStateValidation)
	require.NoError(t, err)
}

func TestScopeAndResourceFailure(t *testing.T) {
	status := model.DvpgToSubnetWorkflow{VpcPath: ptr.To(targetVPC), WorkflowState: &model.DvpgToSubnetWorkflowState{MigrationStatus: &model.DvpgToSubnetMigrationStatus{NsxObject: []model.DvpgToSubnetNsxObjectMigrationStatus{{PolicyPath: ptr.To(targetVPC + "-other/subnets/a"), ResourceType: ptr.To("VpcSubnet"), State: ptr.To("FAILED")}}}}}
	require.ErrorContains(t, ValidateTargetScope(status, targetVPC), "outside target VPC")
	status.WorkflowState.MigrationStatus.NsxObject[0].PolicyPath = ptr.To(targetVPC + "/subnets/a")
	require.NoError(t, ValidateTargetScope(status, targetVPC))
	_, err := resourcesComplete(status)
	require.ErrorContains(t, err, "failed")
	status.WorkflowState.MigrationStatus.NsxObject[0].State = ptr.To("UNKNOWN")
	_, err = resourcesComplete(status)
	require.ErrorContains(t, err, "unknown state")
}

func TestAutoCommitGuardAndCancellation(t *testing.T) {
	client := sdkClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected SDK call") })
	require.Error(t, client.Put(context.Background(), model.DvpgToSubnetWorkflow{Id: ptr.To("workflow-a"), DesiredState: ptr.To(DesiredStatePlan), AutoCommit: ptr.To(true)}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, client.Delete(ctx, "workflow-a"), context.Canceled)

}

// Workflow ID and request validity are checked by the SDK and NSX.
func TestWorkflowValidationIsDelegatedToNSX(t *testing.T) {
	calls := 0
	client := sdkClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error_code":500030,"error_message":"invalid workflow"}`))
	})
	err := client.Put(context.Background(), model.DvpgToSubnetWorkflow{DesiredState: ptr.To("INVALID")})
	require.Error(t, err)
	require.Equal(t, 1, calls, "NSX must validate the request")
}

func TestGetRejectsWrongIdentityAndPreservesSDKErrors(t *testing.T) {
	client := sdkClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"other","resource_type":"DvpgToSubnetWorkflow"}`))
	})
	_, err := client.GetStatus(context.Background(), "workflow-a")
	require.ErrorContains(t, err, "unexpected workflow identity")
	client = sdkClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error_code":202,"error_message":"not found"}`))
	})
	err = client.Delete(context.Background(), "workflow-a")
	require.IsType(t, sdkerrors.NotFound{}, err)
}

func TestPolicyPathMustBeCanonical(t *testing.T) {
	for _, invalidPath := range []string{"/orgs/default/projects/default/vpcs/vpc-1/../vpc-2", "/orgs/default/projects/default/vpcs/vpc-1?query=1", "/orgs/default/projects/default/vpcs/vpc-1/%2e%2e/vpc-2", "//orgs/default/projects/default/vpcs/vpc-1"} {
		require.False(t, validPolicyPath(invalidPath))
	}
	require.False(t, pathIsWithin(targetVPC, targetVPC))
}
