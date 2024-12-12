package common

import (
	"context"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	MetricResTypeSecurityPolicy             = "securitypolicy"
	MetricResTypeNetworkPolicy              = "networkpolicy"
	MetricResTypeIPPool                     = "ippool"
	MetricResTypeIPAddressAllocation        = "ipaddressallocation"
	MetricResTypeNSXServiceAccount          = "nsxserviceaccount"
	MetricResTypeSubnetPort                 = "subnetport"
	MetricResTypeStaticRoute                = "staticroute"
	MetricResTypeSubnet                     = "subnet"
	MetricResTypeSubnetSet                  = "subnetset"
	MetricResTypeSubnetConnectionBindingMap = "subnetconnectionbindingmap"
	MetricResTypeNetworkInfo                = "networkinfo"
	MetricResTypeNamespace                  = "namespace"
	MetricResTypePod                        = "pod"
	MetricResTypeNode                       = "node"
	MetricResTypeServiceLb                  = "servicelb"
	MaxConcurrentReconciles                 = 8
	NSXOperatorError                        = "nsx-op/error"
	//sync the error with NCP side
	ErrorNoDFWLicense = "NO_DFW_LICENSE"

	LabelK8sMasterRole  = "node-role.kubernetes.io/master"
	LabelK8sControlRole = "node-role.kubernetes.io/control-plane"
)

var (
	ResultNormal  = ctrl.Result{}
	ResultRequeue = ctrl.Result{Requeue: true}
	// for k8s events that need to retry in short loop, eg: namespace creation
	ResultRequeueAfter10sec = ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second}
	ResultRequeueAfter60sec = ctrl.Result{Requeue: true, RequeueAfter: 60 * time.Second}
	// for unstable event, eg: failed to k8s resources when reconciling, may due to k8s unstable
	ResultRequeueAfter5mins = ctrl.Result{Requeue: true, RequeueAfter: 5 * time.Minute}
)

const (
	ReasonSuccessfulDelete = "SuccessfulDelete"
	ReasonSuccessfulUpdate = "SuccessfulUpdate"
	ReasonFailDelete       = "FailDelete"
	ReasonFailUpdate       = "FailUpdate"
)

// GarbageCollector interface with collectGarbage method
type GarbageCollector interface {
	CollectGarbage(ctx context.Context)
}
