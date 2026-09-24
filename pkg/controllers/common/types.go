package common

import (
	"context"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
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
	MetricResTypeSubnetIPReservation        = "subnetipreservation"
	MetricResTypeNetworkInfo                = "networkinfo"
	MetricResTypeNamespace                  = "namespace"
	MetricResTypePod                        = "pod"
	MetricResTypeNode                       = "node"
	MetricResTypeServiceLb                  = "servicelb"
	MetricResTypeStatefulSet                = "statefulset"
	MetricResTypeGateway                    = "gateway"
	MaxConcurrentReconciles                 = 8
	NSXOperatorError                        = "nsx-op/error"
	//sync the error with NCP side
	ErrorNoDFWLicense                  = "NO_DFW_LICENSE"
	ErrorNetworkPolicyValidationFailed = "NETWORK_POLICY_VALIDATION_FAILED"
	ErrorNetworkPolicyUpdateFailed     = "NETWORK_POLICY_UPDATE_FAILED"
	ErrorNetworkPolicyUpdatePending    = "NETWORK_POLICY_UPDATE_PENDING"

	LabelK8sMasterRole  = "node-role.kubernetes.io/master"
	LabelK8sControlRole = "node-role.kubernetes.io/control-plane"

	ManagedK8sGatewayClassIstio = "istio"
	ManagedK8sGatewayClassAviLB = "avi-lb"
)

var (
	ResultNormal  = ctrl.Result{}
	ResultRequeue = ctrl.Result{Requeue: true}
	// for k8s events that need to retry in short loop, eg: namespace creation
	ResultRequeueAfter10sec = ctrl.Result{RequeueAfter: 10 * time.Second}
	ResultRequeueAfter60sec = ctrl.Result{RequeueAfter: 60 * time.Second}
	// for unstable event, eg: failed to k8s resources when reconciling, may due to k8s unstable
	ResultRequeueAfter5mins     = ctrl.Result{RequeueAfter: 5 * time.Minute}
	AnnotationNamespaceVPCError = "nsx.vmware.com/vpc_error"
)

// RequeueResultFromReconcileError returns a ctrl.Result based on whether the error is or can be converted to a RetryAfterError.
// If err is nil, it returns ResultNormal.
// If retriable, it requeues after the specified RetryAfterSeconds; otherwise it returns ResultRequeueAfter10sec.
func RequeueResultFromReconcileError(err error) ctrl.Result {
	if err == nil {
		return ResultNormal
	}
	if retryAfterErr, ok := nsxutil.ConvertToRetryAfterError(err); ok {
		return ctrl.Result{RequeueAfter: time.Duration(retryAfterErr.RetryAfterSeconds()) * time.Second}
	}
	return ResultRequeueAfter10sec
}

const (
	ReasonSuccessfulDelete = "SuccessfulDelete"
	ReasonSuccessfulUpdate = "SuccessfulUpdate"
	ReasonFailDelete       = "FailDelete"
	ReasonFailUpdate       = "FailUpdate"
)

// GarbageCollector interface with collectGarbage method
type GarbageCollector interface {
	CollectGarbage(ctx context.Context) error
}

type NameSpaceType int

const (
	SystemNs NameSpaceType = iota
	SVServiceNs
	NormalNs
)
const (
	SupervisorServiceIDLabel = "appplatform.vmware.com/serviceId"
	VsphereAppPlatformLabel  = "vSphere-AppPlatform"
)

// IsReconcileResultRequeue checks if a reconcile result indicates a requeue is needed.
// This includes both immediate requeue (deprecated result.Requeue) and delayed requeue (result.RequeueAfter > 0).
func IsReconcileResultRequeue(result ctrl.Result) bool {
	//nolint:staticcheck // SA1019: result.Requeue is deprecated but still needs to be checked for compatibility
	return result.Requeue || result.RequeueAfter > 0
}
