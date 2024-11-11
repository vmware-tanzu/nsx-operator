package networkinfo

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	commontypes "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	retryInterval = 10 * time.Second
)

// VPCNetworkConfigurationHandler handles VPC NetworkConfiguration event, and reconcile VPC event:
// - VPC Network Configuration creation: Add VPC Network Configuration into cache.
// - VPC Network Configuration deletion: Delete VPC Network Configuration from cache.
// - VPC Network Configuration update:	Only support updating external/private ipblocks, update values in cache

type VPCNetworkConfigurationHandler struct {
	Client              client.Client
	vpcService          commontypes.VPCServiceProvider
	ipBlocksInfoService commontypes.IPBlocksInfoServiceProvider
}

func (h *VPCNetworkConfigurationHandler) Create(ctx context.Context, e event.CreateEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	vpcConfigCR := e.Object.(*v1alpha1.VPCNetworkConfiguration)
	vname := vpcConfigCR.GetName()
	ninfo, err := buildNetworkConfigInfo(*vpcConfigCR)
	if err != nil {
		log.Error(err, "Processing network config add event failed")
		return
	}
	log.Info("Create network config and update to store", "NetworkConfigInfo", ninfo)
	h.vpcService.RegisterVPCNetworkConfig(vname, *ninfo)
	// Update IPBlocks info
	if err = h.ipBlocksInfoService.UpdateIPBlocksInfo(ctx, vpcConfigCR); err != nil {
		log.Error(err, "Failed to update the IPBlocksInfo", "VPCNetworkConfiguration", vname)
	}
}

func (h *VPCNetworkConfigurationHandler) Delete(ctx context.Context, e event.DeleteEvent, w workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	vpcConfigCR := e.Object.(*v1alpha1.VPCNetworkConfiguration)
	if err := h.ipBlocksInfoService.SyncIPBlocksInfo(ctx); err != nil {
		log.Error(err, "failed to synchronize IPBlocksInfo when deleting %s", vpcConfigCR.Name)
		req := reconcile.Request{
			NamespacedName: client.ObjectKey{
				Name:      e.Object.GetName(),
				Namespace: e.Object.GetNamespace(),
			},
		}
		w.AddAfter(req, retryInterval)
	} else {
		h.ipBlocksInfoService.ResetPeriodicSync()
	}
}

func (h *VPCNetworkConfigurationHandler) Generic(_ context.Context, _ event.GenericEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	log.V(1).Info("VPCNetworkConfiguration generic event, do nothing")
}

func (h *VPCNetworkConfigurationHandler) Update(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	log.V(1).Info("Start processing VPC network config update event")
	newNc := e.ObjectNew.(*v1alpha1.VPCNetworkConfiguration)

	oldNc := e.ObjectOld.(*v1alpha1.VPCNetworkConfiguration)
	if reflect.DeepEqual(oldNc.Spec, newNc.Spec) {
		log.Info("Skip processing VPC network config update event", "newNc", newNc, "oldNc", oldNc)
		return
	}

	// update network config info in store
	info, err := buildNetworkConfigInfo(*newNc)
	if err != nil {
		log.Error(err, "Failed to process network config update event")
		return
	}
	h.vpcService.RegisterVPCNetworkConfig(newNc.Name, *info)

	nss := h.vpcService.GetNamespacesByNetworkconfigName(newNc.Name)
	for _, ns := range nss {
		networkInfos := &v1alpha1.NetworkInfoList{}
		err := h.Client.List(ctx, networkInfos, client.InNamespace(ns))
		if err != nil {
			log.Error(err, "Failed to list VPCs in namespace", "Namespace", ns)
			continue
		}

		for _, networkInfo := range networkInfos.Items {
			log.Info("Requeue NetworkInfo CR due to modifying network config CR", "NetworkInfo", networkInfo.Name, "Namespace", ns, "NetworkConfig", newNc.Name)
			q.Add(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      networkInfo.Name,
					Namespace: networkInfo.Namespace,
				},
			})
		}
	}
}

var VPCNetworkConfigurationPredicate = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return true
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		return true
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return true
	},
	GenericFunc: func(genericEvent event.GenericEvent) bool {
		return false
	},
}

func buildNetworkConfigInfo(vpcConfigCR v1alpha1.VPCNetworkConfiguration) (*commontypes.VPCNetworkConfigInfo, error) {
	org, project, err := nsxProjectPathToId(vpcConfigCR.Spec.NSXProject)
	if err != nil {
		log.Error(err, "failed to parse NSX project in network config", "Project Path", vpcConfigCR.Spec.NSXProject)
		return nil, err
	}

	ninfo := &commontypes.VPCNetworkConfigInfo{
		IsDefault:              isDefaultNetworkConfigCR(vpcConfigCR),
		Org:                    org,
		Name:                   vpcConfigCR.Name,
		VPCConnectivityProfile: vpcConfigCR.Spec.VPCConnectivityProfile,
		NSXProject:             project,
		PrivateIPs:             vpcConfigCR.Spec.PrivateIPs,
		DefaultSubnetSize:      vpcConfigCR.Spec.DefaultSubnetSize,
		VPCPath:                vpcConfigCR.Spec.VPC,
	}
	return ninfo, nil
}

func isDefaultNetworkConfigCR(vpcConfigCR v1alpha1.VPCNetworkConfiguration) bool {
	annos := vpcConfigCR.GetAnnotations()
	val, exist := annos[commontypes.AnnotationDefaultNetworkConfig]
	if exist {
		boolVar, err := strconv.ParseBool(val)
		if err != nil {
			log.Error(err, "failed to parse annotation to check default NetworkConfig", "Annotation", annos[commontypes.AnnotationDefaultNetworkConfig])
			return false
		}
		return boolVar
	}
	return false
}

// parse org id and project id from nsxProject path
// example /orgs/default/projects/nsx_operator_e2e_test
func nsxProjectPathToId(path string) (string, string, error) {
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		return "", "", errors.New("invalid NSX project path")
	}
	return parts[2], parts[len(parts)-1], nil
}
