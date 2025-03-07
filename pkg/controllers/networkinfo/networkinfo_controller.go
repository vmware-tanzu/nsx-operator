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
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	commonservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/ipblocksinfo"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

const (
	preVPCSyncInterval = time.Minute * 10
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
	nsMsgVPCNetCfgGetError        = newNsUnreadyMessage("Error happened to get VPC network configuration: %v", NSReasonVPCNetConfigNotReady)
	nsMsgSystemVPCNetCfgNotFound  = newNsUnreadyMessage("Error happened to get system VPC network configuration: %v", NSReasonVPCNetConfigNotReady)
	nsMsgVPCGwConnectionGetError  = newNsUnreadyMessage("Error happened to validate system VPC gateway connection readiness: %v", NSReasonVPCNetConfigNotReady)
	nsMsgVPCGwConnectionNotReady  = newNsUnreadyMessage("System VPC gateway connection is not ready", NSReasonVPCNetConfigNotReady)
	nsMsgVPCCreateUpdateError     = newNsUnreadyMessage("Error happened to create or update VPC: %v", NSReasonVPCNotReady)
	nsMsgVPCNsxLBSNotReady        = newNsUnreadyMessage("Error happened to get NSX LBS path in VPC: %v", NSReasonVPCNotReady)
	nsMsgVPCAviSubnetError        = newNsUnreadyMessage("Error happened to get Avi Load balancer Subnet info: %v", NSReasonVPCNotReady)
	nsMsgVPCNSXLBSNATIPError      = newNsUnreadyMessage("Error happened to get NSX Load balancer SNAT IP info: %v", NSReasonVPCNotReady)
	nsMsgVPCGetExtIPBlockError    = newNsUnreadyMessage("Error happened to get external IP blocks: %v", NSReasonVPCNotReady)
	nsMsgVPCNoExternalIPBlock     = newNsUnreadyMessage("System VPC has no external IP blocks", NSReasonVPCNotReady)
	nsMsgVPCAutoSNATDisabled      = newNsUnreadyMessage("SNAT is not enabled in System VPC", NSReasonVPCSnatNotReady)
	nsMsgVPCDefaultSNATIPGetError = newNsUnreadyMessage("Default SNAT IP is not allocated in VPC: %v", NSReasonVPCSnatNotReady)
	nsMsgVPCIsReady               = newNsUnreadyMessage("", "")
)

type nsUnreadyMessage struct {
	reason string
	msg    string
}

func newNsUnreadyMessage(msg string, reason string) *nsUnreadyMessage {
	return &nsUnreadyMessage{
		msg:    msg,
		reason: reason,
	}
}

func (m *nsUnreadyMessage) getNSNetworkCondition(options ...interface{}) *corev1.NamespaceCondition {
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
	queue               workqueue.TypedRateLimitingInterface[reconcile.Request]
	StatusUpdater       common.StatusUpdater
}

func (r *NetworkInfoReconciler) GetVpcConnectivityProfilePathByVpcPath(vpcPath string) (string, error) {
	// TODO, if needs to add a cache for it
	VPCResourceInfo, err := commonservice.ParseVPCResourcePath(vpcPath)
	if err != nil {
		log.Error(err, "Failed to parse VPC path", "VPC Path", vpcPath)
		return "", err
	}
	// pre created VPC may have more than one attachment, list all the attachment and select the first one
	vpcAttachmentsListResult, err := r.Service.NSXClient.VpcAttachmentClient.List(VPCResourceInfo.OrgID, VPCResourceInfo.ProjectID, VPCResourceInfo.VPCID, nil, nil, nil, nil, nil, nil)
	if err != nil {
		log.Error(err, "Failed to list VPC attachment", "VPC Path", vpcPath)
		return "", err
	}
	vpcAttachments := vpcAttachmentsListResult.Results
	if len(vpcAttachments) > 0 {
		log.V(1).Info("found VPC attachment", "VPC Path", vpcPath, "VPC connectivity profile", vpcAttachments[0].VpcConnectivityProfile)
		return *vpcAttachments[0].VpcConnectivityProfile, nil
	} else {
		err := fmt.Errorf("no VPC attachment found")
		log.Error(err, "List VPC attachment", "VPC Path", vpcPath)
		return "", err
	}
}

