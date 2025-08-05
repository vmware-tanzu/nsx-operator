// Copyright © 2019-2021 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package framework

import (
	"fmt"

	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/vmware-tanzu/nsx-operator/pkg/client/clientset/versioned"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/test/e2e/clients"
)

// Data is the global test data instance
var Data *TestData

// NewTestData creates a new TestData instance
func NewTestData(nsxConfig string, vcUser string, vcPassword string) error {
	Data = &TestData{}
	err := Data.createClients()
	if err != nil {
		return err
	}
	config.UpdateConfigFilePath(nsxConfig)
	cf, err := config.NewNSXOperatorConfigFromFile()
	if err != nil {
		return err
	}
	err = Data.createNSXClients(cf)
	if err != nil {
		return err
	}
	if vcUser != "" && vcPassword != "" {
		Data.VCClient = clients.NewVCClient(cf.VCEndPoint, cf.HttpsPort, vcUser, vcPassword)
	}
	return nil
}

// createNSXClients creates the NSX clients
func (data *TestData) createNSXClients(cf *config.NSXOperatorConfig) error {
	nsxClient, err := clients.NewNSXClient(cf)
	if err != nil {
		return err
	}
	data.NSXClient = nsxClient
	return nil
}

// createClients initializes the clientSets in the TestData structure
func (data *TestData) createClients() error {
	kubeconfigPath, err := Provider.GetKubeconfigPath()
	if err != nil {
		return fmt.Errorf("error when getting Kubeconfig path: %v", err)
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.ExplicitPath = kubeconfigPath
	configOverrides := &clientcmd.ConfigOverrides{}

	kubeConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides).ClientConfig()
	if err != nil {
		return fmt.Errorf("error when building kube config: %v", err)
	}
	clientSet, err := clientset.NewForConfig(kubeConfig)
	if err != nil {
		return fmt.Errorf("error when creating kubernetes client: %v", err)
	}
	crdClientset, err := versioned.NewForConfig(kubeConfig)
	if err != nil {
		return fmt.Errorf("error when creating nsx-operator CRD client: %v", err)
	}
	data.KubeConfig = kubeConfig
	data.ClientSet = clientSet
	data.CRDClientset = crdClientset
	return nil
}
