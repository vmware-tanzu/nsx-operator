/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transition

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	sdkerrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/nsxworkflow"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/topology"
)

func TestIncompatibleVMReasonsUsesNetworkInterfaceCardType(t *testing.T) {
	vm := &unstructured.Unstructured{Object: map[string]interface{}{
		"spec": map[string]interface{}{"network": map[string]interface{}{"interfaces": []interface{}{
			map[string]interface{}{"type": "SRIOV"},
			map[string]interface{}{"type": "Dynamic.DirectPath-IO"},
			map[string]interface{}{"type": "Virtual_PCI_Passthrough"},
		}}},
	}}

	require.Equal(t, []string{
		"DynamicDirectPath I/O network adapter",
		"PCI passthrough network device",
		"SR-IOV virtual NIC",
	}, incompatibleVMReasons(vm))
}

func TestNamespacePreviewMessageDoesNotDuplicatePreviousPreview(t *testing.T) {
	status := &api.NamespaceNetworkTransitionStatus{NamespaceStatuses: []api.NamespaceStatus{{
		Name: "tenant", Message: "Workloads compatible. Source-only NSX validation succeeded. old preview",
	}}}
	networks := []topology.SourceNetwork{{VDNName: "vdn-a", Namespaces: []string{"tenant"}}}
	preview := map[string][]dvpgPreview{"vdn-a": {{
		DVPGRef: "/dvpg/new", Name: "new", VLANID: "42", Compatibility: "SUPPORTED",
	}}}

	message := namespacePreviewMessage(status, "tenant", networks, preview)
	require.Contains(t, message, "Workloads compatible.")
	require.Contains(t, message, "DVPG new")
	require.NotContains(t, message, "old preview")
	require.Equal(t, 1, strings.Count(message, "Source-only NSX validation succeeded."))
}

func TestSetPreviewConditionTruncatesLongMessage(t *testing.T) {
	status := &api.TransitionStatus{}
	setPreviewCondition(status, 1, metav1.ConditionFalse, "ResourceNamingInvalid", strings.Repeat("x", conditionMessageMaxBytes+100))
	condition := findCondition(status.Conditions, api.ConditionMigrationPreviewAvailable)
	require.NotNil(t, condition)
	require.LessOrEqual(t, len(condition.Message), conditionMessageMaxBytes)
	require.Contains(t, condition.Message, "truncated")
}

func TestNamespaceWorkloadCompatibilityReconcile(t *testing.T) {
	validVM := vmObject("compatible", "tenant-a", "VMXNET3")
	unsupportedVM := vmObject("unsupported", "tenant-b", "SR-IOV")
	reader := vmListReader{objects: []unstructured.Unstructured{validVM, unsupportedVM}}
	batch, status := namespaceEvaluateRequest()
	batch.Spec.Namespaces = []string{"tenant-a", "tenant-b"}
	batch.Status = &api.NamespaceNetworkTransitionStatus{}
	status.Conditions = nil

	result, err := (NamespaceWorkloadCompatibility{Reader: reader}).Reconcile(context.Background(), Request{
		Object: batch, Generation: 7, Status: status, NamespaceTopology: namespaceTopology(
			topology.SourceNetwork{VDNName: "vdn-a", VDSRef: "cm:dvs-1", DVPGRefs: []string{"dvpg-1"}, DHCPKnown: true, Namespaces: []string{"tenant-a", "tenant-b"}},
		),
	})

	var incompatible *IncompatibleWorkloadError
	require.ErrorAs(t, err, &incompatible)
	require.Equal(t, Outcome(""), result.Outcome)
	require.Contains(t, err.Error(), `VM "unsupported" (SR-IOV virtual NIC)`)
	require.Len(t, batch.Status.NamespaceStatuses, 2)
	require.Equal(t, "WorkloadsCompatible", batch.Status.NamespaceStatuses[0].Reason)
	require.Equal(t, api.PhaseEvaluating, batch.Status.NamespaceStatuses[0].Phase)
	require.Equal(t, "IncompatibleWorkloadDetected", batch.Status.NamespaceStatuses[1].Reason)
	require.Equal(t, api.PhaseFailed, batch.Status.NamespaceStatuses[1].Phase)
	require.Contains(t, batch.Status.NamespaceStatuses[1].Message, "unsupported")
	condition := findCondition(status.Conditions, api.ConditionWorkloadCompatibilityEvaluated)
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, int64(7), condition.ObservedGeneration)
}