func (r *NetworkInfoReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startTime := time.Now()
	defer func() {
		log.Info("Finished reconciling NetworkInfo", "NetworkInfo", req.NamespacedName, "duration(ms)", time.Since(startTime).Milliseconds())
	}()

	r.StatusUpdater.IncreaseSyncTotal()

	networkInfoCR := &v1alpha1.NetworkInfo{}
	if err := r.Client.Get(ctx, req.NamespacedName, networkInfoCR); err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.deleteVPCsByNamespace(ctx, req.Namespace); err != nil {
				r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
				return common.ResultRequeue, err
			}
			r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
			return common.ResultNormal, nil
		}
		log.Error(err, "Unable to fetch NetworkInfo CR", "NetworkInfo", req.NamespacedName)
		return common.ResultRequeue, err
	}

	// Check if the CR is marked for deletion
	if !networkInfoCR.ObjectMeta.DeletionTimestamp.IsZero() {
		r.StatusUpdater.IncreaseDeleteTotal()
		if err := r.deleteVPCsByNamespace(ctx, networkInfoCR.GetNamespace()); err != nil {
			r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
			return common.ResultRequeue, err
		}
		r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
		return common.ResultNormal, nil
	}

	r.StatusUpdater.IncreaseUpdateTotal()

	nc, err := r.getNetworkConfigInfo(ctx, networkInfoCR)
	if err != nil {
		r.StatusUpdater.UpdateFail(ctx, networkInfoCR, err, "", setNetworkInfoVPCStatusWithError, nil)
		setNSNetworkReadyCondition(ctx, r.Client, req.Namespace, nsMsgVPCNetCfgGetError.getNSNetworkCondition(err))
		return common.ResultRequeueAfter10sec, err
	}

	ncName := nc.Name
	log.Info("Fetched NetworkConfig from store", "NetworkConfig", ncName)

	systemVpcNetCfg := &v1alpha1.VPCNetworkConfiguration{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: commonservice.SystemVPCNetworkConfigurationName}, systemVpcNetCfg)
	if err != nil {
		r.StatusUpdater.UpdateFail(ctx, networkInfoCR, err, "Failed to get system VPCNetworkConfiguration", setNetworkInfoVPCStatusWithError, nil)
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
		gatewayConnectionReady, gatewayConnectionReason, err = r.Service.ValidateGatewayConnectionStatus(nc)
		log.Info("Got the gateway connection status", "gatewayConnectionReady", gatewayConnectionReady, "gatewayConnectionReason", gatewayConnectionReason)
		if err != nil {
			r.StatusUpdater.UpdateFail(ctx, networkInfoCR, err, fmt.Sprintf("Failed to validate the edge and gateway connection, Org: %s, Porject: %s", nc.Org, nc.NSXProject), setNetworkInfoVPCStatusWithError, nil)
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

	lbProvider, err := r.Service.GetLBProvider()
	if err != nil {
		log.Error(err, "Failed to get LB Provider")
		return common.ResultRequeue, nil
	}
	createdVpc, err := r.Service.CreateOrUpdateVPC(ctx, networkInfoCR, nc, lbProvider)
	if err != nil {
		r.StatusUpdater.UpdateFail(ctx, networkInfoCR, err, "Failed to create or update VPC", setNetworkInfoVPCStatusWithError, nil)
		setNSNetworkReadyCondition(ctx, r.Client, req.Namespace, nsMsgVPCCreateUpdateError.getNSNetworkCondition(err))
		return common.ResultRequeueAfter10sec, err
	}

	var privateIPs []string
	var vpcConnectivityProfilePath string
	var nsxLBSPath string
	isPreCreatedVPC := vpc.IsPreCreatedVPC(*nc)
	if isPreCreatedVPC {
		privateIPs = createdVpc.PrivateIps
		vpcPath := *createdVpc.Path
		vpcConnectivityProfilePath, err = r.GetVpcConnectivityProfilePathByVpcPath(vpcPath)
		if err != nil {
			r.StatusUpdater.UpdateFail(ctx, networkInfoCR, err, fmt.Sprintf("Failed to get VPC connectivity profile path %s", vpcPath), setNetworkInfoVPCStatusWithError, nil)
			return common.ResultRequeueAfter10sec, err
		}
		// Retrieve NSX lbs path if Supervisor is configuring with NSX LB.
		if lbProvider == vpc.NSXLB {
			nsxLBSPath, err = r.Service.GetLBSsFromNSXByVPC(vpcPath)
			if err != nil {
				r.StatusUpdater.UpdateFail(ctx, networkInfoCR, err, fmt.Sprintf("Failed to get NSX LBS path with pre-created VPC %s", vpcPath), setNetworkInfoVPCStatusWithError, nil)
				setNSNetworkReadyCondition(ctx, r.Client, req.Namespace, nsMsgVPCNsxLBSNotReady.getNSNetworkCondition(err))
				return common.ResultRequeueAfter10sec, err
			}
			if nsxLBSPath == "" {
				log.Error(nil, "NSX LB path is not set with pre-created VPC", "VPC", vpcPath)
				err = fmt.Errorf("NSX LB does not exist")
				setNSNetworkReadyCondition(ctx, r.Client, req.Namespace, nsMsgVPCNsxLBSNotReady.getNSNetworkCondition(err))
				return common.ResultRequeueAfter10sec, err
			}
		}
	} else {
		privateIPs = nc.PrivateIPs
		vpcConnectivityProfilePath = nc.VPCConnectivityProfile
		nsxLBSPath = r.Service.GetDefaultNSXLBSPathByVPC(*createdVpc.Id)
	}

	snatIP, aviSubnetPath, aviSECIDR, nsxLBSNATIP, lbIP := "", "", "", "", ""

	vpcConnectivityProfile, err := r.Service.GetVpcConnectivityProfile(nc, vpcConnectivityProfilePath)
	if err != nil {
		r.StatusUpdater.UpdateFail(ctx, networkInfoCR, err, "Failed to get VPC connectivity profile", setNetworkInfoVPCStatusWithError, nil)
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
			state := &v1alpha1.VPCState{
				Name:                    *createdVpc.DisplayName,
				DefaultSNATIP:           "",
				LoadBalancerIPAddresses: "",
				PrivateIPs:              privateIPs,
			}
			r.StatusUpdater.UpdateFail(ctx, networkInfoCR, err, fmt.Sprintf("Failed to read default SNAT IP from VPC: %s", *createdVpc.Id), setNetworkInfoVPCStatusWithError, state)
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
		aviSubnetPath, aviSECIDR, err = r.Service.GetAVISubnetInfo(*createdVpc)
		if err != nil {
			log.Error(err, "Failed to read AVI LB Subnet path and CIDR", "VPC", createdVpc.Id)
			state := &v1alpha1.VPCState{
				Name:                    *createdVpc.DisplayName,
				DefaultSNATIP:           snatIP,
				LoadBalancerIPAddresses: "",
				PrivateIPs:              privateIPs,
			}
			r.StatusUpdater.UpdateFail(ctx, networkInfoCR, err, fmt.Sprintf("Failed to read AVI LB Subnet path and CIDR, VPC: %s", *createdVpc.Id), setNetworkInfoVPCStatusWithError, state)
			setNSNetworkReadyCondition(ctx, r.Client, req.Namespace, nsMsgVPCAviSubnetError.getNSNetworkCondition(err))
			return common.ResultRequeueAfter10sec, err
		}
		lbIP = aviSECIDR
	} else if lbProvider == vpc.NSXLB {
		nsxLBSNATIP, err = r.Service.GetNSXLBSNATIP(*createdVpc)
		if err != nil {
			log.Error(err, "Failed to read NSX LB SNAT IP", "VPC", createdVpc.Id)
			state := &v1alpha1.VPCState{
				Name:                    *createdVpc.DisplayName,
				DefaultSNATIP:           snatIP,
				LoadBalancerIPAddresses: "",
				PrivateIPs:              privateIPs,
			}
			r.StatusUpdater.UpdateFail(ctx, networkInfoCR, err, fmt.Sprintf("Failed to read NSX LB Subnet path and CIDR, VPC: %s", *createdVpc.Id), setNetworkInfoVPCStatusWithError, state)
			setNSNetworkReadyCondition(ctx, r.Client, req.Namespace, nsMsgVPCNSXLBSNATIPError.getNSNetworkCondition(err))
			return common.ResultRequeueAfter10sec, err
		}
		lbIP = nsxLBSNATIP
	}

	state := &v1alpha1.VPCState{
		Name:                    *createdVpc.DisplayName,
		DefaultSNATIP:           snatIP,
		LoadBalancerIPAddresses: lbIP,
		PrivateIPs:              privateIPs,
	}

	// AKO needs to know the AVI subnet path created by NSX
	setVPCNetworkConfigurationStatusWithLBS(ctx, r.Client, ncName, state.Name, aviSubnetPath, nsxLBSPath, *createdVpc.Path)
	r.StatusUpdater.UpdateSuccess(ctx, networkInfoCR, setNetworkInfoVPCStatus, state)

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
				NewQueue:                r.getQueue,
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

	// Start a goroutine to periodically sync the pre-created VPC's private IPs to NetworkInfo CR if used.
	go wait.UntilWithContext(context.Background(), r.syncPreCreatedVpcIPs, preVPCSyncInterval)

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
		// Ignore the terminating Namespaces in the list results.
		if ns.ObjectMeta.DeletionTimestamp.IsZero() {
			nsSet.Insert(ns.Name)
			idSet.Insert(string(ns.UID))
		}
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
		r.StatusUpdater.IncreaseDeleteTotal()

		if err = r.Service.DeleteVPC(*nsxVPC.Path); err != nil {
			log.Error(err, "Failed to delete NSX VPC", "VPC", nsxVPC.Id, "Namespace", nsxVPCNamespaceName)
			r.StatusUpdater.IncreaseDeleteFailTotal()
			continue
		}

		r.StatusUpdater.IncreaseDeleteSuccessTotal()
		log.Info("Successfully deleted NSX VPC", "VPC", nsxVPC.Id)
	}
}

