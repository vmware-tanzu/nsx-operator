/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	vmv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	_ "go.uber.org/automaxprocs"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	crdv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/inventory"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/ipaddressallocation"
	namespacecontroller "github.com/vmware-tanzu/nsx-operator/pkg/controllers/namespace"
	networkinfocontroller "github.com/vmware-tanzu/nsx-operator/pkg/controllers/networkinfo"
	networkpolicycontroller "github.com/vmware-tanzu/nsx-operator/pkg/controllers/networkpolicy"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/node"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/pod"
	securitypolicycontroller "github.com/vmware-tanzu/nsx-operator/pkg/controllers/securitypolicy"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/service"
	staticroutecontroller "github.com/vmware-tanzu/nsx-operator/pkg/controllers/staticroute"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/subnet"
	subnetbindingcontroller "github.com/vmware-tanzu/nsx-operator/pkg/controllers/subnetbinding"
	subnetipreservationcontroller "github.com/vmware-tanzu/nsx-operator/pkg/controllers/subnetipreservation"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/subnetset"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/health"
	inventoryservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/inventory"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/ipblocksinfo"
	nodeservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/node"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/staticroute"
	subnetservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	subnetbindingservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetbinding"
	subnetipreservationservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetipreservation"
	subnetportservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"

	nsxserviceaccountcontroller "github.com/vmware-tanzu/nsx-operator/pkg/controllers/nsxserviceaccount"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	ipaddressallocationservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/ipaddressallocation"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	pkgutil "github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	scheme               = runtime.NewScheme()
	log                  logger.CustomLogger
	cf                   *config.NSXOperatorConfig
	nsxOperatorNamespace = "default"
	nsxOperatorPodName   = "default"
	roleKey              = "nsx-operator-role"
	roleMaster           = "master"
	roleStandby          = "standby"
	restoreMode          = false
)

