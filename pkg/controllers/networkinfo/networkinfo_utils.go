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
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	svccommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func deleteFail(r *NetworkInfoReconciler, c context.Context, o *v1alpha1.NetworkInfo, e *error, client client.Client) {
	setNetworkInfoVPCStatus(c, o, client, nil)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailDelete, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, common.MetricResTypeNetworkInfo)
}

func updateFail(r *NetworkInfoReconciler, c context.Context, o *v1alpha1.NetworkInfo, e *error, client client.Client, vpcState *v1alpha1.VPCState) {
	setNetworkInfoVPCStatus(c, o, client, vpcState)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailUpdate, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResType)
}

func updateSuccess(r *NetworkInfoReconciler, c context.Context, o *v1alpha1.NetworkInfo, client client.Client,
	vpcState *v1alpha1.VPCState, ncName string, subnetPath string) {
	setNetworkInfoVPCStatus(c, o, client, vpcState)
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "NetworkInfo CR has been successfully updated")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, common.MetricResTypeNetworkInfo)
}

func deleteSuccess(r *NetworkInfoReconciler, c context.Context, o *v1alpha1.NetworkInfo) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulDelete, "NetworkInfo CR has been successfully deleted")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, common.MetricResTypeNetworkInfo)
}

func setNetworkInfoVPCStatus(ctx context.Context, networkInfo *v1alpha1.NetworkInfo, client client.Client, createdVPC *v1alpha1.VPCState) {
	// if createdVPC is empty, remove the VPC from networkInfo
	if createdVPC == nil {
		networkInfo.VPCs = []v1alpha1.VPCState{}
		client.Update(ctx, networkInfo)
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
	client.Update(ctx, networkInfo)
	return
}

func setVPCNetworkConfigurationStatusWithLBS(ctx context.Context, client client.Client, ncName, vpcName, aviSubnetPath, nsxLBSPath, vpcPath string) {
	// read v1alpha1.VPCNetworkConfiguration by ncName
	nc := &v1alpha1.VPCNetworkConfiguration{}
	err := client.Get(ctx, apitypes.NamespacedName{Name: ncName}, nc)
	if err != nil {
		log.Error(err, "failed to get VPCNetworkConfiguration", "Name", ncName)
		return
	}

	// There should only be one vpc info in vpc network config info although it is defined as a list.
	// Always update vpcs[0] object
	nc.Status.VPCs = []v1alpha1.VPCInfo{{
		Name:                vpcName,
		AVISESubnetPath:     aviSubnetPath,
		NSXLoadBalancerPath: nsxLBSPath,
		VPCPath:             vpcPath,
	}}

	if err := client.Status().Update(ctx, nc); err != nil {
		log.Error(err, "Update VPCNetworkConfiguration status failed", "ncName", ncName, "vpcName", vpcName, "nc.Status.VPCs", nc.Status.VPCs)
		return
	}
	log.Info("Updated VPCNetworkConfiguration status", "ncName", ncName, "vpcName", vpcName, "nc.Status.VPCs", nc.Status.VPCs)
}

func setVPCNetworkConfigurationStatusWithGatewayConnection(ctx context.Context, client client.Client, nc *v1alpha1.VPCNetworkConfiguration, gatewayConnectionReady bool, reason string) {
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
		client.Status().Update(ctx, nc)
		log.Info("set VPCNetworkConfiguration status", "ncName", nc.Name, "condition", newConditions[0])
	}
}

func setVPCNetworkConfigurationStatusWithSnatEnabled(ctx context.Context, client client.Client, nc *v1alpha1.VPCNetworkConfiguration, autoSnatEnabled bool) {
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
		client.Status().Update(ctx, nc)
	}
}

func setVPCNetworkConfigurationStatusWithNoExternalIPBlock(ctx context.Context, client client.Client, nc *v1alpha1.VPCNetworkConfiguration, hasExternalIPs bool) {
	newCondition := v1alpha1.Condition{
		Type:               v1alpha1.ExternalIPBlocksConfigured,
		LastTransitionTime: metav1.Time{},
	}
	if !hasExternalIPs {
		newCondition.Status = v1.ConditionFalse
		newCondition.Reason = svccommon.ReasonNoExternalIPBlocksInVPCConnectivityProfile
		newCondition.Message = "No External IP Blocks exist in VPC Connectivity Profile"
	} else {
		newCondition.Status = v1.ConditionTrue
	}
	if mergeStatusCondition(ctx, &nc.Status.Conditions, &newCondition) {
		if err := client.Status().Update(ctx, nc); err != nil {
			log.Error(err, "update VPCNetworkConfiguration status failed", "VPCNetworkConfiguration", nc.Name)
			return
		}
	}
	log.Info("Updated VPCNetworkConfiguration status", "VPCNetworkConfiguration", nc.Name, "status", newCondition)
}

