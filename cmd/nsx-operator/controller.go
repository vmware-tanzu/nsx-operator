/* Copyright © 2021 VMware, Inc. All Rights Reserved.

   SPDX-License-Identifier: Apache-2.0 */

package main

import (
	"context"
	"fmt"
	ctx "github.com/nsx-operator/pkg/context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	componentbaseconfig "k8s.io/component-base/config"
	//"fmt"
	"time"

	//uuid "github.com/satori/go.uuid"
	"k8s.io/client-go/informers"
	//componentbaseconfig "k8s.io/component-base/config"

	//"github.com/nsx-operator/pkg/controller"
	"github.com/nsx-operator/pkg/k8s"
	"github.com/nsx-operator/pkg/log"
	//"github.com/nsx-operator/pkg/nsx"
	//"github.com/nsx-operator/pkg/nsx/store"
	"github.com/nsx-operator/pkg/signals"
	"github.com/nsx-operator/pkg/util"
	//thirdpartystore "github.com/nsx-operator/third_party/store"
)

const (
	// The default resync period for handlers. Use the same default value as kube-controller-manager:
	// https://github.com/kubernetes/kubernetes/blob/release-1.17/pkg/controller/apis/config/v1alpha1/defaults.go#L120
	informerDefaultResync = 12 * time.Hour
)

// starts NSX Operator controller
func run(c *util.NSXOperatorConfig) error {
	// Create cluster context with cluster info and NCP config
	clusterUUID := string(c.CoeConfig.Cluster)
	ctx := ctx.ClusterContext{
		Config:        c,
		ClusterName: c.CoeConfig.Cluster,
		ClusterID:   clusterUUID,
	}
	logger := log.WithClusterContext(ctx)
	logger.Info("Initialize NSX Operator Controller")

	// Initialize K8s native client and K8s CRD client

	clientConnection := componentbaseconfig.ClientConnectionConfiguration{Kubeconfig: c.K8sConfig.KubeConfigFile}
	// TODO initialize client connection
	kubeclient, crdClient, apiExtensionClient, err := k8s.CreateClients(clientConnection, "")
	print(crdClient, apiExtensionClient)
	options := metav1.ListOptions{
		LabelSelector: "app=<APPNAME>",
	}
	podList, _ := kubeclient.CoreV1().Pods("nsx-system").List(context.TODO(), options)


	// List() returns a pointer to slice, derefernce it, before iterating
	for _, podInfo := range (*podList).Items {
		fmt.Printf("pods-name=%v\n", podInfo.Name)
		fmt.Printf("pods-status=%v\n", podInfo.Status.Phase)
		fmt.Printf("pods-condition=%v\n", podInfo.Status.Conditions)
	}
	if err != nil {
		// Enhance log and error reporting
		return fmt.Errorf("failed to create clientset: %v", err)
	}

	// Initialize informers
	informerFactory := informers.NewSharedInformerFactory(kubeclient, informerDefaultResync)
	// TODO: uncomment the informers when implement the individual controller
	//podInformer := informerFactory.Core().V1().Pods()
	//namespaceInformer := informerFactory.Core().V1().Namespaces()
	//serviceInformer := informerFactory.Core().V1().Services()
	//endpointInformer := informerFactory.Core().V1().Endpoints()
	//nodeInformer := informerFactory.Core().V1().Nodes()

	// TODO Add feature gate for CRD
	//crdInformerFactory := crdinformers.NewSharedInformerFactory(crdClient, informerDefaultResync)
	//vnetInformer := crdInformerFactory.Vmware().V1alpha1().VirtualNetworks()

	// TODO Declare NSX stores

	// TODO Declare NSX services


	// Initialized WCP specific store

	stopCh := signals.RegisterSignalHandlers()

	informerFactory.Start(stopCh)
	// TODO uncomment when crd is in used
	// crdInformerFactory.Start(stopCh)

	// TODO Start controller


	<-stopCh
	// TODO add error handling to terminate_log
	logger.Info("NSX Operator exit!")
	return nil
}