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
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log                     = logger.Log
	SubnetSetLocks          sync.Map
	NetworkSubnetSetNameMap = map[string]string{
		servicecommon.DefaultPodNetwork: servicecommon.LabelDefaultPodSubnetSet,
		servicecommon.DefaultVMNetwork:  servicecommon.LabelDefaultVMSubnetSet,
	}
)

func IsSharedSubnetPath(ctx context.Context, client k8sclient.Client, path string, ns string) (bool, error) {
	associatedResource, err := servicecommon.ConvertSubnetPathToAssociatedResource(path)
	if err != nil {
		return false, err
	}
	subnetList := &v1alpha1.SubnetList{}
	err = client.List(ctx, subnetList, k8sclient.InNamespace(ns), k8sclient.MatchingFields{util.SubnetAssociatedResource: associatedResource})
	if err != nil {
		log.Error(err, "Failed to list Subnet CR", "indexValue", associatedResource)
		return false, err
	}
	if len(subnetList.Items) == 0 {
		log.Debug("Subnet is not a shared Subnet", "path", path)
		return false, nil
	}
	log.Debug("Subnet is a shared Subnet", "path", path)
	return true, nil
}

// For auto-created SubnetSet, get NSX Subnets by SubnetSet CR ID
// For SubnetSet with Subnet specified, get NSX Subnets based on the SubnetCR
func GetNSXSubnetsForSubnetSet(client k8sclient.Client, subnetSet *v1alpha1.SubnetSet, subnetService servicecommon.SubnetServiceProvider) ([]*model.VpcSubnet, error) {
	var subnets []*model.VpcSubnet
	if subnetSet.Spec.SubnetNames == nil {
		return subnetService.GetSubnetsByIndex(servicecommon.TagScopeSubnetSetCRUID, string(subnetSet.UID)), nil
	}
	for _, subnetName := range *subnetSet.Spec.SubnetNames {
		subnetCR := &v1alpha1.Subnet{}
		namespacedName := types.NamespacedName{
			Name:      subnetName,
			Namespace: subnetSet.Namespace,
		}
		if err := client.Get(context.TODO(), namespacedName, subnetCR); err != nil {
			log.Error(err, "Failed to get Subnet CR", "SubnetCR", namespacedName)
			return subnets, err
		}
		nsxSubnet, err := subnetService.GetSubnetByCR(subnetCR)
		if err != nil {
			log.Error(err, "Failed to get NSX Subnet under SubnetSet", "Namespace", subnetCR.Namespace, "SubnetSet", subnetSet.Name, "Subnet", subnetCR.Name)
			return subnets, err
		}
		subnets = append(subnets, nsxSubnet)
	}
	return subnets, nil
}

func GetSubnetFromSubnetSet(client k8sclient.Client, subnetSet *v1alpha1.SubnetSet, subnetService servicecommon.SubnetServiceProvider, subnetPortService servicecommon.SubnetPortServiceProvider) (string, error) {
	var errList []error
	for _, subnetName := range *subnetSet.Spec.SubnetNames {
		subnetCR := &v1alpha1.Subnet{}
		if err := client.Get(context.Background(), types.NamespacedName{Namespace: subnetSet.Namespace, Name: subnetName}, subnetCR); err != nil {
			log.Error(err, "Failed to get Subnet for SubnetSet", "Subnet", subnetName, "SubnetSet", subnetSet.Name, "Namespace", subnetSet.Namespace)
			errList = append(errList, err)
			continue
		}
		nsxSubnet, err := subnetService.GetSubnetByCR(subnetCR)
		if err != nil {
			log.Error(err, "Failed to get NSX Subnet for SubnetSet", "Subnet", subnetName, "SubnetSet", subnetSet.Name, "Namespace", subnetSet.Namespace)
			errList = append(errList, err)
			continue
		}
		canAllocate, err := subnetPortService.AllocatePortFromSubnet(nsxSubnet)
		if err != nil {
			log.Error(err, "Failed to check capacity of NSX Subnet", "Subnet", subnetName, "SubnetSet", subnetSet.Name, "Namespace", subnetSet.Namespace, "NSXSubnet", nsxSubnet.Id)
			errList = append(errList, err)
			continue
		}
		if canAllocate {
			return *nsxSubnet.Path, nil
		}
	}
	if len(errList) > 0 {
		return "", errors.Join(errList...)
	}
	return "", fmt.Errorf("all Subnets for SubnetSet %s/%s are not available", subnetSet.Namespace, subnetSet.Name)
}