func TestNamespaceWorkloadCompatibilityReconcileRejectsMissingVMReader(t *testing.T) {
	batch, status := namespaceEvaluateRequest()
	_, err := (NamespaceWorkloadCompatibility{}).Reconcile(context.Background(), Request{
		Object: batch, Generation: 2, Status: status, NamespaceTopology: namespaceTopology(
			topology.SourceNetwork{VDNName: "vdn-a", VDSRef: "cm:dvs-1", DVPGRefs: []string{"dvpg-1"}, DHCPKnown: true, Namespaces: []string{"tenant"}},
		),
	})
	require.EqualError(t, err, "VirtualMachine reader is not configured")
	condition := findCondition(status.Conditions, api.ConditionWorkloadCompatibilityEvaluated)
	require.NotNil(t, condition)
	require.Equal(t, "WorkloadCompatibilityCheckInProgress", condition.Reason)
}

func TestNamespaceWorkloadCompatibilityReconcileCompatibleVMs(t *testing.T) {
	reader := vmListReader{objects: []unstructured.Unstructured{
		vmObject("vm-a", "tenant-a", "VMXNET3"),
		vmObject("vm-b", "tenant-b", "VMXNET3"),
	}}
	batch, status := namespaceEvaluateRequest()
	batch.Spec.Namespaces = []string{"tenant-a", "tenant-b"}
	batch.Status = &api.NamespaceNetworkTransitionStatus{}
	result, err := (NamespaceWorkloadCompatibility{Reader: reader}).Reconcile(context.Background(), Request{
		Object: batch, Generation: 5, Status: status, NamespaceTopology: namespaceTopology(
			topology.SourceNetwork{VDNName: "vdn-a", VDSRef: "cm:dvs-1", DVPGRefs: []string{"dvpg-1"}, DHCPKnown: true, Namespaces: []string{"tenant-a", "tenant-b"}},
		),
	})

	require.NoError(t, err)
	require.Equal(t, OutcomeSucceeded, result.Outcome)
	require.Len(t, batch.Status.NamespaceStatuses, 2)
	for _, namespaceStatus := range batch.Status.NamespaceStatuses {
		require.Equal(t, api.PhaseEvaluating, namespaceStatus.Phase)
		require.Equal(t, "WorkloadsCompatible", namespaceStatus.Reason)
		require.Contains(t, namespaceStatus.Message, "Checked 1 VirtualMachine CRs")
	}
	condition := findCondition(status.Conditions, api.ConditionWorkloadCompatibilityEvaluated)
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Equal(t, "WorkloadsCompatible", condition.Reason)
	require.Equal(t, int64(5), condition.ObservedGeneration)
}

func TestNamespaceEvaluateReconcileUsesRecordedWorkflowAndStoresPreview(t *testing.T) {
	batch, status := namespaceEvaluateRequest()
	status.NSXWorkflows = []api.NSXWorkflowStatus{{VDNName: "vdn-a", WorkflowID: "recorded-workflow"}}
	status.Conditions = []metav1.Condition{{Type: api.ConditionMigrationPreviewAvailable, Status: metav1.ConditionFalse, Reason: "OldFailure"}}
	workflow := &namespaceWorkflowClientStub{
		status: successfulNamespaceValidationWorkflow("recorded-workflow", "cm:dvs-1"),
		results: []model.DvpgValidationResult{{
			DvpgRef: stringPointer("dvpg-1"), DvpgName: stringPointer("tenant-dvpg"),
			VlanId: stringPointer("42"), Compatibility: stringPointer("SUPPORTED"),
		}},
	}
	result, err := (NamespaceEvaluate{WorkflowClient: workflow}).Reconcile(context.Background(), Request{
		Object: batch, Generation: 9, Status: status, NamespaceTopology: namespaceTopology(
			topology.SourceNetwork{VDNName: "vdn-a", VDSRef: "cm:dvs-1", DVPGRefs: []string{"dvpg-1"}, DHCPKnown: true, Namespaces: []string{"tenant"}},
		),
	})

	require.NoError(t, err)
	require.Equal(t, OutcomeSucceeded, result.Outcome)
	require.Empty(t, workflow.putRequests, "a matching successful workflow must be reused without PUT")
	require.Equal(t, []string{"recorded-workflow"}, workflow.getIDs)
	require.Equal(t, []string{"recorded-workflow"}, workflow.resultIDs)
	require.Equal(t, api.PhaseEvaluated, batch.Status.NamespaceStatuses[0].Phase)
	require.Equal(t, "EvaluateCompleted", batch.Status.NamespaceStatuses[0].Reason)
	require.Contains(t, batch.Status.NamespaceStatuses[0].Message, "DVPG tenant-dvpg")
	require.Contains(t, batch.Status.NamespaceStatuses[0].Message, "VLAN 42")
	condition := findCondition(status.Conditions, api.ConditionMigrationPreviewAvailable)
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Equal(t, "SourceValidationSucceeded", condition.Reason)
}

