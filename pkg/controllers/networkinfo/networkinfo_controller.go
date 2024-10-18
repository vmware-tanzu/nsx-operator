/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkinfo

import (
	"context"
	"fmt"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	commonservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/ipblocksinfo"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

var (
	log           = &logger.Log
	MetricResType = common.MetricResTypeNetworkInfo
)

const (
	NamespaceNetworkReady corev1.NamespaceConditionType = "NamespaceNetworkReady"

	NSReasonVPCNetConfigNotReady string = "VPCNetworkConfigurationNotReady"
	NSReasonVPCNotReady          string = "VPCNotReady"
	NSReasonVPCSnatNotReady      string = "VPCSnatNotReady"
)

var (
	nsMsgVPCNetCfgGetError        = newNsUnReadyMessage("Error happened to get VPC network configuration: %v", NSReasonVPCNetConfigNotReady)
	nsMsgSystemVPCNetCfgNotFound  = newNsUnReadyMessage("Error happened to get system VPC network configuration: %v", NSReasonVPCNetConfigNotReady)
	nsMsgVPCGwConnectionGetError  = newNsUnReadyMessage("Error happened to validate system VPC gateway connection readiness: %v", NSReasonVPCNetConfigNotReady)
	nsMsgVPCGwConnectionNotReady  = newNsUnReadyMessage("System VPC gateway connection is not ready", NSReasonVPCNetConfigNotReady)
	nsMsgVPCCreateUpdateError     = newNsUnReadyMessage("Error happened to create or update VPC: %v", NSReasonVPCNotReady)
	nsMsgVPCNsxLBSNotReady        = newNsUnReadyMessage("Error happened to get NSX LBS path in VPC: %v", NSReasonVPCNotReady)
	nsMsgVPCAviSubnetError        = newNsUnReadyMessage("Error happened to get Avi Load balancer Subnet info: %v", NSReasonVPCNotReady)
	nsMsgVPCGetExtIPBlockError    = newNsUnReadyMessage("Error happened to get external IP blocks: %v", NSReasonVPCNotReady)
	nsMsgVPCNoExternalIPBlock     = newNsUnReadyMessage("System VPC has no external IP blocks", NSReasonVPCNotReady)
	nsMsgVPCAutoSNATDisabled      = newNsUnReadyMessage("SNAT is not enabled in System VPC", NSReasonVPCSnatNotReady)
	nsMsgVPCDefaultSNATIPGetError = newNsUnReadyMessage("Default SNAT IP is not allocated in VPC: %v", NSReasonVPCSnatNotReady)
	nsMsgVPCIsReady               = newNsUnReadyMessage("", "")
)

type nsUnReadyMessage struct {
	reason string
	msg    string
}

func newNsUnReadyMessage(msg string, reason string) *nsUnReadyMessage {
	return &nsUnReadyMessage{
		msg:    msg,
		reason: reason,
	}
}

func (m *nsUnReadyMessage) getNSNetworkCondition(options ...interface{}) *corev1.NamespaceCondition {
	cond := &corev1.NamespaceCondition{
		Type:   NamespaceNetworkReady,
		Status: corev1.ConditionTrue,
	}
	if m.reason != "" {
		cond.Status = corev1.ConditionFalse
		cond.Reason = m.reason
		cond.Message = fmt.Sprintf(m.msg, options...)
	}
	return cond
}

// NetworkInfoReconciler NetworkInfoReconcile reconciles a NetworkInfo object
// Actually it is more like a shell, which is used to manage nsx VPC
type NetworkInfoReconciler struct {
	Client              client.Client
	Scheme              *apimachineryruntime.Scheme
	Service             *vpc.VPCService
	IPBlocksInfoService *ipblocksinfo.IPBlocksInfoService
	Recorder            record.EventRecorder
}

func (r *NetworkInfoReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startTime := time.Now()
	defer func() {
		log.Info("Finished reconciling NetworkInfo", "NetworkInfo", req.NamespacedName, "duration(ms)", time.Since(startTime).Milliseconds())
	}()
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, common.MetricResTypeNetworkInfo)

	networkInfoCR := &v1alpha1.NetworkInfo{}
	if err := r.Client.Get(ctx, req.NamespacedName, networkInfoCR); err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.deleteVPCsByName(ctx, req.Namespace); err != nil {
				log.Error(err, "Failed to delete stale NSX VPC", "NetworkInfo", req.NamespacedName)
				return common.ResultRequeue, err
			}
			return common.ResultNormal, nil
		}
		log.Error(err, "Unable to fetch NetworkInfo CR", "NetworkInfo", req.NamespacedName)
		return common.ResultRequeue, err
	}

	// Check if the CR is marked for deletion
	if !networkInfoCR.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, common.MetricResTypeNetworkInfo)
		if err := r.deleteVPCsByID(ctx, networkInfoCR.GetNamespace(), string(networkInfoCR.UID)); err != nil {
			deleteFail(r, ctx, networkInfoCR, &err, r.Client)
			log.Error(err, "Failed to delete stale NSX VPC, retrying", "NetworkInfo", req.NamespacedName)
			return common.ResultRequeue, err
		}
		deleteSuccess(r, ctx, networkInfoCR)
		return common.ResultNormal, nil
	}

	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateTotal, common.MetricResTypeNetworkInfo)
	// TODO:
	// 1. check whether the logic to get VPC network config can be replaced by GetVPCNetworkConfigByNamespace
	// 2. sometimes the variable nc points to a VPCNetworkInfo, sometimes it's a VPCNetworkConfiguration, we need to distinguish between them.
	nc, err := r.getNetworkConfigInfo(networkInfoCR)
	if err != nil {
		updateFail(r, ctx, networkInfoCR, &err, r.Client, nil)
		setNSNetworkReadyCondition(ctx, r.Client, req.Namespace, nsMsgVPCNetCfgGetError.getNSNetworkCondition(err))
		return common.ResultRequeueAfter10sec, err
	}

	ncName := nc.Name
	log.Info("Fetched network config from store", "NetworkConfig", ncName)

	systemVpcNetCfg := &v1alpha1.VPCNetworkConfiguration{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: commonservice.SystemVPCNetworkConfigurationName}, systemVpcNetCfg)
	if err != nil {
		log.Error(err, "Failed to get system VPCNetworkConfiguration")
		updateFail(r, ctx, networkInfoCR, &err, r.Client, nil)
		setNSNetworkReadyCondition(ctx, r.Client, req.Namespace, nsMsgSystemVPCNetCfgNotFound.getNSNetworkCondition(err))
		return common.ResultRequeueAfter10sec, err
	}

	retryWithSystemVPC := false
	var systemNSCondition *corev1.NamespaceCondition

	gatewayConnectionReady, _ := getGatewayConnectionStatus(ctx, systemVpcNetCfg)
	gatewayConnectionReason := ""
	if !gatewayConnectionReady {
		// Retry after 60s if the gateway connection is not ready in system VPC.
		if ncName != commonservice.SystemVPCNetworkConfigurationName {
			log.Info("Skipping reconciliation due to unready system gateway connection", "NetworkInfo", req.NamespacedName)
			setNSNetworkReadyCondition(ctx, r.Client, req.Namespace, nsMsgVPCGwConnectionNotReady.getNSNetworkCondition())
			return common.ResultRequeueAfter60sec, nil
		}

		// Re-check the gateway connection readiness in system VPC on NSX.
		gatewayConnectionReady, gatewayConnectionReason, err = r.Service.ValidateGatewayConnectionStatus(&nc)
		log.Info("got the gateway connection status", "gatewayConnectionReady", gatewayConnectionReady, "gatewayConnectionReason", gatewayConnectionReason)
		if err != nil {
			log.Error(err, "Failed to validate the edge and gateway connection", "Org", nc.Org, "Project", nc.NSXProject)
			updateFail(r, ctx, networkInfoCR, &err, r.Client, nil)
			setNSNetworkReadyCondition(ctx, r.Client, req.Namespace, nsMsgVPCGwConnectionGetError.getNSNetworkCondition(err))
			return common.ResultRequeueAfter10sec, err
		}
		setVPCNetworkConfigurationStatusWithGatewayConnection(ctx, r.Client, systemVpcNetCfg, gatewayConnectionReady, gatewayConnectionReason)

		// Retry after 60s if the gateway connection is still not ready in system VPC.
		if !gatewayConnectionReady {
			log.Info("Requeue NetworkInfo CR because VPCNetworkConfiguration system is not ready", "gatewayConnectionReason", gatewayConnectionReason, "req", req)
			retryWithSystemVPC = true
			systemNSCondition = nsMsgVPCGwConnectionNotReady.getNSNetworkCondition()
		}
	}

	lbProvider := r.Service.GetLBProvider()
	createdVpc, err := r.Service.CreateOrUpdateVPC(networkInfoCR, &nc, lbProvider)
	if err != nil {
		log.Error(err, "Failed to create or update VPC", "NetworkInfo", req.NamespacedName)
		updateFail(r, ctx, networkInfoCR, &err, r.Client, nil)
		setNSNetworkReadyCondition(ctx, r.Client, req.Namespace, nsMsgVPCCreateUpdateError.getNSNetworkCondition(err))
		return common.ResultRequeueAfter10sec, err
	}

	var privateIPs []string
	var vpcConnectivityProfilePath, nsxLBSPath string
	isPreCreatedVPC := vpc.IsPreCreatedVPC(nc)
	if isPreCreatedVPC {
		privateIPs = createdVpc.PrivateIps
		vpcConnectivityProfilePath = *createdVpc.VpcConnectivityProfile
		// Retrieve NSX lbs path if Avi is not used with the pre-created VPC.
		if createdVpc.LoadBalancerVpcEndpoint == nil || createdVpc.LoadBalancerVpcEndpoint.Enabled == nil ||
			!*createdVpc.LoadBalancerVpcEndpoint.Enabled {
			nsxLBSPath, err = r.Service.GetLBSsFromNSXByVPC(*createdVpc.Path)
			if err != nil {
				log.Error(err, "Failed to get NSX LBS path with pre-created VPC", "VPC", createdVpc.Path)
				updateFail(r, ctx, networkInfoCR, &err, r.Client, nil)
				setNSNetworkReadyCondition(ctx, r.Client, req.Namespace, nsMsgVPCNsxLBSNotReady.getNSNetworkCondition(err))
				return common.ResultRequeueAfter10sec, err
			}
		}
	} else {
		privateIPs = nc.PrivateIPs
		vpcConnectivityProfilePath = nc.VPCConnectivityProfile
		nsxLBSPath = r.Service.GetDefaultNSXLBSPathByVPC(*createdVpc.Id)
	}

	snatIP, path, cidr := "", "", ""

	vpcConnectivityProfile, err := r.Service.GetVpcConnectivityProfile(&nc, vpcConnectivityProfilePath)
	if err != nil {
		log.Error(err, "Failed to get VPC connectivity profile", "NetworkInfo", req.NamespacedName)
		updateFail(r, ctx, networkInfoCR, &err, r.Client, nil)
		setNSNetworkReadyCondition(ctx, r.Client, req.Namespace, nsMsgVPCGetExtIPBlockError.getNSNetworkCondition(err))
		return common.ResultRequeueAfter10sec, err
	}
	// Check external IP blocks on system VPC network config.
	if ncName == commonservice.SystemVPCNetworkConfigurationName {
		hasExternalIPs := len(vpcConnectivityProfile.ExternalIpBlocks) > 0
		setVPCNetworkConfigurationStatusWithNoExternalIPBlock(ctx, r.Client, systemVpcNetCfg, hasExternalIPs)
		if !hasExternalIPs && !retryWithSystemVPC {
			log.Error(err, "There is no ExternalIPBlock in VPC ConnectivityProfile", "NetworkInfo", req.NamespacedName)
			retryWithSystemVPC = true
			systemNSCondition = nsMsgVPCNoExternalIPBlock.getNSNetworkCondition()
		}
	}

	// currently, auto snat is not exposed, and use default value True
	// checking autosnat to support future extension in VPC configuration
	autoSnatEnabled := r.Service.IsEnableAutoSNAT(vpcConnectivityProfile)
	if autoSnatEnabled {
		snatIP, err = r.Service.GetDefaultSNATIP(*createdVpc)
		if err != nil {
			log.Error(err, "Failed to read default SNAT IP from VPC", "VPC", createdVpc.Id)
			state := &v1alpha1.VPCState{
				Name:                    *createdVpc.DisplayName,
				DefaultSNATIP:           "",
				LoadBalancerIPAddresses: "",
				PrivateIPs:              privateIPs,
			}
			updateFail(r, ctx, networkInfoCR, &err, r.Client, state)
			setNSNetworkReadyCondition(ctx, r.Client, req.Namespace, nsMsgVPCDefaultSNATIPGetError.getNSNetworkCondition(err))
			return common.ResultRequeueAfter10sec, err
		}
	}
	if ncName == commonservice.SystemVPCNetworkConfigurationName {
		log.Info("Got the AutoSnat status", "autoSnatEnabled", autoSnatEnabled, "NetworkInfo", req.NamespacedName)
		setVPCNetworkConfigurationStatusWithSnatEnabled(ctx, r.Client, systemVpcNetCfg, autoSnatEnabled)
		if !autoSnatEnabled && !retryWithSystemVPC {
			log.Info("Requeue NetworkInfo CR because VPCNetworkConfiguration system is not ready", "autoSnatEnabled", autoSnatEnabled, "req", req)
			retryWithSystemVPC = true
			systemNSCondition = nsMsgVPCAutoSNATDisabled.getNSNetworkCondition()
		}
	}

	// if lb VPC enabled, read avi subnet path and cidr
	// nsx bug, if set LoadBalancerVpcEndpoint.Enabled to false, when read this VPC back,
	// LoadBalancerVpcEndpoint.Enabled will become a nil pointer.
	if lbProvider == vpc.AVILB && createdVpc.LoadBalancerVpcEndpoint != nil && createdVpc.LoadBalancerVpcEndpoint.Enabled != nil && *createdVpc.LoadBalancerVpcEndpoint.Enabled {
		path, cidr, err = r.Service.GetAVISubnetInfo(*createdVpc)
		if err != nil {
			log.Error(err, "Failed to read LB Subnet path and CIDR", "VPC", createdVpc.Id)
			state := &v1alpha1.VPCState{
				Name:                    *createdVpc.DisplayName,
				DefaultSNATIP:           snatIP,
				LoadBalancerIPAddresses: "",
				PrivateIPs:              privateIPs,
			}
			updateFail(r, ctx, networkInfoCR, &err, r.Client, state)
			setNSNetworkReadyCondition(ctx, r.Client, req.Namespace, nsMsgVPCAviSubnetError.getNSNetworkCondition(err))
			return common.ResultRequeueAfter10sec, err
		}
	}

	state := &v1alpha1.VPCState{
		Name:                    *createdVpc.DisplayName,
		DefaultSNATIP:           snatIP,
		LoadBalancerIPAddresses: cidr,
		PrivateIPs:              privateIPs,
		VPCPath:                 *createdVpc.Path,
	}

	// AKO needs to know the AVI subnet path created by NSX
	setVPCNetworkConfigurationStatusWithLBS(ctx, r.Client, ncName, state.Name, path, nsxLBSPath, *createdVpc.Path)
	updateSuccess(r, ctx, networkInfoCR, r.Client, state, nc.Name, path)

	if retryWithSystemVPC {
		setNSNetworkReadyCondition(ctx, r.Client, req.Namespace, systemNSCondition)
		return common.ResultRequeueAfter60sec, nil
	}

	setNSNetworkReadyCondition(ctx, r.Client, req.Namespace, nsMsgVPCIsReady.getNSNetworkCondition())
	return common.ResultNormal, nil
}

