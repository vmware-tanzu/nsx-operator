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
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log  = &logger.Log
	lock = &sync.Mutex{}
)

func AllocateSubnetFromSubnetSet(subnetSet *v1alpha1.SubnetSet, vpcService servicecommon.VPCServiceProvider, subnetService servicecommon.SubnetServiceProvider, subnetPortService servicecommon.SubnetPortServiceProvider) (string, error) {
	// TODO: For now, this is a global lock. In the future, we need to narrow its scope down to improve the performance.
	lock.Lock()
	defer lock.Unlock()
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
	log.Info("the existing subnets are not available, creating new subnet", "subnetList", subnetList, "subnetSet.Name", subnetSet.Name, "subnetSet.Namespace", subnetSet.Namespace)
	vpcInfoList := vpcService.ListVPCInfo(subnetSet.Namespace)
	if len(vpcInfoList) == 0 {
		err := errors.New("no VPC found")
		log.Error(err, "failed to allocate Subnet")
		return "", err
	}
	return subnetService.CreateOrUpdateSubnet(subnetSet, vpcInfoList[0], tags)
}

func getSharedNamespaceForNamespace(client k8sclient.Client, ctx context.Context, namespaceName string) (string, error) {
	namespace := &v1.Namespace{}
	namespacedName := types.NamespacedName{Name: namespaceName}
	if err := client.Get(ctx, namespacedName, namespace); err != nil {
		log.Error(err, "failed to get target namespace during getting VPC for namespace")
		return "", err
	}
	sharedNamespaceName, exists := namespace.Annotations[servicecommon.AnnotationSharedVPCNamespace]
	if !exists {
		return "", nil
	}
	log.Info("got shared VPC namespace", "current namespace", namespaceName, "shared namespace", sharedNamespaceName)
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
