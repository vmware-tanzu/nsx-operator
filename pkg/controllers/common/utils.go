package common

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
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
)

var (
	log            = logger.Log
	SubnetSetLocks sync.Map
)

func AllocateSubnetFromSubnetSet(subnetSet *v1alpha1.SubnetSet, vpcService servicecommon.VPCServiceProvider, subnetService servicecommon.SubnetServiceProvider, subnetPortService servicecommon.SubnetPortServiceProvider) (string, error) {
	// Use SubnetSet uuid lock to make sure when multiple ports are created on the same SubnetSet, only one Subnet will be created
	subnetSetLock := LockSubnetSet(subnetSet.GetUID())
	defer UnlockSubnetSet(subnetSet.GetUID(), subnetSetLock)
	subnetList := subnetService.GetSubnetsByIndex(servicecommon.TagScopeSubnetSetCRUID, string(subnetSet.GetUID()))
	for _, nsxSubnet := range subnetList {
		canAllocate, err := subnetPortService.AllocatePortFromSubnet(nsxSubnet)
		if err != nil {
			return "", err
		}
		if canAllocate {
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
		log.Warn("No VPC found for SubnetSet, will retry later", "Namespace", subnetSet.Namespace)
		return "", errors.New("no VPC found, will retry later")
	}
	nsxSubnet, err := subnetService.CreateOrUpdateSubnet(subnetSet, vpcInfoList[0], tags)
	if err != nil {
		return "", err
	}
	canAllocate, err := subnetPortService.AllocatePortFromSubnet(nsxSubnet)
	if err != nil {
		return "", err
	}
	if canAllocate {
		return *nsxSubnet.Path, nil
	}
	return "", fmt.Errorf("cannot allocate Port from SubnetSet %s", subnetSet.Name)
}

func GetDefaultSubnetSetByNamespace(client k8sclient.Client, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
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
		log.Error(err, "Failed to list default SubnetSet CR", "Namespace", namespace)
		return nil, err
	}
	if len(subnetSetList.Items) == 0 {
		return nil, errors.New("default SubnetSet not found")
	} else if len(subnetSetList.Items) > 1 {
		return nil, errors.New("multiple default SubnetSets found")
	}
	subnetSet := subnetSetList.Items[0]
	log.Info("Got default SubnetSet", "subnetset.Name", subnetSet.Name, "subnetset.uid", subnetSet.UID)
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

func GenericGarbageCollector(cancel chan bool, timeout time.Duration, f func(ctx context.Context) error) {
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

func LockSubnetSet(uuid types.UID) *sync.Mutex {
	lock := sync.Mutex{}
	subnetSetLock, _ := SubnetSetLocks.LoadOrStore(uuid, &lock)
	log.Trace("Lock SubnetSet", "uuid", uuid)
	subnetSetLock.(*sync.Mutex).Lock()
	return subnetSetLock.(*sync.Mutex)
}

func UnlockSubnetSet(uuid types.UID, subnetSetLock *sync.Mutex) {
	if subnetSetLock != nil {
		log.Trace("Unlock SubnetSet", "uuid", uuid)
		subnetSetLock.Unlock()
	}
}

func UpdateReconfigureNicAnnotation(client k8sclient.Client, ctx context.Context, obj k8sclient.Object, value string) error {
	key := types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}
	err := client.Get(ctx, key, obj)
	if err != nil {
		log.Error(err, "Failed to get Object", "key", key)
		return err
	}
	anno := obj.GetAnnotations()
	if anno == nil {
		anno = map[string]string{}
	}
	restoreValue, ok := anno[servicecommon.AnnotationReconfigureNic]
	if ok {
		// Append the value to annotation if it is an interface name
		if restoreValue == "" || value == "true" {
			restoreValue = value
		} else if !strings.Contains(restoreValue, value) {
			restoreValue += fmt.Sprintf(", %s", value)
		}
	} else {
		restoreValue = value
	}
	anno[servicecommon.AnnotationReconfigureNic] = restoreValue
	obj.SetAnnotations(anno)
	return client.Update(ctx, obj)
}

func GetSubnetByIP(subnets []*model.VpcSubnet, ip net.IP) (string, error) {
	for _, subnet := range subnets {
		for _, cidr := range subnet.IpAddresses {
			_, ipnet, err := net.ParseCIDR(cidr)
			if err != nil {
				return "", err
			}
			if ipnet.Contains(ip) && subnet.Path != nil {
				return *subnet.Path, nil
			}
		}
	}
	return "", fmt.Errorf("failed to find Subnet matching IP %s", ip)
}