func (r *NetworkInfoReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.NetworkInfo{}).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Watches(
			// For created/removed network config, add/remove from VPC network config cache,
			// and update IPBlocksInfo.
			// For modified network config, currently only support appending ips to public ip blocks,
			// update network config in cache and update nsx VPC object.
			&v1alpha1.VPCNetworkConfiguration{},
			&VPCNetworkConfigurationHandler{
				Client:              mgr.GetClient(),
				vpcService:          r.Service,
				ipBlocksInfoService: r.IPBlocksInfoService,
			},
			builder.WithPredicates(VPCNetworkConfigurationPredicate)).
		Complete(r)
}

// Start setup manager and launch GC
func (r *NetworkInfoReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}
	return nil
}

func (r *NetworkInfoReconciler) listNamespaceCRsNameIDSet(ctx context.Context) (sets.Set[string], sets.Set[string], error) {
	// read all Namespaces from K8s
	namespaces := &corev1.NamespaceList{}
	err := r.Client.List(ctx, namespaces)
	if err != nil {
		return nil, nil, err
	}
	nsSet := sets.Set[string]{}
	idSet := sets.Set[string]{}
	for _, ns := range namespaces.Items {
		nsSet.Insert(ns.Name)
		idSet.Insert(string(ns.UID))
	}
	return nsSet, idSet, nil
}

