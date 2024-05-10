package common

import (
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	MetricResTypeSecurityPolicy    = "securitypolicy"
	MetricResTypeNetworkPolicy     = "networkpolicy"
	MetricResTypeIPPool            = "ippool"
	MetricResTypeNSXServiceAccount = "nsxserviceaccount"
	MetricResTypeSubnetPort        = "subnetport"
	MetricResTypeStaticRoute       = "staticroute"
	MetricResTypeSubnet            = "subnet"
	MetricResTypeSubnetSet         = "subnetset"
	MetricResTypeNetworkInfo       = "networkinfo"
	MetricResTypeNamespace         = "namespace"
	MetricResTypePod               = "pod"
	MetricResTypeNode              = "node"
	MetricResTypeServiceLb         = "servicelb"
	MaxConcurrentReconciles        = 8

	LabelK8sMasterRole  = "node-role.kubernetes.io/master"
	LabelK8sControlRole = "node-role.kubernetes.io/control-plane"
)

var (
	ResultNormal  = ctrl.Result{}
	ResultRequeue = ctrl.Result{Requeue: true}
	// for k8s events that need to retry in short loop, eg: namespace creation
	ResultRequeueAfter10sec = ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second}
	// for unstable event, eg: failed to k8s resources when reconciling, may due to k8s unstable
	ResultRequeueAfter5mins = ctrl.Result{Requeue: true, RequeueAfter: 5 * time.Minute}
)

const (
	ReasonSuccessfulDelete = "SuccessfulDelete"
	ReasonSuccessfulUpdate = "SuccessfulUpdate"
	ReasonFailDelete       = "FailDelete"
	ReasonFailUpdate       = "FailUpdate"
)
