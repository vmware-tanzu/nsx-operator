package common

import (
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	MetricResTypeSecurityPolicy = "securitypolicy"
)

var (
	ResultNormal            = ctrl.Result{}
	ResultRequeue           = ctrl.Result{Requeue: true}
	ResultRequeueAfter5mins = ctrl.Result{Requeue: true, RequeueAfter: 5 * time.Minute}
)
