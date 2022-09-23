package controllers

import (
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	WCP_SYSTEM_RESOURCE    = "vmware-system-shared-t1"
	METRIC_SECURITY_POLICY = "securitypolicy"
)

var (
	Log                     = logf.Log.WithName("controller")
	ResultNormal            = ctrl.Result{}
	ResultRequeue           = ctrl.Result{Requeue: true}
	ResultRequeueAfter5mins = ctrl.Result{Requeue: true, RequeueAfter: 5 * time.Minute}
)