func init() {
	var err error
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(crdv1alpha1.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(vmv1alpha1.AddToScheme(scheme))
	config.AddFlags()

	cf, err = config.NewNSXOperatorConfigFromFile()
	if err != nil {
		os.Exit(1)
	}

	log = logger.ZapCustomLogger(cf.DefaultConfig.Debug, config.LogLevel)
	logger.Log = log
	// Set the controller-runtime logger to prevent the warning about log.SetLogger(...) never being called
	logf.SetLogger(log.Logger)

	if os.Getenv("NSX_OPERATOR_NAMESPACE") != "" {
		nsxOperatorNamespace = os.Getenv("NSX_OPERATOR_NAMESPACE")
	}
	if os.Getenv("NSX_OPERATOR_NAME") != "" {
		nsxOperatorPodName = os.Getenv("NSX_OPERATOR_NAME")
	}

	if cf.HAEnabled() {
		log.Info("HA mode enabled")
	} else {
		log.Info("HA mode disabled")
	}

	if metrics.AreMetricsExposed(cf) {
		metrics.InitializePrometheusMetrics()
	}
}

func startServiceController(mgr manager.Manager, nsxClient *nsx.Client) {
	// Generate webhook certificates and start refreshing webhook certificates periodically
	if cf.CoeConfig.EnableVPCNetwork {
		if err := pkgutil.GenerateWebhookCerts(); err != nil {
			log.Error(err, "Failed to generate webhook certificates")
			os.Exit(1)
		}
		log.Info("Successfully generated webhook certificates")
		go refreshCertPeriodically()
	}

	// Initialize and start the system health reporter
	if cf.CoeConfig.EnableVPCNetwork && cf.EnableInventory && cf.CoeConfig.EnableSha {
		health.Start(nsxClient, cf, mgr.GetClient())
	}

	//  Embed the common commonService to sub-services.
	commonService := common.Service{
		Client:    mgr.GetClient(),
		NSXClient: nsxClient,
		NSXConfig: cf,
	}

	checkLicense(nsxClient, cf.LicenseValidationInterval)

	if cf.K8sConfig.EnableRestore && cf.CoeConfig.EnableVPCNetwork {
		var err error
		restoreMode, err = pkgutil.CompareNSXRestore(mgr.GetClient(), nsxClient)
		if err != nil {
			log.Error(err, "NSX restore check failed")
			os.Exit(1)
		}
	} else {
		restoreMode = false
	}

	var reconcilerList []pkgutil.ReconcilerProvider

	var vpcService *vpc.VPCService
	var hookServer webhook.Server
	var subnetSetReconcile *subnetset.SubnetSetReconciler

	if cf.CoeConfig.EnableVPCNetwork {
		// Check NSX version for VPC networking mode
		if !commonService.NSXClient.NSXCheckVersion(nsx.VPC) {
			log.Error(nil, "VPC mode cannot be enabled if NSX version is lower than 4.1.1")
			os.Exit(1)
		}
		log.Info("VPC mode is enabled")

		var err error
		vpcService, err = vpc.InitializeVPC(commonService)
		if err != nil {
			log.Error(err, "Failed to initialize vpc commonService", "controller", "VPC")
			os.Exit(1)
		}
		subnetService, err := subnetservice.InitializeSubnetService(commonService)
		if err != nil {
			log.Error(err, "Failed to initialize subnet commonService")
			os.Exit(1)
		}
		ipAddressAllocationService, err := ipaddressallocationservice.InitializeIPAddressAllocation(commonService, vpcService, false)
		if err != nil {
			log.Error(err, "Failed to initialize ipaddressallocation commonService", "controller", "IPAddressAllocation")
		}
		subnetPortService, err := subnetportservice.InitializeSubnetPort(commonService, vpcService, ipAddressAllocationService)
		if err != nil {
			log.Error(err, "Failed to initialize subnetport commonService", "controller", "SubnetPort")
			os.Exit(1)
		}
		nodeService, err := nodeservice.InitializeNode(commonService)
		if err != nil {
			log.Error(err, "Failed to initialize node commonService", "controller", "Node")
			os.Exit(1)
		}
		staticRouteService, err := staticroute.InitializeStaticRoute(commonService, vpcService)
		if err != nil {
			log.Error(err, "Failed to initialize staticroute commonService", "controller", "StaticRoute")
			os.Exit(1)
		}
		ipblocksInfoService := ipblocksinfo.InitializeIPBlocksInfoService(commonService, subnetService)

		subnetBindingService, err := subnetbindingservice.InitializeService(commonService)
		if err != nil {
			log.Error(err, "Failed to initialize SubnetConnectionBindingMap commonService")
			os.Exit(1)
		}
		var inventoryService *inventoryservice.InventoryService
		if cf.EnableInventory {
			inventoryService, err = inventoryservice.InitializeService(commonService, false)
			if err != nil {
				log.Error(err, "Failed to initialize inventory commonService", "controller", "Inventory")
				os.Exit(1)
			}
		}
		subnetIPReservationService, err := subnetipreservationservice.InitializeService(commonService)
		if err != nil {
			log.Error(err, "Failed to initialize SubnetIPReservation commonService", "controller", "SubnetIPReservation")
			os.Exit(1)
		}

		if _, err := os.Stat(config.WebhookCertDir); errors.Is(err, os.ErrNotExist) {
			log.Error(err, "Server cert not found, disabling webhook server", "cert", config.WebhookCertDir)
		} else {
			hookServer = webhook.NewServer(webhook.Options{
				Port:    config.WebhookServerPort,
				CertDir: config.WebhookCertDir,
			})
			if err := mgr.Add(hookServer); err != nil {
				log.Error(err, "Failed to add hook server")
				os.Exit(1)
			}
		}

		// Create controllers which only supports VPC
		subnetSetReconcile = subnetset.NewSubnetSetReconciler(mgr, subnetService, subnetPortService, vpcService, subnetBindingService)
		reconcilerList = append(
			reconcilerList,
			networkinfocontroller.NewNetworkInfoReconciler(mgr, vpcService, ipblocksInfoService),
			namespacecontroller.NewNamespaceReconciler(mgr, cf, vpcService, subnetService),
			subnet.NewSubnetReconciler(mgr, subnetService, subnetPortService, vpcService, subnetBindingService),
			subnetSetReconcile,
			node.NewNodeReconciler(mgr, nodeService),
			staticroutecontroller.NewStaticRouteReconciler(mgr, staticRouteService),
			// SubnetPort may use IPAddressAllocation for AddressBinding, reconcile IPAddressAllocation first
			ipaddressallocation.NewIPAddressAllocationReconciler(mgr, ipAddressAllocationService, vpcService),
			subnetport.NewSubnetPortReconciler(mgr, subnetPortService, subnetService, vpcService, ipAddressAllocationService),
			pod.NewPodReconciler(mgr, subnetPortService, subnetService, vpcService, nodeService),
			networkpolicycontroller.NewNetworkPolicyReconciler(mgr, commonService, vpcService),
			service.NewServiceLbReconciler(mgr, commonService),
			subnetbindingcontroller.NewReconciler(mgr, subnetService, subnetBindingService),
			subnetipreservationcontroller.NewReconciler(mgr, subnetIPReservationService, subnetService),
		)
		if cf.EnableInventory {
			reconcilerList = append(reconcilerList, inventory.NewInventoryController(mgr.GetClient(), inventoryService, cf))
		}
	}

	// Add controllers which can run in non-VPC mode
	reconcilerList = append(reconcilerList, securitypolicycontroller.NewSecurityPolicyReconciler(mgr, commonService, vpcService))

	// Add the NSXServiceAccount controller.
	if cf.EnableAntreaNSXInterworking {
		reconcilerList = append(reconcilerList, nsxserviceaccountcontroller.NewNSXServiceAccountReconciler(mgr, commonService))
	}

	if restoreMode {
		subnetSetReconcile.EnableRestoreMode()
		err := pkgutil.ProcessRestore(reconcilerList, mgr.GetClient())
		if err != nil {
			log.Error(err, "Failed to process restore")
			os.Exit(1)
		}
		log.Info("Restore config update succeeds, restart to enter normal mode...")
		os.Exit(0)
	}

	log.Info("Enter normal mode")
	for _, reconciler := range reconcilerList {
		if reconciler != nil {
			if err := reconciler.StartController(mgr, hookServer); err != nil {
				log.Error(err, "Failed to start the controllers")
				os.Exit(1)
			}
		}
	}

	// Update pod labels to determine if this pod is the master
	err := updatePodLabels(mgr)
	if err != nil {
		log.Error(err, "Failed to update Pod labels")
		panic(err)
	}
}

func electMaster(mgr manager.Manager, nsxClient *nsx.Client) {
	log.Info("I'm trying to be elected as master")
	<-mgr.Elected()
	log.Info("I'm the master now")
	// In HA mode, there can be a brief period where both the old and new leader
	// operators are active simultaneously. After a time synchronization by NTP,
	// the new operator may acquire the lease before the old operator recognizes
	// it has lost the lease, leading to a potential race condition. To mitigate this,
	// the new master operator is configured to wait for 15 seconds, which is
	// slightly longer than the default Leader Election Renew Deadline (10 seconds),
	// ensuring a smooth transition.
	log.Info("Waiting a 15-second delay to let the old instance know that it has lost its lease")
	time.Sleep(15 * time.Second)
	startServiceController(mgr, nsxClient)
}

func main() {
	log.Info("Starting NSX Operator")
	cfg, err := pkgutil.GetConfig()
	if err != nil {
		log.Error(err, "Failed to get rest config for manager")
		os.Exit(1)
	}
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                  scheme,
		HealthProbeBindAddress:  config.ProbeAddr,
		Metrics:                 metricsserver.Options{BindAddress: config.MetricsAddr},
		LeaderElection:          cf.HAEnabled(),
		LeaderElectionNamespace: nsxOperatorNamespace,
		LeaderElectionID:        "nsx-operator",
	})
	if err != nil {
		log.Error(err, "Failed to init manager")
		os.Exit(1)
	}

	// nsxClient is used to interact with NSX API.
	nsxClient := nsx.GetClient(cf)
	if nsxClient == nil {
		log.Error(nil, "Failed to get nsx client")
		os.Exit(1)
	}

	if cf.HAEnabled() {
		go electMaster(mgr, nsxClient)
	} else {
		go startServiceController(mgr, nsxClient)
	}

	if metrics.AreMetricsExposed(cf) {
		go updateHealthMetricsPeriodically(nsxClient)
	}

	if err := mgr.AddHealthzCheck("healthz", nsxClient.NSXChecker.CheckNSXHealth); err != nil {
		log.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	log.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "Failed to start manager")
		os.Exit(1)
	}
}

