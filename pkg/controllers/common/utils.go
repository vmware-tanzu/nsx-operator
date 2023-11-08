package common

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	log  = logger.Log
	lock = &sync.Mutex{}
)

func AllocateSubnetFromSubnetSet(subnetSet *v1alpha1.SubnetSet) (string, error) {
	// TODO: For now, this is a global lock. In the future, we need to narrow its scope down to improve the performance.
	lock.Lock()
	defer lock.Unlock()
	subnetPath, err := ServiceMediator.GetAvailableSubnet(subnetSet)
	if err != nil {
		log.Error(err, "failed to allocate Subnet")
		return "", err
	}
	return subnetPath, nil
}

func getSharedNamespaceAndVpcForNamespace(client k8sclient.Client, ctx context.Context, namespaceName string) (string, string, error) {
	namespace := &v1.Namespace{}
	namespacedName := types.NamespacedName{Name: namespaceName}
	if err := client.Get(ctx, namespacedName, namespace); err != nil {
		log.Error(err, "failed to get target namespace during getting VPC for namespace")
		return "", "", err
	}
	vpcAnnotation, exists := namespace.Annotations[servicecommon.AnnotationVPCName]
	if !exists {
		return "", "", nil
	}
	array := strings.Split(vpcAnnotation, "/")
	if len(array) != 2 {
		err := fmt.Errorf("invalid annotation value of '%s': %s", servicecommon.AnnotationVPCName, vpcAnnotation)
		return "", "", err
	}
	sharedNamespaceName, sharedVpcName := array[0], array[1]
	log.Info("got shared VPC for namespace", "current namespace", namespaceName, "shared VPC", sharedVpcName, "shared namespace", sharedNamespaceName)
	return sharedNamespaceName, sharedVpcName, nil
}

func GetDefaultSubnetSet(client k8sclient.Client, ctx context.Context, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
	targetNamespace, _, err := getSharedNamespaceAndVpcForNamespace(client, ctx, namespace)
	if err != nil {
		return nil, err
	}
	if targetNamespace == "" {
		log.Info("namespace doesn't have shared VPC, searching the default subnetset in the current namespace", "namespace", namespace)
		targetNamespace = namespace
	}
	subnetSet, err := getDefaultSubnetSetByNamespace(client, ctx, targetNamespace, resourceType)
	if err != nil {
		return nil, err
	}
	return subnetSet, err
}

func getDefaultSubnetSetByNamespace(client k8sclient.Client, ctx context.Context, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
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

