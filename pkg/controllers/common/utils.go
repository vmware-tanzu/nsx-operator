package common

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log            = &logger.Log
	SubnetSetLocks sync.Map
)

func AllocateSubnetFromSubnetSet(subnetSet *v1alpha1.SubnetSet, vpcService servicecommon.VPCServiceProvider, subnetService servicecommon.SubnetServiceProvider, subnetPortService servicecommon.SubnetPortServiceProvider) (string, error) {
	// Use SubnetSet uuid lock to make sure when multiple ports are created on the same SubnetSet, only one Subnet will be created
	lockSubnetSet(subnetSet.GetUID())
	defer unlockSubnetSet(subnetSet.GetUID())
	subnetList := subnetService.GetSubnetsByIndex(servicecommon.TagScopeSubnetSetCRUID, string(subnetSet.GetUID()))
	for _, nsxSubnet := range subnetList {
		portNums := len(subnetPortService.GetPortsOfSubnet(*nsxSubnet.Id))
		totalIP := int(*nsxSubnet.Ipv4SubnetSize)
		if len(nsxSubnet.IpAddresses) > 0 {
			// totalIP will be overrided if IpAddresses are specified.
			totalIP, _ = util.CalculateIPFromCIDRs(nsxSubnet.IpAddresses)
		}
		// NSX reserves 4 ip addresses in each subnet for network address, gateway address,
		// dhcp server address and broadcast address.
		if portNums < totalIP-4 {
			return *nsxSubnet.Path, nil
		}
	}
	tags := subnetService.GenerateSubnetNSTags(subnetSet)
	if tags == nil {
		return "", errors.New("failed to generate subnet tags")
	}
	log.Info("The existing subnets are not available, creating new subnet", "subnetList", subnetList, "subnetSet.Name", subnetSet.Name, "subnetSet.Namespace", subnetSet.Namespace)
	vpcInfoList := vpcService.ListVPCInfo(subnetSet.Namespace)
	if len(vpcInfoList) == 0 {
		err := errors.New("no VPC found")
		log.Error(err, "Failed to allocate Subnet")
		return "", err
	}
	return subnetService.CreateOrUpdateSubnet(subnetSet, vpcInfoList[0], tags)
}

func getSharedNamespaceForNamespace(client k8sclient.Client, ctx context.Context, namespaceName string) (string, error) {
	namespace := &v1.Namespace{}
	namespacedName := types.NamespacedName{Name: namespaceName}
	if err := client.Get(ctx, namespacedName, namespace); err != nil {
		log.Error(err, "Failed to get target namespace during getting VPC for namespace")
		return "", err
	}
	sharedNamespaceName, exists := namespace.Annotations[servicecommon.AnnotationSharedVPCNamespace]
	if !exists {
		return "", nil
	}
	log.Info("Got shared VPC namespace", "current namespace", namespaceName, "shared namespace", sharedNamespaceName)
	return sharedNamespaceName, nil
}

func GetDefaultSubnetSet(client k8sclient.Client, ctx context.Context, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
	targetNamespace, err := getSharedNamespaceForNamespace(client, ctx, namespace)
	if err != nil {
		return nil, err
	}
	if targetNamespace == "" {
		log.Info("namespace doesn't have shared VPC, searching the default subnetset in the current namespace", "namespace", namespace)
		targetNamespace = namespace
	}
	subnetSet, err := getDefaultSubnetSetByNamespace(client, targetNamespace, resourceType)
	if err != nil {
		return nil, err
	}
	return subnetSet, err
}

func getDefaultSubnetSetByNamespace(client k8sclient.Client, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
	subnetSetList := &v1alpha1.SubnetSetList{}
	subnetSetSelector := &metav1.LabelSelector{
		MatchLabels: map[string]string{
			servicecommon.LabelDefaultSubnetSet: resourceType,
		},
	}
	labelSelector, _ := metav1.LabelSelectorAsSelector(subnetSetSelector)
	opts := &k8sclient.ListOptions{
		LabelSelector: labelSelector,
		Namespace:     namespace,
	}
	if err := client.List(context.Background(), subnetSetList, opts); err != nil {
		log.Error(err, "failed to list default subnetset CR", "namespace", namespace)
		return nil, err
	}
	if len(subnetSetList.Items) == 0 {
		return nil, errors.New("default subnetset not found")
	} else if len(subnetSetList.Items) > 1 {
		return nil, errors.New("multiple default subnetsets found")
	}
	subnetSet := subnetSetList.Items[0]
	log.Info("got default subnetset", "subnetset.Name", subnetSet.Name, "subnetset.uid", subnetSet.UID)
	return &subnetSet, nil

}

func NodeIsMaster(node *v1.Node) bool {
	for k := range node.Labels {
		if k == LabelK8sMasterRole || k == LabelK8sControlRole {
			return true
		}
	}
	return false
}

func GetVirtualMachineNameForSubnetPort(subnetPort *v1alpha1.SubnetPort) (string, string, error) {
	annotations := subnetPort.GetAnnotations()
	if annotations == nil {
		return "", "", nil
	}
	attachmentRef, exist := annotations[servicecommon.AnnotationAttachmentRef]
	if !exist {
		return "", "", nil
	}
	array := strings.Split(attachmentRef, "/")
	if len(array) != 3 || !strings.EqualFold(array[0], servicecommon.ResourceTypeVirtualMachine) {
		err := fmt.Errorf("invalid annotation value of '%s': %s", servicecommon.AnnotationAttachmentRef, attachmentRef)
		return "", "", err
	}
	return array[1], array[2], nil
}