func TestNamespaceEvaluateReconcileRejectsInvalidNSXScopeAndDVPGResults(t *testing.T) {
	tests := []struct {
		name      string
		status    model.DvpgToSubnetWorkflow
		results   []model.DvpgValidationResult
		wantError string
	}{
		{
			name: "unexpected VPC path",
			status: func() model.DvpgToSubnetWorkflow {
				workflow := successfulNamespaceValidationWorkflow("recorded-workflow", "cm:dvs-1")
				workflow.VpcPath = stringPointer("/orgs/o/projects/p/vpcs/vpc-1")
				return workflow
			}(),
			results:   []model.DvpgValidationResult{{DvpgRef: stringPointer("dvpg-1"), VlanId: stringPointer("42"), Compatibility: stringPointer("SUPPORTED")}},
			wantError: "unexpectedly targets VPC",
		},
		{
			name:      "extra DVPG result",
			status:    successfulNamespaceValidationWorkflow("recorded-workflow", "cm:dvs-1"),
			results:   []model.DvpgValidationResult{{DvpgRef: stringPointer("dvpg-1"), VlanId: stringPointer("42"), Compatibility: stringPointer("SUPPORTED")}, {DvpgRef: stringPointer("dvpg-extra"), VlanId: stringPointer("43"), Compatibility: stringPointer("SUPPORTED")}},
			wantError: "unexpected DVPG",
		},
		{
			name:      "missing DVPG result",
			status:    successfulNamespaceValidationWorkflow("recorded-workflow", "cm:dvs-1"),
			results:   nil,
			wantError: "omitted DVPG",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			batch, status := namespaceEvaluateRequest()
			workflow := &namespaceWorkflowClientStub{status: tc.status, results: tc.results}
			result, err := (NamespaceEvaluate{WorkflowClient: workflow}).Reconcile(context.Background(), Request{
				Object: batch, Generation: 4, Status: status, NamespaceTopology: namespaceTopology(
					topology.SourceNetwork{VDNName: "vdn-a", VDSRef: "cm:dvs-1", DVPGRefs: []string{"dvpg-1"}, DHCPKnown: true, Namespaces: []string{"tenant"}},
				),
			})
			require.NoError(t, err)
			require.Equal(t, OutcomeFailed, result.Outcome)
			require.Contains(t, result.FailureMessage, tc.wantError)
			require.Equal(t, api.PhaseFailed, batch.Status.NamespaceStatuses[0].Phase)
			condition := findCondition(status.Conditions, api.ConditionMigrationPreviewAvailable)
			require.NotNil(t, condition)
			require.Equal(t, metav1.ConditionFalse, condition.Status)
			require.Equal(t, "SourceValidationFailed", condition.Reason)
		})
	}
}