// CollectGarbage logic for NSX VPC is that:
// 1. list all current existing namespace in kubernetes
// 2. list all the NSX VPC in vpcStore
// 3. loop all the NSX VPC to get its namespace, check if the namespace still exist
// 4. if ns do not exist anymore, delete the NSX VPC resource
// it implements the interface GarbageCollector method.
func (r *NetworkInfoReconciler) CollectGarbage(ctx context.Context) {
	startTime := time.Now()
	defer func() {
		log.Info("VPC garbage collection completed", "duration(ms)", time.Since(startTime).Milliseconds())
	}()

	// read all NSX VPC from VPC store
	nsxVPCList := r.Service.ListVPC()
	if len(nsxVPCList) == 0 {
		log.Info("No NSX VPCs found in the store, skipping garbage collection")
		return
	}

	_, idSet, err := r.listNamespaceCRsNameIDSet(ctx)
	if err != nil {
		log.Error(err, "Failed to list Kubernetes Namespaces for VPC garbage collection")
		return
	}

	for i, nsxVPC := range nsxVPCList {
		nsxVPCNamespaceName := filterTagFromNSXVPC(&nsxVPCList[i], commonservice.TagScopeNamespace)
		nsxVPCNamespaceID := filterTagFromNSXVPC(&nsxVPCList[i], commonservice.TagScopeNamespaceUID)
		if idSet.Has(nsxVPCNamespaceID) {
			continue
		}
		log.Info("Garbage collecting NSX VPC object", "VPC", nsxVPC.Id, "Namespace", nsxVPCNamespaceName)
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, common.MetricResTypeNetworkInfo)

		if err = r.Service.DeleteVPC(*nsxVPC.Path); err != nil {
			log.Error(err, "Failed to delete NSX VPC", "VPC", nsxVPC.Id, "Namespace", nsxVPCNamespaceName)
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, common.MetricResTypeNetworkInfo)
			continue
		}

		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, common.MetricResTypeNetworkInfo)
		log.Info("Successfully deleted NSX VPC", "VPC", nsxVPC.Id)
	}
}