func (r *NetworkInfoReconciler) deleteVPCsByNamespace(ctx context.Context, ns string) error {
	staleVPCs := r.Service.GetVPCsByNamespace(ns)
	if len(staleVPCs) == 0 {
		return nil
	}

	_, idSet, err := r.listNamespaceCRsNameIDSet(ctx)
	if err != nil {
		log.Error(err, "Failed to list Kubernetes Namespaces")
		return fmt.Errorf("failed to list Kubernetes Namespaces while deleting VPCs: %v", err)
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
	vpcNetConfig, err := r.Service.GetVPCNetworkConfigByNamespace(ns)
	if err != nil {
		log.Error(err, "Failed to get VPCNetworkConfig", "Namespace", ns)
		return nil
	}
	if vpcNetConfig != nil {
		updateVPCNetworkConfigurationStatusWithAliveVPCs(ctx, r.Client, vpcNetConfig.Name, r.listVPCsByNetworkConfigName)
	}
	return nil
}

func (r *NetworkInfoReconciler) listVPCsByNetworkConfigName(ncName string) ([]*model.Vpc, error) {
	aliveVPCs := make([]*model.Vpc, 0)
	namespacesUsingNC, err := r.Service.GetNamespacesByNetworkconfigName(ncName)
	if err != nil {
		return aliveVPCs, err
	}
	for _, namespace := range namespacesUsingNC {
		aliveVPCs = append(aliveVPCs, r.Service.GetVPCsByNamespace(namespace)...)
	}
	return aliveVPCs, nil
}

func (r *NetworkInfoReconciler) syncPreCreatedVpcIPs(ctx context.Context) {
	// Construct a map for the existing NetworkInfo CRs, the key is its Namespace, and the value is
	// the NetworkInfo CR.
	networkInfos := &v1alpha1.NetworkInfoList{}
	err := r.Client.List(ctx, networkInfos)
	if err != nil {
		log.Error(err, "Failed to list NetworkInfos")
		return
	}
	networkInfoMap := make(map[string]v1alpha1.NetworkInfo)
	for _, netInfo := range networkInfos.Items {
		networkInfoMap[netInfo.Namespace] = netInfo
	}

	// Read all VPCs from NSX. Note, we can't use the cached VPC from local store for pre-created VPC case.
	vpcPathMap := r.Service.GetAllVPCsFromNSX()

	retry.OnError(util.K8sClientRetry, func(err error) bool {
		return err != nil
	}, func() error {
		nsVpcMap, err := r.Service.GetNamespacesWithPreCreatedVPCs()
		if err != nil {
			return err
		}

		for ns, vpcPath := range nsVpcMap {
			// Continue if no NetworkInfo exists in the Namespace.
			netInfo, found := networkInfoMap[ns]
			if !found {
				continue
			}

			// Note: we don't process the case in this routine if VPC is not ready in a NetworkInfo. The reconciler
			// shall handle this case.
			if len(netInfo.VPCs) == 0 {
				continue
			}

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      netInfo.Name,
					Namespace: netInfo.Namespace,
				},
			}

			preVPC, found := vpcPathMap[vpcPath]
			if !found {
				// Notify Reconciler that the pre-created VPC does not exit.
				r.queue.Add(req)
				continue
			}

			vpcState := netInfo.VPCs[0]
			if !nsxutil.CompareArraysWithoutOrder(preVPC.PrivateIps, vpcState.PrivateIPs) {
				// Notify Reconciler that the pre-created VPC has changed private IPs.
				r.queue.Add(req)
				continue
			}
			// TODO: add the check on SNAT IP changed, and LB type changed in future if needed.
		}
		return nil
	})

}

