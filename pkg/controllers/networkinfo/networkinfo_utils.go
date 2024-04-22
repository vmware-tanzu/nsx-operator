package networkinfo

import (
	"context"
	"fmt"
	"reflect"

	v1 "k8s.io/api/core/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	client "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
)

func deleteFail(r *NetworkInfoReconciler, c *context.Context, o *v1alpha1.NetworkInfo, e *error, client client.Client) {
	createdVpc := &v1alpha1.VPCState{}
	setNetworkInfoVPCStatus(c, o, client, createdVpc)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailDelete, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, common.MetricResTypeNetworkInfo)
}

func updateFail(r *NetworkInfoReconciler, c *context.Context, o *v1alpha1.NetworkInfo, e *error, client client.Client) {
	createdVpc := &v1alpha1.VPCState{}
	setNetworkInfoVPCStatus(c, o, client, createdVpc)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailUpdate, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResType)
}

func updateSuccess(r *NetworkInfoReconciler, c *context.Context, o *v1alpha1.NetworkInfo, client client.Client,
	name string, path string, snatIP string, subnetPath string, cidr string, privateCidrs []string, ncName string) {
	createdVpc := &v1alpha1.VPCState{
		Name:                    name,
		VPCPath:                 path,
		DefaultSNATIP:           snatIP,
		LoadBalancerIPAddresses: cidr,
		PrivateIPv4CIDRs:        privateCidrs,
	}
	setNetworkInfoVPCStatus(c, o, client, createdVpc)
	// ako needs to know the avi subnet path created by nsx
	setVPCNetworkConfigurationStatus(c, client, ncName, name, subnetPath)
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "NetworkInfo CR has been successfully updated")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, common.MetricResTypeNetworkInfo)
}

func deleteSuccess(r *NetworkInfoReconciler, _ *context.Context, o *v1alpha1.NetworkInfo) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulDelete, "NetworkInfo CR has been successfully deleted")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, common.MetricResTypeNetworkInfo)
}

func setNetworkInfoVPCStatus(ctx *context.Context, networkInfo *v1alpha1.NetworkInfo, client client.Client, createdVPC *v1alpha1.VPCState) {
	existingVPC := &v1alpha1.VPCState{}
	if len(networkInfo.VPCs) > 0 {
		existingVPC = &networkInfo.VPCs[0]
	}
	if !reflect.DeepEqual(*existingVPC, *createdVPC) {
		networkInfo.VPCs = []v1alpha1.VPCState{*createdVPC}
		client.Update(*ctx, networkInfo)
	}
}

func setVPCNetworkConfigurationStatus(ctx *context.Context, client client.Client, ncName string, vpcName string, aviSubnetPath string) {
	// read v1alpha1.VPCNetworkConfiguration by ncName
	nc := &v1alpha1.VPCNetworkConfiguration{}
	err := client.Get(*ctx, apitypes.NamespacedName{Name: ncName}, nc)
	if err != nil {
		log.Error(err, "failed to get VPCNetworkConfiguration", "Name", ncName)
	}
	createdVPCInfo := &v1alpha1.VPCInfo{
		Name:            vpcName,
		AVISESubnetPath: aviSubnetPath,
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