// NumReconcile now uses the fix number of concurrency
func NumReconcile() int {
	return MaxConcurrentReconciles
}

func GenericGarbageCollector(cancel chan bool, timeout time.Duration, f func(ctx context.Context)) {
	ctx := context.Background()
	ticker := time.NewTicker(timeout)
	defer ticker.Stop()

	for {
		select {
		case <-cancel:
			return
		case <-ticker.C:
			f(ctx)
		}
	}
}

type UpdateSuccessStatusFn func(k8sclient.Client, context.Context, k8sclient.Object, metav1.Time, ...interface{})

type UpdateFailStatusFn func(k8sclient.Client, context.Context, k8sclient.Object, metav1.Time, error, ...interface{})

type StatusUpdater struct {
	Client          k8sclient.Client
	NSXConfig       *config.NSXOperatorConfig
	Recorder        record.EventRecorder
	MetricResType   string
	NSXResourceType string
	ResourceType    string
}

func (u *StatusUpdater) UpdateSuccess(ctx context.Context, obj k8sclient.Object, setStatusFn UpdateSuccessStatusFn, args ...interface{}) {
	log.Info(fmt.Sprintf("Successfully created or updated %s CR", u.ResourceType), u.ResourceType, obj)
	if setStatusFn != nil {
		setStatusFn(u.Client, ctx, obj, metav1.Now(), args...)
	}
	u.Recorder.Event(obj, v1.EventTypeNormal, ReasonSuccessfulUpdate, fmt.Sprintf("%s CR has been successfully updated", u.ResourceType))
	metrics.CounterInc(u.NSXConfig, metrics.ControllerUpdateSuccessTotal, u.MetricResType)
}

func (u *StatusUpdater) UpdateFail(ctx context.Context, obj k8sclient.Object, err error, msg string, setStatusFn UpdateFailStatusFn, args ...interface{}) {
	log.Error(err, fmt.Sprintf("Failed to create or update %s CR", u.ResourceType), "Reason", msg, u.ResourceType, obj)
	if setStatusFn != nil {
		setStatusFn(u.Client, ctx, obj, metav1.Now(), err, args...)
	}
	u.Recorder.Event(obj, v1.EventTypeWarning, ReasonFailUpdate, fmt.Sprintf("%v", err))
	metrics.CounterInc(u.NSXConfig, metrics.ControllerUpdateFailTotal, u.MetricResType)
}

func (u *StatusUpdater) DeleteSuccess(namespacedName types.NamespacedName, obj k8sclient.Object) {
	log.Info(fmt.Sprintf("Successfully deleted %s CR", u.ResourceType), u.ResourceType, namespacedName)
	if obj != nil {
		u.Recorder.Event(obj, v1.EventTypeNormal, ReasonSuccessfulDelete, fmt.Sprintf("%s CR has been successfully deleted", u.ResourceType))
	}
	metrics.CounterInc(u.NSXConfig, metrics.ControllerDeleteSuccessTotal, u.MetricResType)
}

func (u *StatusUpdater) DeleteFail(namespacedName types.NamespacedName, obj k8sclient.Object, err error) {
	log.Error(err, fmt.Sprintf("Failed to delete NSX %s, would retry exponentially", u.NSXResourceType), u.ResourceType, namespacedName)
	if obj != nil {
		u.Recorder.Event(obj, v1.EventTypeWarning, ReasonFailDelete, fmt.Sprintf("%v", err))
	}
	metrics.CounterInc(u.NSXConfig, metrics.ControllerDeleteFailTotal, u.MetricResType)
}

func (u *StatusUpdater) IncreaseSyncTotal() {
	metrics.CounterInc(u.NSXConfig, metrics.ControllerSyncTotal, u.MetricResType)
}

func (u *StatusUpdater) IncreaseUpdateTotal() {
	metrics.CounterInc(u.NSXConfig, metrics.ControllerUpdateTotal, u.MetricResType)
}

func (u *StatusUpdater) IncreaseDeleteTotal() {
	metrics.CounterInc(u.NSXConfig, metrics.ControllerDeleteTotal, u.MetricResType)
}

func (u *StatusUpdater) IncreaseDeleteSuccessTotal() {
	metrics.CounterInc(u.NSXConfig, metrics.ControllerDeleteSuccessTotal, u.MetricResType)
}

func (u *StatusUpdater) IncreaseDeleteFailTotal() {
	metrics.CounterInc(u.NSXConfig, metrics.ControllerDeleteFailTotal, u.MetricResType)
}

func NewStatusUpdater(client k8sclient.Client, nsxConfig *config.NSXOperatorConfig, recorder record.EventRecorder, metricResType string, nsxResourceType string, resourceType string) StatusUpdater {
	return StatusUpdater{
		Client:          client,
		NSXConfig:       nsxConfig,
		Recorder:        recorder,
		MetricResType:   metricResType,
		NSXResourceType: nsxResourceType,
		ResourceType:    resourceType,
	}
}

func lockSubnetSet(uuid types.UID) {
	lock := sync.Mutex{}
	subnetSetLock, _ := SubnetSetLocks.LoadOrStore(uuid, &lock)
	log.V(1).Info("Lock SubnetSet", "uuid", uuid)
	subnetSetLock.(*sync.Mutex).Lock()
}

func unlockSubnetSet(uuid types.UID) {
	if subnetSetLock, existed := SubnetSetLocks.Load(uuid); existed {
		log.V(1).Info("Unlock SubnetSet", "uuid", uuid)
		subnetSetLock.(*sync.Mutex).Unlock()
	}
}