// TODO: abstract the logic of merging condition for common, which can be used by the other controller, e.g. security policy
func mergeStatusCondition(ctx context.Context, conditions *[]v1alpha1.Condition, newCondition *v1alpha1.Condition) bool {
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

func getGatewayConnectionStatus(ctx context.Context, nc *v1alpha1.VPCNetworkConfiguration) (bool, string) {
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
	return gatewayConnectionReady, reason
}

func deleteVPCNetworkConfigurationStatus(ctx context.Context, client client.Client, ncName string, staleVPCs []*model.Vpc, aliveVPCs []model.Vpc) {
	aliveVPCNames := sets.New[string]()
	for _, vpcModel := range aliveVPCs {
		aliveVPCNames.Insert(*vpcModel.DisplayName)
	}
	staleVPCNames := sets.New[string]()
	for _, vpc := range staleVPCs {
		staleVPCNames.Insert(*vpc.DisplayName)
	}
	// read v1alpha1.VPCNetworkConfiguration by ncName
	nc := &v1alpha1.VPCNetworkConfiguration{}
	err := client.Get(ctx, apitypes.NamespacedName{Name: ncName}, nc)
	if err != nil {
		log.Error(err, "failed to get VPCNetworkConfiguration", "Name", ncName)
		return
	}
	// iterate through VPCNetworkConfiguration.Status.VPCs, if vpcName does not exist in the staleVPCNames, append in new VPCs status
	var newVPCInfos []v1alpha1.VPCInfo
	for _, vpc := range nc.Status.VPCs {
		if !staleVPCNames.Has(vpc.Name) && aliveVPCNames.Has(vpc.Name) {
			newVPCInfos = append(newVPCInfos, vpc)
		}
	}
	nc.Status.VPCs = newVPCInfos
	if err := client.Status().Update(ctx, nc); err != nil {
		log.Error(err, "failed to delete stale VPCNetworkConfiguration status", "Name", ncName, "nc.Status.VPCs", nc.Status.VPCs, "staleVPCs", staleVPCNames)
		return
	}
	log.Info("Deleted stale VPCNetworkConfiguration status", "Name", ncName, "nc.Status.VPCs", nc.Status.VPCs, "staleVPCs", staleVPCNames)
}

func filterTagFromNSXVPC(nsxVPC *model.Vpc, tagName string) string {
	tags := nsxVPC.Tags
	for _, tag := range tags {
		if *tag.Scope == tagName {
			return *tag.Tag
		}
	}
	return ""
}

func setNSNetworkReadyCondition(ctx context.Context, client client.Client, nsName string, condition *v1.NamespaceCondition) {
	obj := &v1.Namespace{}
	if err := client.Get(ctx, apitypes.NamespacedName{Name: nsName}, obj); err != nil {
		log.Error(err, "unable to fetch namespace", "Namespace", nsName)
		return
	}

	updatedConditions := make([]v1.NamespaceCondition, 0)
	existingConditions := obj.Status.Conditions
	var extCondition *v1.NamespaceCondition
	for i := range existingConditions {
		cond := obj.Status.Conditions[i]
		if cond.Type == NamespaceNetworkReady {
			extCondition = &cond
		} else {
			updatedConditions = append(updatedConditions, cond)
		}
	}
	// Return if the failure (reason/message) is already added on Namespace condition.
	if extCondition != nil && nsConditionEquals(*extCondition, *condition) {
		return
	}

	updatedConditions = append(updatedConditions, v1.NamespaceCondition{
		Type:               condition.Type,
		Status:             condition.Status,
		Reason:             condition.Reason,
		Message:            condition.Message,
		LastTransitionTime: metav1.Now(),
	})
	obj.Status.Conditions = updatedConditions
	if err := client.Update(ctx, obj); err != nil {
		log.Error(err, "update Namespace status failed", "Namespace", nsName)
		return
	}
	log.Info("Updated Namespace network condition", "Namespace", nsName, "status", condition.Status, "reason", condition.Reason, "Message", condition.Message)
}

// nsConditionEquals compares the old and new Namespace condition. The compare ignores the differences in field
// "LastTransitionTime". It returns true all other fields in the two Conditions are the same, otherwise, it returns
// false. Ignoring the difference on LastTransitionTime may reduce the number of the Namespace update events.
func nsConditionEquals(old, new v1.NamespaceCondition) bool {
	if old.Type != new.Type {
		return false
	}
	if old.Status != new.Status {
		return false
	}
	if old.Reason != new.Reason {
		return false
	}
	if old.Message != new.Message {
		return false
	}
	return true
}
