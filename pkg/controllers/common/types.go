package common

import (
	"time"
	
	ctrl "sigs.k8s.io/controller-runtime"
	
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/mediator"
)

const (
	MetricResTypeSecurityPolicy = "securitypolicy"
)

var (
	ResultNormal            = ctrl.Result{}
	ResultRequeue           = ctrl.Result{Requeue: true}
	ResultRequeueAfter5mins = ctrl.Result{Requeue: true, RequeueAfter: 5 * time.Minute}
	
	ServiceMediator = mediator.ServiceMediator{}
)
