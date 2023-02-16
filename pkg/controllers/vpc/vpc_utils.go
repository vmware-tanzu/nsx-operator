package vpc

import (
	"context"
	"fmt"
	"reflect"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func setVPCReadyStatusFalse(ctx *context.Context, vpc *v1alpha1.VPC, err *error, client client.Client) {
	newConditions := []v1alpha1.Condition{
		{
			Type:    v1alpha1.Ready,
			Status:  v1.ConditionFalse,
			Message: "NSX VPC could not be created/updated",
			Reason:  fmt.Sprintf("Error occurred while processing the VPC CR. Please check the config and try again. Error: %v", *err),
		},
	}
	updateVPCStatusConditions(ctx, vpc, newConditions, client, "", "")
}

func updateVPCStatusConditions(ctx *context.Context, vpc *v1alpha1.VPC, newConditions []v1alpha1.Condition, client client.Client, path string, snatIP string) {
	conditionsUpdated := false
	statusUpdated := false
	for i := range newConditions {
		if mergeVPCStatusCondition(ctx, vpc, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if vpc.Status.NSXResourcePath != path || vpc.Status.DefaultSNATIP != snatIP {
		vpc.Status.NSXResourcePath = path
		vpc.Status.DefaultSNATIP = snatIP
		statusUpdated = true
	}

	if conditionsUpdated || statusUpdated {

		client.Status().Update(*ctx, vpc)
		log.V(1).Info("Updated VPC CRD", "Name", vpc.Name, "Namespace", vpc.Namespace, "New Conditions", newConditions)
	}
}

func deleteFail(nsxConfig *config.NSXOperatorConfig, c *context.Context, o *v1alpha1.VPC, e *error, client client.Client) {
	setVPCReadyStatusFalse(c, o, e, client)
	metrics.CounterInc(nsxConfig, metrics.ControllerDeleteFailTotal, common.MetricResTypeVPC)
}

func updateFail(nsxConfig *config.NSXOperatorConfig, c *context.Context, o *v1alpha1.VPC, e *error, client client.Client) {
	setVPCReadyStatusFalse(c, o, e, client)
	metrics.CounterInc(nsxConfig, metrics.ControllerUpdateFailTotal, MetricResType)
}

func updateSuccess(nsxConfig *config.NSXOperatorConfig, c *context.Context, o *v1alpha1.VPC, client client.Client, path string, snatIP string) {
	setVPCReadyStatusTrue(c, o, client, path, snatIP)
	metrics.CounterInc(nsxConfig, metrics.ControllerUpdateSuccessTotal, common.MetricResTypeVPC)
}

func deleteSuccess(nsxConfig *config.NSXOperatorConfig, _ *context.Context, _ *v1alpha1.VPC) {
	metrics.CounterInc(nsxConfig, metrics.ControllerDeleteSuccessTotal, common.MetricResTypeVPC)
}

func setVPCReadyStatusTrue(ctx *context.Context, vpc *v1alpha1.VPC, client client.Client, path string, snatIP string) {
	newConditions := []v1alpha1.Condition{
		{
			Type:    v1alpha1.Ready,
			Status:  v1.ConditionTrue,
			Message: "NSX VPC has been successfully created/updated",
			Reason:  "NSX API returned 200 response code for PATCH",
		},
	}
	updateVPCStatusConditions(ctx, vpc, newConditions, client, path, snatIP)
}

func mergeVPCStatusCondition(ctx *context.Context, vpc *v1alpha1.VPC, newCondition *v1alpha1.Condition) bool {
	matchedCondition := getExistingConditionOfType(newCondition.Type, vpc.Status.Conditions)

	if reflect.DeepEqual(matchedCondition, newCondition) {
		log.V(2).Info("Conditions already match", "New Condition", newCondition, "Existing Condition", matchedCondition)
		return false
	}

	if matchedCondition != nil {
		matchedCondition.Reason = newCondition.Reason
		matchedCondition.Message = newCondition.Message
		matchedCondition.Status = newCondition.Status
	} else {
		vpc.Status.Conditions = append(vpc.Status.Conditions, *newCondition)
	}
	return true
}

func getExistingConditionOfType(conditionType v1alpha1.ConditionType, existingConditions []v1alpha1.Condition) *v1alpha1.Condition {
	for i := range existingConditions {
		if existingConditions[i].Type == v1alpha1.ConditionType(conditionType) {
			return &existingConditions[i]
		}
	}
	return nil
}
