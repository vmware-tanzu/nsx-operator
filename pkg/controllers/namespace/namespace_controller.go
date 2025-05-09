/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package namespace

import (
	"context"
	"errors"
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	types "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log                 = &logger.Log
	MetricResTypeSubnet = common.MetricResTypeSubnet
)

// NamespaceReconciler process Namespace create/delete event
// Using vpcservice provider instead of vpc service to prevent
// invoking method that should be exposed to other module.
type NamespaceReconciler struct {
	Client              client.Client
	Scheme              *apimachineryruntime.Scheme
	NSXConfig           *config.NSXOperatorConfig
	VPCService          types.VPCServiceProvider
	SubnetService       *subnet.SubnetService
	Recorder            record.EventRecorder
	SubnetStatusUpdater common.StatusUpdater
}

func (r *NamespaceReconciler) getDefaultNetworkConfigName() (string, error) {
	nc, err := r.VPCService.GetDefaultNetworkConfig()
	if err != nil {
		return "", fmt.Errorf("default NetworkConfig not found: %w", err)
	}
	return nc.Name, nil
}

func (r *NamespaceReconciler) createNetworkInfoCR(ctx context.Context, obj client.Object, ns string) (*v1alpha1.NetworkInfo, error) {
	networkInfos := &v1alpha1.NetworkInfoList{}
	if err := r.Client.List(ctx, networkInfos, client.InNamespace(ns)); err != nil {
		return nil, fmt.Errorf("failed to list NetworkInfo CRs: %w", err)
	}

	if len(networkInfos.Items) > 0 {
		// if there is already one networkInfo, return this networkInfo
		log.Info("NetworkInfo already exists", "NetworkInfo", networkInfos.Items[0].Name, "Namespace", ns)
		return &networkInfos.Items[0], nil
	}

	// create networkInfo cr with existing vpc network config
	networkInfoCR := &v1alpha1.NetworkInfo{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ns,
			Namespace: ns,
		},
		VPCs: []v1alpha1.VPCState{},
	}
	if err := r.Client.Create(ctx, networkInfoCR); err != nil {
		r.namespaceError(ctx, obj, "failed to create NetworkInfo CR", err)
		// If create NetworkInfo CR failed, put Namespace create event back to queue.
		return nil, err
	}

	changes := map[string]string{common.AnnotationNamespaceVPCError: ""}
	util.UpdateK8sResourceAnnotation(r.Client, ctx, obj, changes)

	log.Info("Created NetworkInfo CR", "NetworkInfo", networkInfoCR.Name, "Namespace", networkInfoCR.Namespace)
	return networkInfoCR, nil
}

func getAccessMode(name string) v1alpha1.AccessMode {
	switch name {
	case types.DefaultVMSubnetSet:
		return v1alpha1.AccessMode(v1alpha1.AccessModePrivate)
	case types.DefaultPodSubnetSet:
		return v1alpha1.AccessMode(v1alpha1.AccessModeProject)
	default:
		return ""
	}
}

func (r *NamespaceReconciler) createDefaultSubnetSet(ctx context.Context, ns string, defaultSubnetSize int) error {
	defaultSubnetSets := map[string]string{
		types.DefaultVMSubnetSet:  types.LabelDefaultVMSubnetSet,
		types.DefaultPodSubnetSet: types.LabelDefaultPodSubnetSet,
	}
	for name, subnetSetType := range defaultSubnetSets {
		if err := retry.OnError(retry.DefaultRetry, func(err error) bool {
			return err != nil
		}, func() error {
			list := &v1alpha1.SubnetSetList{}
			label := client.MatchingLabels{
				types.LabelDefaultSubnetSet: subnetSetType,
			}
			if err := r.Client.List(context.Background(), list, label, client.InNamespace(ns)); err != nil {
				return err
			}
			if len(list.Items) > 0 {
				log.Info("Default SubnetSet already exists", types.LabelDefaultSubnetSet, subnetSetType)
				return nil
			}
			obj := &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: ns,
					Name:      name,
					Labels:    map[string]string{types.LabelDefaultSubnetSet: subnetSetType},
				},
				Spec: v1alpha1.SubnetSetSpec{
					AccessMode:     getAccessMode(name),
					IPv4SubnetSize: defaultSubnetSize,
				},
			}

			return r.Client.Create(ctx, obj)
		}); err != nil {
			log.Error(err, "Failed to create SubnetSet", "Namespace", ns, "Name", name)
			return err
		}
	}
	return nil
}

func (r *NamespaceReconciler) deleteDefaultSubnetSet(ns string) error {
	subnetSets := []string{types.DefaultVMSubnetSet, types.DefaultPodSubnetSet}

	for _, name := range subnetSets {
		err := retry.OnError(retry.DefaultRetry, func(err error) bool { return err != nil }, func() error {
			obj := &v1alpha1.SubnetSet{}
			if err := r.Client.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, obj); err != nil {
				return client.IgnoreNotFound(err)
			}
			log.Info("Delete default SubnetSet", "Namespace", ns, "Name", name)
			return r.Client.Delete(context.Background(), obj)
		})

		if err != nil {
			log.Error(err, "Failed to delete SubnetSet", "Namespace", ns, "Name", name)
			return err
		}
	}
	return nil
}

func (r *NamespaceReconciler) namespaceError(ctx context.Context, k8sObj client.Object, msg string, err error) {
	logErr := util.If(err == nil, errors.New(msg), err).(error)
	log.Error(logErr, msg)
	changes := map[string]string{common.AnnotationNamespaceVPCError: msg}
	util.UpdateK8sResourceAnnotation(r.Client, ctx, k8sObj, changes)
}