// Function for fetching nsx health status and feeding it to the prometheus metric.
func getHealthStatus(nsxClient *nsx.Client) error {
	status := 1
	if err := nsxClient.NSXChecker.CheckNSXHealth(nil); err != nil {
		status = 0
	}
	// Record the new health status in metric.
	metrics.NSXOperatorHealthStats.Set(float64(status))
	return nil
}

// Periodically fetches health info.
func updateHealthMetricsPeriodically(nsxClient *nsx.Client) {
	for {
		if err := getHealthStatus(nsxClient); err != nil {
			log.Error(err, "Failed to fetch health info")
		}
		select {
		case <-time.After(metrics.ScrapeTimeout):
		}
	}
}

func checkLicense(nsxClient *nsx.Client, interval int) {
	err := nsxClient.ValidateLicense(true)
	if err != nil {
		os.Exit(1)
	}
	// if there is no dfw license enabled, check the license more frequently
	// if the customer set it in config, use it, else use licenseTimeoutNoDFW
	if interval == 0 {
		if !util.GetDFWLicense() {
			interval = config.LicenseIntervalForDFW
		} else {
			interval = config.LicenseInterval
		}
	}
	go updateLicensePeriodically(nsxClient, time.Duration(interval)*time.Second)
}

func updateLicensePeriodically(nsxClient *nsx.Client, interval time.Duration) {
	for {
		select {
		case <-time.After(interval):
		}
		err := nsxClient.ValidateLicense(false)
		if err != nil {
			os.Exit(1)
		}
	}
}

