package networkinfo

import (
	"context"
	"fmt"
	"reflect"
	"slices"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	client "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	svccommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func deleteFail(r *NetworkInfoReconciler, c *context.Context, o *v1alpha1.NetworkInfo, e *error, client client.Client) {
	setNetworkInfoVPCStatus(c, o, client, nil)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailDelete, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, common.MetricResTypeNetworkInfo)
}

func updateFail(r *NetworkInfoReconciler, c *context.Context, o *v1alpha1.NetworkInfo, e *error, client client.Client, vpcState *v1alpha1.VPCState) {
	setNetworkInfoVPCStatus(c, o, client, vpcState)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailUpdate, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResType)
}

func updateSuccess(r *NetworkInfoReconciler, c *context.Context, o *v1alpha1.NetworkInfo, client client.Client,
	vpcState *v1alpha1.VPCState, ncName string, subnetPath string) {
	setNetworkInfoVPCStatus(c, o, client, vpcState)
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "NetworkInfo CR has been successfully updated")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, common.MetricResTypeNetworkInfo)
}

func deleteSuccess(r *NetworkInfoReconciler, _ *context.Context, o *v1alpha1.NetworkInfo) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulDelete, "NetworkInfo CR has been successfully deleted")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, common.MetricResTypeNetworkInfo)
}

func setNetworkInfoVPCStatus(ctx *context.Context, networkInfo *v1alpha1.NetworkInfo, client client.Client, createdVPC *v1alpha1.VPCState) {
	// if createdVPC is empty, remove the VPC from networkInfo
	if createdVPC == nil {
		networkInfo.VPCs = []v1alpha1.VPCState{}
		client.Update(*ctx, networkInfo)
		return
	}
	existingVPC := &v1alpha1.VPCState{}
	if len(networkInfo.VPCs) > 0 {
		existingVPC = &networkInfo.VPCs[0]
	}
	slices.Sort(existingVPC.PrivateIPs)
	slices.Sort(createdVPC.PrivateIPs)
	if reflect.DeepEqual(*existingVPC, *createdVPC) {
		return
	}
	networkInfo.VPCs = []v1alpha1.VPCState{*createdVPC}
	client.Update(*ctx, networkInfo)
	return
}

func setVPCNetworkConfigurationStatusWithLBS(ctx *context.Context, client client.Client, ncName string, vpcName string, aviSubnetPath string, nsxLBSPath string) {
	// read v1alpha1.VPCNetworkConfiguration by ncName
	nc := &v1alpha1.VPCNetworkConfiguration{}
	err := client.Get(*ctx, apitypes.NamespacedName{Name: ncName}, nc)
	if err != nil {
		log.Error(err, "failed to get VPCNetworkConfiguration", "Name", ncName)
	}
	createdVPCInfo := &v1alpha1.VPCInfo{
		Name:                vpcName,
		AVISESubnetPath:     aviSubnetPath,
		NSXLoadBalancerPath: nsxLBSPath,
	}
	// iterate through VPCNetworkConfiguration.Status.VPCs, if vpcName already exists, update it
	for i, vpc := range nc.Status.VPCs {
		if vpc.Name == vpcName {
			nc.Status.VPCs[i] = *createdVPCInfo
			client.Status().Update(*ctx, nc)
			return
		}
	}
	// else append the new VPCInfo
	if nc.Status.VPCs == nil {
		nc.Status.VPCs = []v1alpha1.VPCInfo{}
	}
	nc.Status.VPCs = append(nc.Status.VPCs, *createdVPCInfo)
	client.Status().Update(*ctx, nc)
}

func setVPCNetworkConfigurationStatusWithGatewayConnection(ctx *context.Context, client client.Client, nc *v1alpha1.VPCNetworkConfiguration, gatewayConnectionReady bool, reason string) {
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.GatewayConnectionReady,
			Status:             v1.ConditionFalse,
			Reason:             reason,
			LastTransitionTime: metav1.Time{},
		},
	}
	if gatewayConnectionReady {
		newConditions[0].Status = v1.ConditionTrue
	}
	conditionsUpdated := false
	for i := range newConditions {
		if mergeStatusCondition(ctx, &nc.Status.Conditions, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		client.Status().Update(*ctx, nc)
		log.Info("set VPCNetworkConfiguration status", "ncName", nc.Name, "condition", newConditions[0])
	}
}

func setVPCNetworkConfigurationStatusWithSnatEnabled(ctx *context.Context, client client.Client, nc *v1alpha1.VPCNetworkConfiguration, autoSnatEnabled bool) {
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.AutoSnatEnabled,
			Status:             v1.ConditionFalse,
			LastTransitionTime: metav1.Time{},
		},
	}
	if autoSnatEnabled {
		newConditions[0].Status = v1.ConditionTrue
	}
	conditionsUpdated := false
	for i := range newConditions {
		if mergeStatusCondition(ctx, &nc.Status.Conditions, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		client.Status().Update(*ctx, nc)
	}
}

// TODO: abstract the logic of merging condition for common, which can be used by the other controller, e.g. security policy
func mergeStatusCondition(ctx *context.Context, conditions *[]v1alpha1.Condition, newCondition *v1alpha1.Condition) bool {
	existingCondition := getExistingConditionOfType(newCondition.Type, *conditions)
	if existingCondition != nil {
		// Don't compare the timestamp.
		existingCondition.LastTransitionTime = metav1.Time{}
	}

	if reflect.DeepEqual(existingCondition, newCondition) {
		log.V(2).Info("conditions already match", "New Condition", newCondition, "Existing Condition", existingCondition)
		return false
	}

	if existingCondition != nil {
		existingCondition.Reason = newCondition.Reason
		existingCondition.Message = newCondition.Message
		existingCondition.Status = newCondition.Status
		existingCondition.LastTransitionTime = metav1.Now()
	} else {
		newCondition.LastTransitionTime = metav1.Now()
		*conditions = append(*conditions, *newCondition)
	}
	return true
}

func getExistingConditionOfType(conditionType v1alpha1.ConditionType, existingConditions []v1alpha1.Condition) *v1alpha1.Condition {
	for i := range existingConditions {
		if existingConditions[i].Type == conditionType {
			return &existingConditions[i]
		}
	}
	return nil
}

func getGatewayConnectionStatus(ctx *context.Context, nc *v1alpha1.VPCNetworkConfiguration) (bool, string, error) {
	gatewayConnectionReady := false
	reason := ""
	for _, condition := range nc.Status.Conditions {
		if condition.Type != v1alpha1.GatewayConnectionReady {
			continue
		}
		if condition.Status == v1.ConditionTrue {
			gatewayConnectionReady = true
			reason = condition.Reason
			break
		}
	}
	return gatewayConnectionReady, reason, nil
}

func getNamespaceFromNSXVPC(nsxVPC *model.Vpc) string {
	tags := nsxVPC.Tags
	for _, tag := range tags {
		if *tag.Scope == svccommon.TagScopeNamespace {
			return *tag.Tag
		}
	}
	return ""
}
