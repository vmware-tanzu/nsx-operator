package networkinfo

import (
	"context"
	"reflect"
	"slices"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	svccommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func setNetworkInfoVPCStatusWithError(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, _ error, args ...interface{}) {
	setNetworkInfoVPCStatus(client, ctx, obj, transitionTime, args...)
}

func setNetworkInfoVPCStatus(client client.Client, ctx context.Context, obj client.Object, _ metav1.Time, args ...interface{}) {
	if len(args) != 1 {
		log.Error(nil, "VPC State is needed when updating NetworkInfo status")
		return
	}
	networkInfo := obj.(*v1alpha1.NetworkInfo)
	var createdVPC *v1alpha1.VPCState
	if args[0] == nil {
		// if createdVPC is empty, remove the VPC from networkInfo
		networkInfo.VPCs = []v1alpha1.VPCState{}
		client.Update(ctx, networkInfo)
		return
	} else {
		createdVPC = args[0].(*v1alpha1.VPCState)
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
		log.Error(err, "Failed to get VPCNetworkConfiguration", "Name", ncName)
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
		if mergeStatusCondition(&nc.Status.Conditions, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		client.Status().Update(ctx, nc)
		log.Info("Set VPCNetworkConfiguration status", "ncName", nc.Name, "condition", newConditions[0])
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
		if mergeStatusCondition(&nc.Status.Conditions, &newConditions[i]) {
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
	if mergeStatusCondition(&nc.Status.Conditions, &newCondition) {
		if err := client.Status().Update(ctx, nc); err != nil {
			log.Error(err, "Update VPCNetworkConfiguration status failed", "VPCNetworkConfiguration", nc.Name)
			return
		}
	}
	log.Info("Updated VPCNetworkConfiguration status", "VPCNetworkConfiguration", nc.Name, "status", newCondition)
}

// TODO: abstract the logic of merging condition for common, which can be used by the other controller, e.g. security policy
func mergeStatusCondition(conditions *[]v1alpha1.Condition, newCondition *v1alpha1.Condition) bool {
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

func updateVPCNetworkConfigurationStatusWithAliveVPCs(ctx context.Context, client client.Client, ncName string, getAliveVPCsFn func(ncName string) []*model.Vpc) {
	// read v1alpha1.VPCNetworkConfiguration by ncName
	nc := &v1alpha1.VPCNetworkConfiguration{}
	err := client.Get(ctx, apitypes.NamespacedName{Name: ncName}, nc)
	if err != nil {
		log.Error(err, "Failed to get VPCNetworkConfiguration", "Name", ncName)
		return
	}

	if getAliveVPCsFn != nil {
		aliveVPCs := sets.New[string]()
		for _, vpc := range getAliveVPCsFn(ncName) {
			aliveVPCs.Insert(*vpc.DisplayName)
		}
		var newVPCInfos []v1alpha1.VPCInfo
		for _, vpcInfo := range nc.Status.VPCs {
			if aliveVPCs.Has(vpcInfo.Name) {
				newVPCInfos = append(newVPCInfos, vpcInfo)
			}
		}
		nc.Status.VPCs = newVPCInfos
		if err := client.Status().Update(ctx, nc); err != nil {
			log.Error(err, "Failed to update VPCNetworkConfiguration status", "Name", ncName, "nc.Status.VPCs", nc.Status.VPCs)
			return
		}
		log.Info("Updated VPCNetworkConfiguration status", "Name", ncName, "nc.Status.VPCs", nc.Status.VPCs)
	}
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

func setNSNetworkReadyCondition(ctx context.Context, kubeClient client.Client, nsName string, condition *v1.NamespaceCondition) {
	obj := &v1.Namespace{}
	if err := kubeClient.Get(ctx, apitypes.NamespacedName{Name: nsName}, obj); err != nil {
		log.Error(err, "Unable to fetch Namespace", "Namespace", nsName)
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
	if err := kubeClient.Status().Update(ctx, obj, &client.SubResourceUpdateOptions{}); err != nil {
		log.Error(err, "Failed to update Namespace status", "Namespace", nsName)
		return
	}
	log.Info("Updated Namespace network condition", "Namespace", nsName, "status", condition.Status, "reason", condition.Reason, "message", condition.Message)
}

// nsConditionEquals compares the old and new Namespace condition. The compare ignores the differences in field
// "LastTransitionTime". It returns true if all other fields in the two Conditions are the same, otherwise, it returns
// false. Ignoring the difference on LastTransitionTime may reduce the number of the Namespace update events.
func nsConditionEquals(old, new v1.NamespaceCondition) bool {
	return old.Type == new.Type && old.Status == new.Status &&
		old.Reason == new.Reason && old.Message == new.Message
}