func (r *NetworkInfoReconciler) fetchStaleVPCsByNamespace(ctx context.Context, ns string) ([]*model.Vpc, error) {
	isShared, err := r.Service.IsSharedVPCNamespaceByNS(ns)
	if err != nil {
		return nil, fmt.Errorf("failed to check if Namespace is shared for NS %s: %w", ns, err)
	}
	if isShared {
		log.Info("Shared Namespace, skipping deletion of NSX VPC", "Namespace", ns)
		return nil, nil
	}

	return r.Service.GetVPCsByNamespace(ns), nil
}

func (r *NetworkInfoReconciler) deleteVPCsByName(ctx context.Context, ns string) error {
	_, idSet, err := r.listNamespaceCRsNameIDSet(ctx)
	if err != nil {
		log.Error(err, "Failed to list Kubernetes Namespaces")
		return fmt.Errorf("failed to list Kubernetes Namespaces while deleting VPCs: %v", err)
	}

	staleVPCs, err := r.fetchStaleVPCsByNamespace(ctx, ns)
	if err != nil {
		return err
	}

	var vpcToDelete []*model.Vpc
	for _, nsxVPC := range staleVPCs {
		namespaceIDofVPC := filterTagFromNSXVPC(nsxVPC, commonservice.TagScopeNamespaceUID)
		if idSet.Has(namespaceIDofVPC) {
			log.Info("Skipping deletion, Namespace still exists in K8s", "Namespace", ns)
			continue
		}
		vpcToDelete = append(vpcToDelete, nsxVPC)
	}
	return r.deleteVPCs(ctx, vpcToDelete, ns)
}