/*
	VPC creation strategy:

We suppose Namespace should have following annotations:
  - "nsx.vmware.com/shared_vpc_namespace": "<Namespace Name>"
    If the Namespace contains this annotation, first check if the Namespace in annotation is the same as
    the one in Namespace event, if yes, create an infra VPC for it. if	not, skip the whole Namespace event as the infra
    VPC will be created its corresponding Namespace creation event.
  - "nsx.vmware.com/vpc_network_config":"<Supervisor ID>"
    If Namespace does not contain "nsx.vmware.com/shared_vpc_namespace" annotation. Use this annotation to handle VPC creation.
    VPC will locate the network config with the CR name, and create VPC using its config.
  - If the Namespace do not have either of the annotation above, then we believe it is using default VPC, try to search
    default VPC in network config CR store. The default VPC network config CR's name is "default".
*/
func (r *NamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startTime := time.Now()
	defer func() {
		log.Info("Finished reconciling Namespace", "Namespace", req.NamespacedName, "duration(ms)", time.Since(startTime).Milliseconds())
	}()
	metrics.CounterInc(r.NSXConfig, metrics.ControllerSyncTotal, common.MetricResTypeNamespace)

	obj := &v1.Namespace{}
	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		log.Error(err, "Unable to fetch Namespace", "Namespace", req.NamespacedName)
		return common.ResultNormal, client.IgnoreNotFound(err)
	}

	// processing create/update event
	ns := obj.GetName()
	if obj.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.NSXConfig, metrics.ControllerUpdateTotal, common.MetricResTypeNamespace)
		log.Info("Start processing Namespace create/update event", "Namespace", ns)

		ctx := context.Background()
		ncName, err := r.VPCService.GetNetworkconfigNameFromNS(ctx, ns)
		if err != nil {
			log.Error(err, "Failed to get NetworkConfig name", "Namespace", ns)
			return common.ResultRequeueAfter10sec, nil
		}

		nc, ncExist, err := r.VPCService.GetVPCNetworkConfig(ncName)
		if err != nil {
			log.Error(err, "Failed to get NetworkConfig", "Namespace", ncName)
			return common.ResultRequeue, nil
		}
		if !ncExist {
			message := fmt.Sprintf("missing NetworkConfig %s for Namespace %s", ncName, ns)
			r.namespaceError(ctx, obj, message, nil)
			return common.ResultRequeueAfter10sec, errors.New(message)
		}
		if err = r.VPCService.ValidateNetworkConfig(nc); err != nil {
			// if network config is not valid, no need to retry, skip processing
			message := fmt.Sprintf("invalid NetworkConfig %s for Namespace %s, error: %v", ncName, ns, err)
			r.namespaceError(ctx, obj, message, nil)
			return common.ResultRequeueAfter10sec, errors.New(message)
		}

		if _, err := r.createNetworkInfoCR(ctx, obj, ns); err != nil {
			return common.ResultRequeueAfter10sec, err
		}
		if err := r.createDefaultSubnetSet(ctx, ns, nc.Spec.DefaultSubnetSize); err != nil {
			return common.ResultRequeueAfter10sec, err
		}

		// Sync shared subnets, look into shared subnets in vpcnetworkconfigurations,
		// for new shared subnets, create Subnet CRs,
		// for deleted shared subnets, delete Subnet CRs
		if err := r.syncSharedSubnets(ctx, ns, nc); err != nil {
			log.Error(err, "Failed to sync shared Subnets", "Namespace", ns)
			return common.ResultRequeue, err
		}

		return common.ResultNormal, nil
	} else {
		metrics.CounterInc(r.NSXConfig, metrics.ControllerDeleteTotal, common.MetricResTypeNamespace)
		// actively delete default SubnetSet, so that SubnetSet webhook can admit the delete request
		if err := r.deleteDefaultSubnetSet(ns); err != nil {
			return common.ResultRequeueAfter10sec, err
		}
		return common.ResultNormal, nil
	}
}

func (r *NamespaceReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Namespace{}).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Watches(
			&v1alpha1.VPCNetworkConfiguration{},
			&EnqueueRequestForVPCNetworkConfiguration{Reconciler: r},
			builder.WithPredicates(PredicateFuncsVPCNetworkConfig),
		).
		Complete(r)
}

// Start setup manager and launch GC
func (r *NamespaceReconciler) Start(mgr ctrl.Manager) error {
	return r.setupWithManager(mgr)
}

func (r *NamespaceReconciler) RestoreReconcile() error {
	return nil
}

func (r *NamespaceReconciler) CollectGarbage(_ context.Context) error {
	return nil
}

func (r *NamespaceReconciler) StartController(mgr ctrl.Manager, _ webhook.Server) error {
	if err := r.Start(mgr); err != nil {
		log.Error(err, "Failed to create namespace controller", "controller", "Namespace")
		return err
	}
	return nil
}

func NewNamespaceReconciler(mgr ctrl.Manager, cf *config.NSXOperatorConfig, vpcService types.VPCServiceProvider,
	subnetService *subnet.SubnetService) *NamespaceReconciler {
	nsReconciler := &NamespaceReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		NSXConfig:     cf,
		VPCService:    vpcService,
		SubnetService: subnetService,
		Recorder:      mgr.GetEventRecorderFor("namespace-controller"),
	}
	nsReconciler.SubnetStatusUpdater = common.NewStatusUpdater(nsReconciler.Client, nsReconciler.SubnetService.NSXConfig, nsReconciler.Recorder, MetricResTypeSubnet, "Subnet", "Subnet")
	return nsReconciler
}