func TestNamespaceEvaluateWorkflowRetryAndRequeue(t *testing.T) {
	tests := []struct {
		name           string
		status         model.DvpgToSubnetWorkflow
		getError       error
		wantPut        bool
		wantRevision   *int64
		wantInProgress bool
		wantFailed     bool
		wantDetail     string
		wantRetriable  bool
		wantError      string
		putError       error
		afterPutStatus *model.DvpgToSubnetWorkflow
	}{
		{
			name:           "creates missing workflow and requeues",
			getError:       sdkerrors.NewNotFound(),
			wantPut:        true,
			wantInProgress: true,
		},
		{
			name: "reuses in progress workflow",
			status: func() model.DvpgToSubnetWorkflow {
				workflow := successfulNamespaceValidationWorkflow("recorded-workflow", "cm:dvs-1")
				workflow.WorkflowState.Status = stringPointer("IN_PROGRESS")
				return workflow
			}(),
			wantInProgress: true,
		},
		{
			name: "updates workflow with current revision when VDS changes",
			status: func() model.DvpgToSubnetWorkflow {
				workflow := successfulNamespaceValidationWorkflow("recorded-workflow", "cm:old-dvs")
				workflow.WorkflowState.Status = stringPointer("SUCCESS")
				return workflow
			}(),
			wantPut:        true,
			wantRevision:   ptr.To(int64(3)),
			wantInProgress: true,
		},
		{
			name: "requeues while NSX reports pending",
			status: func() model.DvpgToSubnetWorkflow {
				workflow := successfulNamespaceValidationWorkflow("recorded-workflow", "cm:dvs-1")
				workflow.WorkflowState.Status = stringPointer("PENDING")
				return workflow
			}(),
			wantInProgress: true,
		},
		{
			name: "treats failed workflow as terminal and preserves NSX errors",
			status: func() model.DvpgToSubnetWorkflow {
				workflow := successfulNamespaceValidationWorkflow("recorded-workflow", "cm:dvs-1")
				workflow.WorkflowState.Status = stringPointer("FAILED")
				workflow.WorkflowState.Errors = []string{"validation backend failure detail", "second backend detail"}
				return workflow
			}(),
			wantFailed: true,
			wantDetail: "second backend detail",
		},
		{
			name: "treats error workflow as terminal",
			status: func() model.DvpgToSubnetWorkflow {
				workflow := successfulNamespaceValidationWorkflow("recorded-workflow", "cm:dvs-1")
				workflow.WorkflowState.Status = stringPointer("ERROR")
				workflow.WorkflowState.Errors = []string{"terminal NSX error"}
				return workflow
			}(),
			wantFailed: true,
			wantDetail: "terminal NSX error",
		},
		{
			name:       "terminal non-not-found GET error becomes failed outcome",
			getError:   errors.New("NSX unavailable"),
			wantFailed: true,
			wantDetail: "NSX unavailable",
		},
		{
			name:          "propagates transient non-not-found GET error",
			getError:      MarkRetriable(errors.New("NSX temporarily unavailable")),
			wantError:     "NSX temporarily unavailable",
			wantRetriable: true,
		},
		{
			name: "retries stale revision after rereading updated workflow",
			status: func() model.DvpgToSubnetWorkflow {
				workflow := successfulNamespaceValidationWorkflow("recorded-workflow", "cm:old-dvs")
				workflow.WorkflowState.Status = stringPointer("SUCCESS")
				return workflow
			}(),
			wantPut:      true,
			wantRevision: ptr.To(int64(3)),
			putError:     errors.New("revision precondition failed"),
			afterPutStatus: func() *model.DvpgToSubnetWorkflow {
				workflow := successfulNamespaceValidationWorkflow("recorded-workflow", "cm:dvs-1")
				workflow.Revision = ptr.To(int64(4))
				workflow.WorkflowState.Status = stringPointer("IN_PROGRESS")
				return &workflow
			}(),
			wantError:     "revision precondition failed",
			wantRetriable: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			batch, status := namespaceEvaluateRequest()
			status.NSXWorkflows = []api.NSXWorkflowStatus{{VDNName: "vdn-a", WorkflowID: "recorded-workflow"}}
			workflow := &namespaceWorkflowClientStub{status: tc.status, getError: tc.getError, putError: tc.putError, afterPutStatus: tc.afterPutStatus}
			result, err := (NamespaceEvaluate{WorkflowClient: workflow}).Reconcile(context.Background(), Request{
				Object: batch, Generation: 4, Status: status, NamespaceTopology: namespaceTopology(
					topology.SourceNetwork{VDNName: "vdn-a", VDSRef: "cm:dvs-1", DVPGRefs: []string{"dvpg-1"}, DHCPKnown: true, Namespaces: []string{"tenant"}},
				),
			})
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				if tc.wantRetriable {
					require.True(t, IsRetriableError(err))
				}
			} else {
				require.NoError(t, err)
			}
			require.Len(t, workflow.putRequests, boolToInt(tc.wantPut))
			if tc.wantPut {
				request := workflow.putRequests[0]
				require.Equal(t, "recorded-workflow", ptr.Deref(request.Id, ""))
				require.Equal(t, nsxworkflow.DesiredStateValidation, ptr.Deref(request.DesiredState, ""))
				require.Equal(t, "cm:dvs-1", ptr.Deref(request.VdsRef, ""))
				require.Equal(t, tc.wantRevision, request.Revision)
			}
			if tc.wantInProgress {
				require.Equal(t, OutcomeInProgress, result.Outcome)
				require.Equal(t, namespaceValidationRequeueAfter, result.RequeueAfter)
				require.NotEqual(t, api.PhaseEvaluated, batch.Status.NamespaceStatuses[0].Phase)
			} else if tc.wantFailed {
				require.Equal(t, OutcomeFailed, result.Outcome)
				require.Zero(t, result.RequeueAfter)
				require.Empty(t, workflow.putRequests, "FAILED must not trigger an automatic PUT retry")
				require.Equal(t, api.PhaseFailed, batch.Status.NamespaceStatuses[0].Phase)
				require.Contains(t, batch.Status.NamespaceStatuses[0].Message, tc.wantDetail)
				previewCondition := findCondition(status.Conditions, api.ConditionMigrationPreviewAvailable)
				require.NotNil(t, previewCondition)
				require.Contains(t, previewCondition.Message, tc.wantDetail)
			} else if tc.wantError == "" {
				require.Equal(t, Outcome(""), result.Outcome)
			}
		})
	}
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type vmListReader struct {
	client.Reader
	objects []unstructured.Unstructured
	err     error
}

