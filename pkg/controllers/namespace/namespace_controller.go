/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package namespace

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"

	v1 "k8s.io/api/core/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	types "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	AnnotationNamespaceVPCError = " nsx.vmware.com/vpc_error"

	log = logger.Log
)

// NamespaceReconciler process namespace create/delete event
type NamespaceReconciler struct {
	Client    client.Client
	Scheme    *apimachineryruntime.Scheme
	NSXConfig *config.NSXOperatorConfig
}

func (r *NamespaceReconciler) createVPCCR(ctx *context.Context, obj client.Object, ns string, ncName string, vpcName *string) (*v1alpha1.VPC, error) {
	// check if vpc cr already exist under this namespace
	vpcs := &v1alpha1.VPCList{}
	r.Client.List(*ctx, vpcs, client.InNamespace(ns))
	if len(vpcs.Items) > 0 {
		// if there is already one vpc exist under this ns, return this vpc.
		log.Info("vpc cr already exist, skip creating", "VPC", vpcs.Items[0].Name)
		return &vpcs.Items[0], nil
	}
	nc, ncExist := common.ServiceMediator.GetVPCNetworkConfig(ncName)
	if !ncExist {
		message := fmt.Sprintf("missing network config %s for namespace %s", ncName, ns)
		r.namespaceError(ctx, obj, message, nil)
		return nil, errors.New(message)
	}

	//TODO: in next patch, remove this validation. If user did not provide private cidr
	// use a hardcoded cidr to create a private ip block.
	if !common.ServiceMediator.ValidateNetworkConfig(nc) {
		// if netwrok config is not valid, no need to retry, skip processing
		message := fmt.Sprintf("invalid network config %s for namespace %s, missing private cidr", ncName, ns)
		r.namespaceError(ctx, obj, message, nil)
		return nil, errors.New(message)
	}

	// create vpc cr with exisitng vpc network config
	vpcCR := BuildVPCCR(ns, ncName, vpcName)
	err := r.Client.Create(*ctx, vpcCR)
	if err != nil {
		message := "failed to create VPC CR"
		r.namespaceError(ctx, obj, message, err)
		// If create VPC CR failed, put ns create event back to queue.
		return nil, err
	}

	changes := map[string]string{
		AnnotationNamespaceVPCError: "",
	}
	util.UpdateK8sResourceAnnotation(r.Client, ctx, obj, changes)
	log.Info("create vpc CR", "VPC", vpcCR.Name, "Namespace", vpcCR.Namespace)
	return vpcCR, nil
}

func (r *NamespaceReconciler) namespaceError(ctx *context.Context, k8sObj client.Object, msg string, err error) {
	logErr := util.If(err == nil, errors.New(msg), err).(error)
	log.Error(logErr, msg)
	changes := map[string]string{AnnotationNamespaceVPCError: msg}
	util.UpdateK8sResourceAnnotation(r.Client, ctx, k8sObj, changes)
}

func (r *NamespaceReconciler) insertNamespaceNetworkconfigBinding(ns string, anno map[string]string) {
	if anno == nil {
		log.Info("empty annotation for namespace", "Namespace", ns)
		return
	}

	ncName, ncExist := anno[types.AnnotationVPCNetworkConfig]
	if !ncExist {
		ncName = types.DefaultNetworkConfigName
	}

	log.V(2).Info("insert namespace and network config mapping", "Namespace", ns, "Networkconfig", ncName)
	common.ServiceMediator.RegisterNamespaceNetworkconfigBinding(ns, ncName)
}

/*
	VPC creation strategy:

We suppose namespace should have following annotations:
  - "nsx.vmware.com/vpc_name": "<Namespace Name>/<Supervisor ID>"
    If the ns contains this annotation, first check if	the namespace in annotation is the same as
    the one in ns event, if yes, create an infra VPC for it. if	not, skip the whole ns event as the infra
    VPC will be created its corresponding ns creation event.
  - "nsx.vmware.com/vpc_network_config":"<Supervisor ID>"
    If ns do not contains "nsx.vmware.com/vpc_name" annotation. Use this annotation to handle VPC creation.
    VPC will locate the network config with the CR name, and create VPC using its config.
  - If the ns do not have either of the annotation above, then we believe it is using default VPC, try to search
    default VPC in network config CR store. The default VPC network config CR's name is "default".
*/
func (r *NamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	obj := &v1.Namespace{}
	log.Info("reconciling K8s namespace", "namespace", req.NamespacedName)
	metrics.CounterInc(r.NSXConfig, metrics.ControllerSyncTotal, common.MetricResTypeNamespace)

	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		log.Error(err, "unable to fetch namespace", "req", req.NamespacedName)
		return common.ResultRequeueAfter5mins, err
	}

	// processing create/update event
	ns := obj.GetName()
	if obj.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.NSXConfig, metrics.ControllerUpdateTotal, common.MetricResTypeNamespace)
		log.Info("start processing namespace create/update event", "namespace", ns)
		ctx := context.Background()
		annotations := obj.GetAnnotations()
		r.insertNamespaceNetworkconfigBinding(ns, annotations)
		// read anno "nsx.vmware.com/vpc_name", if ns contains this annotation, it means it will share
		// infra VPC, if the ns in the annotation is the same as ns event, create infra VPC, if not,
		// skip the event.
		ncName, ncExist := annotations[types.AnnotationVPCNetworkConfig]
		vpcName, nameExist := annotations[types.AnnotationVPCName]
		var create_vpc_name *string
		if nameExist {
			log.Info("read ns annotation vpcName", "VPCNAME", vpcName)
			res := strings.Split(vpcName, "/")
			// The format should be namespace/vpc_name
			if len(res) != 2 {
				message := fmt.Sprintf("incorrect vpcName annotation %s for namespace %s", vpcName, ns)
				r.namespaceError(&ctx, obj, message, nil)
				// If illegal format, skip handling this event?
				return common.ResultNormal, nil
			}
			log.Info("start to handle vpcName anno", "VPCNS", res[1], "NS", ns)

			if ns != res[0] {
				log.Info("name space is using shared vpc, with vpc name anno", "VPCNAME", vpcName, "Namespace", ns)
				return common.ResultNormal, nil
			}
			create_vpc_name = &res[1]
			log.Info("creating vpc using customer defined vpc name", "VPCName", res[1])
		}

		// If ns do not have network config name tag, then use default vpc network config name
		if !ncExist {
			log.Info("network config name not found on ns, using default network config", "Namespace", ns)
			ncName = types.DefaultNetworkConfigName
		}

		if _, err := r.createVPCCR(&ctx, obj, ns, ncName, create_vpc_name); err != nil {
			return common.ResultRequeueAfter10sec, nil
		}
		return common.ResultNormal, nil
	} else {
		log.Info("skip ns deletion event for ns", "Namespace", ns)
		metrics.CounterInc(r.NSXConfig, metrics.ControllerDeleteTotal, common.MetricResTypeNamespace)
		common.ServiceMediator.UnRegisterNamespaceNetworkconfigBinding(obj.GetNamespace())
		return common.ResultNormal, nil
	}
}

func (r *NamespaceReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Namespace{}).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: runtime.NumCPU(),
			}).
		Complete(r)
}

// Start setup manager and launch GC
func (r *NamespaceReconciler) Start(mgr ctrl.Manager) error {
	return r.setupWithManager(mgr)
}