// IsNamespaceInTepLessMode checks if the namespace has a VLAN-backed VPC (tepless mode).
func IsNamespaceInTepLessMode(client k8sclient.Client, namespace string) (bool, error) {
	log.Debug("Checking if namespace is in tepless mode", "namespace", namespace)
	networkInfoList := &v1alpha1.NetworkInfoList{}
	err := client.List(context.TODO(), networkInfoList, &k8sclient.ListOptions{Namespace: namespace})
	if err != nil {
		log.Error(err, "Failed to list NetworkInfo for tepless check", "namespace", namespace)
		return false, fmt.Errorf("failed to list NetworkInfo: %v", err)
	}
	if len(networkInfoList.Items) == 0 {
		err := fmt.Errorf("no NetworkInfo found in namespace %s", namespace)
		log.Error(err, "NetworkInfo not ready")
		return false, err
	}
	networkInfo := &networkInfoList.Items[0]
	if len(networkInfo.VPCs) == 0 {
		err := fmt.Errorf("no VPC found in NetworkInfo for namespace %s", namespace)
		log.Error(err, "NetworkInfo not ready")
		return false, err
	}
	if networkInfo.VPCs[0].NetworkStack == "" {
		err := errors.New("NetworkStack is not set in NetworkInfo CRD")
		log.Error(err, "NetworkInfo not ready")
		return false, err
	}
	return networkInfo.VPCs[0].NetworkStack == v1alpha1.VLANBackedVPC, nil
}

func AllocateSubnetFromSubnetSet(client k8sclient.Client, subnetSet *v1alpha1.SubnetSet, vpcService servicecommon.VPCServiceProvider, subnetService servicecommon.SubnetServiceProvider, subnetPortService servicecommon.SubnetPortServiceProvider) (string, *types.UID, *sync.RWMutex, error) {
	if subnetSet.Spec.SubnetNames != nil {
		// Use Read lock to allow SubnetPorts created parallelly on the pre-created SubnetSet
		// and block the SubnetPort creation when the SubnetSet is updated
		subnetSetLock := RLockSubnetSet(subnetSet.UID)
		nsxSubnet, err := GetSubnetFromSubnetSet(client, subnetSet, subnetService, subnetPortService)
		return nsxSubnet, &subnetSet.UID, subnetSetLock, err
	}
	// Use SubnetSet uuid lock to make sure when multiple ports are created on the same SubnetSet, only one Subnet will be created
	subnetSetLock := WLockSubnetSet(subnetSet.GetUID())
	defer WUnlockSubnetSet(subnetSet.GetUID(), subnetSetLock)
	subnetList := subnetService.GetSubnetsByIndex(servicecommon.TagScopeSubnetSetCRUID, string(subnetSet.GetUID()))
	for _, nsxSubnet := range subnetList {
		canAllocate, err := subnetPortService.AllocatePortFromSubnet(nsxSubnet)
		if err != nil {
			return "", nil, nil, err
		}
		if canAllocate {
			return *nsxSubnet.Path, nil, nil, nil
		}
	}
	tags := subnetService.GenerateSubnetNSTags(subnetSet)
	if tags == nil {
		return "", nil, nil, errors.New("failed to generate subnet tags")
	}
	log.Info("The existing subnets are not available, creating new subnet", "subnetList", subnetList, "subnetSet.Name", subnetSet.Name, "subnetSet.Namespace", subnetSet.Namespace)
	vpcInfoList := vpcService.ListVPCInfo(subnetSet.Namespace)
	if len(vpcInfoList) == 0 {
		err := errors.New("no VPC found")
		log.Error(err, "Failed to allocate Subnet")
		return "", nil, nil, err
	}
	nsxSubnet, err := subnetService.CreateOrUpdateSubnet(subnetSet, vpcInfoList[0], tags)
	if err != nil {
		return "", nil, nil, err
	}
	canAllocate, err := subnetPortService.AllocatePortFromSubnet(nsxSubnet)
	if err != nil {
		return "", nil, nil, err
	}
	if canAllocate {
		return *nsxSubnet.Path, nil, nil, nil
	}
	return "", nil, nil, fmt.Errorf("cannot allocate Port from SubnetSet %s", subnetSet.Name)
}