func refreshCertPeriodically() {
	ticker := time.NewTicker(30 * 24 * time.Hour) // 30 days
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			log.Info("Refreshing webhook certificates...")
			if err := pkgutil.GenerateWebhookCerts(); err != nil {
				log.Error(err, "Failed to refresh webhook certificates")
			} else {
				log.Info("Successfully refreshed webhook certificates")
			}
		}
	}
}

// updatePodLabels updates the role label of pods based on the master election.
func updatePodLabels(mgr manager.Manager) error {
	c := mgr.GetClient()
	// Fetch all pods in the given namespace
	podList := &corev1.PodList{}
	if err := c.List(context.TODO(), podList, &client.ListOptions{Namespace: nsxOperatorNamespace}); err != nil {
		return fmt.Errorf("failed to list Pods in Namespace %s: %w", nsxOperatorNamespace, err)
	}

	// Iterate over the pods and update the role labels
	for _, po := range podList.Items {
		targetRole := roleStandby
		if po.Name == nsxOperatorPodName {
			targetRole = roleMaster
		}

		if po.Labels[roleKey] != targetRole {
			patch := client.MergeFrom(po.DeepCopy())
			po.Labels[roleKey] = targetRole
			if err := c.Patch(context.TODO(), &po, patch); err != nil {
				return fmt.Errorf("failed to update labels for Pod %s: %w", po.Name, err)
			}
			log.Info("Updated Pod labels", "pod", po.Name, "labels", po.Labels)
		}
	}

	return nil
}