func (r *NetworkInfoReconciler) getQueue(controllerName string, rateLimiter workqueue.TypedRateLimiter[reconcile.Request]) workqueue.TypedRateLimitingInterface[reconcile.Request] {
	if r.queue == nil {
		r.queue = workqueue.NewTypedRateLimitingQueueWithConfig(rateLimiter, workqueue.TypedRateLimitingQueueConfig[reconcile.Request]{
			Name: controllerName,
		})
	}
	return r.queue
}

func (r *NetworkInfoReconciler) getNetworkConfigInfo(ctx context.Context, networkInfoCR *v1alpha1.NetworkInfo) (*commonservice.VPCNetworkConfigInfo, error) {
	ncName, err := r.Service.GetNetworkconfigNameFromNS(ctx, networkInfoCR.Namespace)
	if err != nil {
		log.Error(err, "Failed to get NetworkConfig name for VPC when creating NSX VPC", "NetworkInfo", networkInfoCR.Name)
		return nil, err
	}
	nc, _exist, err := r.Service.GetVPCNetworkConfig(ncName)
	if err != nil {
		log.Error(err, "Failed to get VPCNetworkConfig", "Name", ncName)
		return nil, err
	}
	if !_exist {
		log.Error(nil, fmt.Sprintf("NetworkConfig %s does not exist when creating NSX VPC", ncName))
		return nil, fmt.Errorf("NetworkConfig %s not found", ncName)
	}
	return nc, nil
}
