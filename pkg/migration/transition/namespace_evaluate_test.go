/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transition

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/nsxworkflow"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/topology"
)

func TestIncompatibleVMReasonsUsesNetworkInterfaceCardType(t *testing.T) {
	vm := &unstructured.Unstructured{Object: map[string]interface{}{
		"spec": map[string]interface{}{
			"networkInterfaces": []interface{}{
				map[string]interface{}{"ethernetCardType": "SR-IOV"},
				map[string]interface{}{"ethernetCardType": "Dynamic.DirectPath-IO"},
				map[string]interface{}{"ethernetCardType": "Virtual_PCI_Passthrough"},
			},
		},
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
		status: model.DvpgToSubnetWorkflow{Id: stringPointer("recorded-workflow")},
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
	require.Len(t, workflow.putRequests, 1)
	require.Equal(t, "recorded-workflow", *workflow.putRequests[0].Id)
	require.Equal(t, nsxworkflow.DesiredStateValidation, *workflow.putRequests[0].DesiredState)
	require.Equal(t, "cm:dvs-1", *workflow.putRequests[0].VdsRef)
	require.Nil(t, workflow.putRequests[0].VpcPath, "source-only validation must omit the target VPC path")
	require.Equal(t, []string{"recorded-workflow"}, workflow.waitedIDs)
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
			name:      "unexpected VPC path",
			status:    model.DvpgToSubnetWorkflow{VpcPath: stringPointer("/orgs/o/projects/p/vpcs/vpc-1")},
			results:   []model.DvpgValidationResult{{DvpgRef: stringPointer("dvpg-1"), VlanId: stringPointer("42"), Compatibility: stringPointer("SUPPORTED")}},
			wantError: "unexpectedly targets VPC",
		},
		{
			name:      "extra DVPG result",
			status:    model.DvpgToSubnetWorkflow{},
			results:   []model.DvpgValidationResult{{DvpgRef: stringPointer("dvpg-1"), VlanId: stringPointer("42"), Compatibility: stringPointer("SUPPORTED")}, {DvpgRef: stringPointer("dvpg-extra"), VlanId: stringPointer("43"), Compatibility: stringPointer("SUPPORTED")}},
			wantError: "unexpected DVPG",
		},
		{
			name:      "missing DVPG result",
			status:    model.DvpgToSubnetWorkflow{},
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
			require.ErrorContains(t, err, tc.wantError)
			require.Equal(t, Outcome(""), result.Outcome)
			condition := findCondition(status.Conditions, api.ConditionMigrationPreviewAvailable)
			require.NotNil(t, condition)
			require.Equal(t, metav1.ConditionFalse, condition.Status)
			require.Equal(t, "SourceValidationFailed", condition.Reason)
		})
	}
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
	putRequests []model.DvpgToSubnetWorkflow
	putError    error
	status      model.DvpgToSubnetWorkflow
	waitError   error
	results     []model.DvpgValidationResult
	listError   error
	waitedIDs   []string
	resultIDs   []string
}

func (stub *namespaceWorkflowClientStub) Put(_ context.Context, request model.DvpgToSubnetWorkflow) error {
	stub.putRequests = append(stub.putRequests, request)
	return stub.putError
}

func (stub *namespaceWorkflowClientStub) WaitForCompletion(_ context.Context, workflowID, _ string, _ nsxworkflow.DesiredState) (model.DvpgToSubnetWorkflow, error) {
	stub.waitedIDs = append(stub.waitedIDs, workflowID)
	return stub.status, stub.waitError
}

func (stub *namespaceWorkflowClientStub) ListValidationResults(_ context.Context, workflowID string, _ int) ([]model.DvpgValidationResult, error) {
	stub.resultIDs = append(stub.resultIDs, workflowID)
	return stub.results, stub.listError
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
		"apiVersion": "vmoperator.vmware.com/v1alpha1",
		"kind":       "VirtualMachine",
		"metadata":   map[string]interface{}{"name": name, "namespace": namespace},
		"spec": map[string]interface{}{"networkInterfaces": []interface{}{
			map[string]interface{}{"ethernetCardType": ethernetCardType},
		}},
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