func (reader vmListReader) List(_ context.Context, list client.ObjectList, options ...client.ListOption) error {
	if reader.err != nil {
		return reader.err
	}
	vmList, ok := list.(*unstructured.UnstructuredList)
	if !ok {
		return errors.New("unexpected list type")
	}
	if vmList.GetAPIVersion() != "vmoperator.vmware.com/v1alpha6" || vmList.GetKind() != "VirtualMachineList" {
		return errors.New("unexpected VM API version or kind")
	}
	listOptions := &client.ListOptions{}
	for _, option := range options {
		option.ApplyToList(listOptions)
	}
	for _, object := range reader.objects {
		if listOptions.Namespace == "" || object.GetNamespace() == listOptions.Namespace {
			vmList.Items = append(vmList.Items, *object.DeepCopy())
		}
	}
	return nil
}

type namespaceWorkflowClientStub struct {
	putRequests    []model.DvpgToSubnetWorkflow
	putError       error
	status         model.DvpgToSubnetWorkflow
	getError       error
	afterPutStatus *model.DvpgToSubnetWorkflow
	afterPutError  error
	results        []model.DvpgValidationResult
	listError      error
	getIDs         []string
	resultIDs      []string
}

func (stub *namespaceWorkflowClientStub) Put(_ context.Context, request model.DvpgToSubnetWorkflow) error {
	stub.putRequests = append(stub.putRequests, request)
	return stub.putError
}

func (stub *namespaceWorkflowClientStub) GetStatus(_ context.Context, workflowID string) (model.DvpgToSubnetWorkflow, error) {
	stub.getIDs = append(stub.getIDs, workflowID)
	if len(stub.putRequests) != 0 && stub.afterPutStatus != nil {
		return *stub.afterPutStatus, stub.afterPutError
	}
	return stub.status, stub.getError
}

func (stub *namespaceWorkflowClientStub) ListValidationResults(_ context.Context, workflowID string, _ int) ([]model.DvpgValidationResult, error) {
	stub.resultIDs = append(stub.resultIDs, workflowID)
	return stub.results, stub.listError
}

func successfulNamespaceValidationWorkflow(workflowID, vdsRef string) model.DvpgToSubnetWorkflow {
	return model.DvpgToSubnetWorkflow{
		Id:           stringPointer(workflowID),
		DesiredState: stringPointer(nsxworkflow.DesiredStateValidation),
		VdsRef:       stringPointer(vdsRef),
		Revision:     ptr.To(int64(3)),
		WorkflowState: &model.DvpgToSubnetWorkflowState{
			Status: stringPointer("SUCCESS"),
		},
	}
}

func namespaceEvaluateRequest() (*api.NamespaceNetworkTransition, *api.TransitionStatus) {
	return &api.NamespaceNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{UID: types.UID("batch-uid")},
		Spec:       api.NamespaceNetworkTransitionSpec{Namespaces: []string{"tenant"}},
		Status:     &api.NamespaceNetworkTransitionStatus{NamespaceStatuses: []api.NamespaceStatus{{Name: "tenant", Phase: api.PhaseEvaluating, Reason: "WorkloadsCompatible", Message: "Checked workloads."}}},
	}, &api.TransitionStatus{}
}

func namespaceTopology(networks ...topology.SourceNetwork) *topology.BatchSnapshot {
	resources := make([]topology.ResourceIdentity, 0, len(networks))
	for _, network := range networks {
		resources = append(resources, topology.ResourceIdentity{ResourceType: topology.ResourceVDN, Key: "vdn:" + network.VDNName, VDNName: network.VDNName})
	}
	return &topology.BatchSnapshot{Complete: true, Resources: resources, SourceNetworks: networks}
}

func vmObject(name, namespace, ethernetCardType string) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "vmoperator.vmware.com/v1alpha6",
		"kind":       "VirtualMachine",
		"metadata":   map[string]interface{}{"name": name, "namespace": namespace},
		"spec": map[string]interface{}{"network": map[string]interface{}{"interfaces": []interface{}{
			map[string]interface{}{"type": ethernetCardType},
		}}},
	}}
}

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func stringPointer(value string) *string { return &value }

var _ client.Reader = vmListReader{}