func GetDefaultSubnetSetByNamespace(client k8sclient.Client, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
	subnetSetList := &v1alpha1.SubnetSetList{}
	subnetSetSelector := &metav1.LabelSelector{
		MatchLabels: map[string]string{
			servicecommon.LabelDefaultNetwork: resourceType,
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

func WLockSubnetSet(uuid types.UID) *sync.RWMutex {
	lock := sync.RWMutex{}
	subnetSetLock, _ := SubnetSetLocks.LoadOrStore(uuid, &lock)
	log.Trace("Write lock SubnetSet", "uuid", uuid)
	subnetSetLock.(*sync.RWMutex).Lock()
	return subnetSetLock.(*sync.RWMutex)
}

func RLockSubnetSet(uuid types.UID) *sync.RWMutex {
	lock := sync.RWMutex{}
	subnetSetLock, _ := SubnetSetLocks.LoadOrStore(uuid, &lock)
	log.Trace("Read lock SubnetSet", "uuid", uuid)
	subnetSetLock.(*sync.RWMutex).RLock()
	return subnetSetLock.(*sync.RWMutex)
}

func WUnlockSubnetSet(uuid types.UID, subnetSetLock *sync.RWMutex) {
	if subnetSetLock != nil {
		log.Trace("Write unlock SubnetSet", "uuid", uuid)
		subnetSetLock.Unlock()
	}
}

func RUnlockSubnetSet(uuid types.UID, subnetSetLock *sync.RWMutex) {
	if subnetSetLock != nil {
		subnetSetLock.RUnlock()
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
	if err = client.Update(ctx, obj); err != nil {
		return err
	}
	log.Info("Update workload restore annotation", "kind", obj.GetObjectKind(), "key", key, "annotation", restoreValue)
	return nil
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

func listSubnetSet(ctx context.Context, client k8sclient.Client, ns string, label k8sclient.MatchingLabels) (*v1alpha1.SubnetSet, error) {
	var oldObj *v1alpha1.SubnetSet
	list := &v1alpha1.SubnetSetList{}
	if err := client.List(ctx, list, label, k8sclient.InNamespace(ns)); err != nil {
		return nil, err
	}
	if len(list.Items) > 0 {
		log.Info("Default SubnetSet already exists", "label", label, "Namespace", ns)
		oldObj = &list.Items[0]
	}
	return oldObj, nil
}

func ListDefaultSubnetSet(ctx context.Context, client k8sclient.Client, ns string, subnetSetType string) (*v1alpha1.SubnetSet, error) {
	label := k8sclient.MatchingLabels{
		servicecommon.LabelDefaultNetwork: subnetSetType,
	}
	oldObj, err := listSubnetSet(ctx, client, ns, label)
	if err != nil {
		return nil, err
	}
	// check the old formatted default subnetset
	if oldObj == nil {
		newType, _ := NetworkSubnetSetNameMap[subnetSetType]
		label = k8sclient.MatchingLabels{
			servicecommon.LabelDefaultSubnetSet: newType,
		}
		return listSubnetSet(ctx, client, ns, label)
	}
	return oldObj, nil
}

// GetNamespaceType determines the type of the namespace based on the VPCNetworkConfiguration
func GetNamespaceType(ns *v1.Namespace, vnc *v1alpha1.VPCNetworkConfiguration) NameSpaceType {
	anno := ns.Annotations
	if len(anno) > 0 {
		if ncName, exist := anno[servicecommon.AnnotationVPCNetworkConfig]; exist {
			if ncName == "system" {
				return SystemNs
			}
		}
	}
	label := ns.Labels
	if len(label) > 0 {
		if _, exist := label[SupervisorServiceIDLabel]; exist {
			if value, exist := label["managedBy"]; exist && value == VsphereAppPlatformLabel {
				return SVServiceNs
			}
		}
	}
	return NormalNs
}

func CheckNetworkStack(k8sClient k8sclient.Client, ctx context.Context, ns string, resourceType string) error {
	tepless, err := IsTepLessMode(k8sClient, ctx, ns)
	if err != nil {
		return err
	}
	if tepless {
		return fmt.Errorf("%s is not supported in VLANBackedVPC VPC", resourceType)
	}
	return nil
}

func CheckAccessModeOrVisibility(client k8sclient.Client, ctx context.Context, ns string, accessMode string, resourceType string) error {
	tepLess, err := IsTepLessMode(client, ctx, ns)
	if err != nil {
		return err
	}
	log.Trace("CheckAccessModeOrVisibility", "accessMode", accessMode, "resourceType", resourceType, "namespace", ns)
	if resourceType == servicecommon.ResourceTypeIPAddressAllocation {
		if tepLess && (accessMode == string(v1alpha1.IPAddressVisibilityPrivate) || accessMode == string(v1alpha1.IPAddressVisibilityPrivateTGW)) {
			return fmt.Errorf("IPAddressVisibility other than External is not supported for VLANBackedVPC")
		}
	} else {
		if tepLess && (accessMode == string(v1alpha1.AccessModePrivate) || accessMode == string(v1alpha1.AccessModeProject)) {
			return fmt.Errorf("AccessMode other than Public/L2Only is not supported for VLANBackedVPC")
		}

	}
	return nil
}

func GetVpcNetworkConfig(service servicecommon.VPCServiceProvider, ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
	vpcNetworkConfig, err := service.GetVPCNetworkConfigByNamespace(ns)
	if err != nil {
		log.Error(err, "Failed to get VPCNetworkConfig", "Namespace", ns)
		return nil, err
	}
	if vpcNetworkConfig == nil {
		err := fmt.Errorf("VPCNetworkConfig not found")
		log.Error(err, "VPCNetworkConfig is nil", "Namespace", ns)
		return nil, nil
	}
	return vpcNetworkConfig, nil
}

func GetDefaultAccessMode(service servicecommon.VPCServiceProvider, ns string) (v1alpha1.AccessMode, *v1alpha1.VPCNetworkConfiguration, error) {
	vpcNetworkConfig, err := GetVpcNetworkConfig(service, ns)
	if err != nil {
		return v1alpha1.AccessMode(""), nil, err
	}
	if vpcNetworkConfig == nil {
		return v1alpha1.AccessMode(""), nil, nil
	}
	networkStack, err := service.GetNetworkStackFromNC(vpcNetworkConfig)
	if err != nil {
		log.Error(err, "Failed to get NetworkStack", "Namespace", ns)
		return v1alpha1.AccessMode(""), nil, err
	}
	if networkStack == v1alpha1.FullStackVPC {
		return v1alpha1.AccessMode(v1alpha1.AccessModePrivate), vpcNetworkConfig, nil
	} else {
		return v1alpha1.AccessMode(v1alpha1.AccessModePublic), vpcNetworkConfig, nil
	}
}

// ErrFailedToListNetworkInfo indicates the controller failed to list NetworkInfo resources
var ErrFailedToListNetworkInfo = errors.New("failed to list NetworkInfo in namespace")

func IsTepLessMode(k8sClient k8sclient.Client, ctx context.Context, ns string) (bool, error) {
	networkInfoList := &v1alpha1.NetworkInfoList{}
	err := k8sClient.List(ctx, networkInfoList, k8sclient.InNamespace(ns))
	if err != nil {
		return false, fmt.Errorf("%w %s: %v", ErrFailedToListNetworkInfo, ns, err)
	}
	// if no networkinfo found or no vpc realized, ignore it and let nsx validate it
	if len(networkInfoList.Items) == 0 || len(networkInfoList.Items[0].VPCs) == 0 {
		return false, nil
	}
	for _, vpc := range networkInfoList.Items[0].VPCs {
		if vpc.NetworkStack == v1alpha1.VLANBackedVPC {
			log.Debug("Check network stack", "networkstack", vpc.NetworkStack)
			return true, nil
		}
	}
	return false, nil
}
