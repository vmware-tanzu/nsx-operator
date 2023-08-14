package common

import (
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/mediator"
)

const (
	MetricResTypeSecurityPolicy    = "securitypolicy"
	MetricResTypeIPPool            = "ippool"
	MetricResTypeNSXServiceAccount = "nsxserviceaccount"
	MetricResTypeSubnetPort        = "subnetport"
	MetricResTypeStaticRoute       = "staticroute"
	MetricResTypeSubnet            = "subnet"
	MetricResTypeSubnetSet         = "subnetset"
	MetricResTypeVPC               = "vpc"
	MetricResTypeNamespace         = "namespace"
)

var (
	ResultNormal  = ctrl.Result{}
	ResultRequeue = ctrl.Result{Requeue: true}
	// for k8s events that need to retry in short loop, eg: namespace creation
	ResultRequeueAfter10sec = ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second}
	// for unstable event, eg: failed to k8s resources when reconciling, may due to k8s unstable
	ResultRequeueAfter5mins = ctrl.Result{Requeue: true, RequeueAfter: 5 * time.Minute}

	ServiceMediator = mediator.ServiceMediator{}
)