func (r *NetworkInfoReconciler) deleteVPCsByID(ctx context.Context, ns, id string) error {
	staleVPCs, err := r.fetchStaleVPCsByNamespace(ctx, ns)
	if err != nil {
		return err
	}

	var vpcToDelete []*model.Vpc
	for _, nsxVPC := range staleVPCs {
		namespaceIDofVPC := filterTagFromNSXVPC(nsxVPC, commonservice.TagScopeNamespaceUID)
		if namespaceIDofVPC == id {
			vpcToDelete = append(vpcToDelete, nsxVPC)
		}
	}
	return r.deleteVPCs(ctx, vpcToDelete, ns)
}

func (r *NetworkInfoReconciler) deleteVPCs(ctx context.Context, staleVPCs []*model.Vpc, ns string) error {
	if len(staleVPCs) == 0 {
		log.Info("There is no VPCs found in store, skipping deletion of NSX VPC", "Namespace", ns)
		return nil
	}
	var deleteErrs []error
	for _, nsxVPC := range staleVPCs {
		if nsxVPC.Path == nil {
			log.Error(nil, "VPC path is nil, skipping", "VPC", nsxVPC)
			continue
		}
		if err := r.Service.DeleteVPC(*nsxVPC.Path); err != nil {
			log.Error(err, "Failed to delete VPC in NSX", "VPC", nsxVPC.Path)
			deleteErrs = append(deleteErrs, fmt.Errorf("failed to delete VPC %s: %w", *nsxVPC.Path, err))
		}
	}
	if len(deleteErrs) > 0 {
		return fmt.Errorf("multiple errors occurred while deleting VPCs: %v", deleteErrs)
	}

	// Update the VPCNetworkConfiguration Status
	ncName, err := r.Service.GetNetworkconfigNameFromNS(ns)
	if err != nil {
		return fmt.Errorf("failed to get VPCNetworkConfiguration for Namespace when deleting stale VPCs %s: %w", ns, err)
	}
	deleteVPCNetworkConfigurationStatus(ctx, r.Client, ncName, staleVPCs, r.Service.ListVPC())
	return nil
}

func (r *NetworkInfoReconciler) getNetworkConfigInfo(networkInfoCR *v1alpha1.NetworkInfo) (commonservice.VPCNetworkConfigInfo, error) {
	ncName, err := r.Service.GetNetworkconfigNameFromNS(networkInfoCR.Namespace)
	if err != nil {
		log.Error(err, "Failed to get network config name for VPC when creating NSX VPC", "NetworkInfo", networkInfoCR.Name)
		return commonservice.VPCNetworkConfigInfo{}, err
	}
	nc, _exist := r.Service.GetVPCNetworkConfig(ncName)
	if !_exist {
		message := fmt.Sprintf("Failed to read network config %s when creating NSX VPC", ncName)
		log.Info(message)
		return commonservice.VPCNetworkConfigInfo{}, fmt.Errorf("VPCNetworkConfig %s not found", ncName)
	}
	return nc, nil
}
