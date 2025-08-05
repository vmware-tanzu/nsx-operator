// Copyright © 2019-2021 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package clients

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
)

// NewVCClient creates a new VCClient instance
func NewVCClient(hostname string, port int, userName, password string) *VCClient {
	httpClient := createHTTPClient()
	baseurl := fmt.Sprintf("https://%s:%d/", hostname, port)
	vcurl, _ := url.Parse(baseurl)

	vcurl.User = url.UserPassword(userName, password)
	return &VCClient{
		url:        vcurl,
		httpClient: httpClient,
	}
}

// GetSupervisorID retrieves the supervisor ID from vCenter
func (c *VCClient) GetSupervisorID() (string, error) {
	err := c.StartSession()
	if err != nil {
		return "", err
	}
	urlPath := "/api/vcenter/namespace-management/supervisors/summaries"
	request, err := c.prepareRequest(http.MethodGet, urlPath, nil)
	if err != nil {
		return "", err
	}
	var response struct {
		Items []SupervisorSummary `json:"items"`
	}
	if _, err = c.handleRequest(request, &response); err != nil {
		return "", err
	}

	for _, sv := range response.Items {
		log.Info("Checking supervisor", "supervisor", sv.Info.Name, "status", sv.Info.ConfigStatus)
		if sv.Info.ConfigStatus == "RUNNING" {
			return sv.ID, nil
		}
	}
	return "", fmt.Errorf("no valid supervisor found on vCenter")
}

// GetStoragePolicyID retrieves the storage policy ID from vCenter
func (c *VCClient) GetStoragePolicyID() (string, string, error) {
	err := c.StartSession()
	if err != nil {
		return "", "", err
	}

	urlPath := "/api/vcenter/storage/policies"
	request, err := c.prepareRequest(http.MethodGet, urlPath, nil)
	if err != nil {
		return "", "", err
	}
	// response is a list of storage policy info
	var response []StoragePolicyInfo
	if _, err = c.handleRequest(request, &response); err != nil {
		return "", "", err
	}

	for _, po := range response {
		log.Info("Checking storage policy", "policy name", po.Name, "description", po.Description, "policy", po.Policy)
		if strings.Contains(po.Name, "global") {
			return po.Name, po.Policy, nil
		}
	}
	return "", "", fmt.Errorf("no valid storage policy found on vCenter")
}

// GetClusterVirtualMachineImage retrieves the cluster virtual machine image
func (c *VCClient) GetClusterVirtualMachineImage() (string, error) {
	// Better to use tkg.5, otherwise the image may not get ip
	kubectlCmd := "kubectl get clustervirtualmachineimage -A | grep -E 'tkg.5|photon-5' | tail -n 1 | awk '{print $1}'"
	var stdout, stderr bytes.Buffer
	command := exec.Command("bash", "-c", kubectlCmd)
	command.Stdout = &stdout
	command.Stderr = &stderr

	log.Info("Executing", "cmd", kubectlCmd)

	err := command.Run()
	_, _ = stdout.String(), stderr.String()

	if err != nil {
		log.Info("Failed to execute", "cmd error", err)
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// GetContentLibraryID retrieves the content library ID from vCenter
func (c *VCClient) GetContentLibraryID() (string, error) {
	err := c.StartSession()
	if err != nil {
		return "", err
	}

	urlPath := "/api/content/library"
	request, err := c.prepareRequest(http.MethodGet, urlPath, nil)
	if err != nil {
		return "", err
	}
	var response []string
	if _, err = c.handleRequest(request, &response); err != nil {
		return "", err
	}

	for _, cl := range response {
		log.Info("Checking content library", "content library", cl)
		if cl != "" {
			return cl, nil
		}
	}
	return "", fmt.Errorf("no valid content library found on vCenter")
}

// CreateNamespaceWithPreCreatedVPC creates a namespace with a pre-created VPC
func (c *VCClient) CreateNamespaceWithPreCreatedVPC(namespace string, vpcPath string, supervisorID string) error {
	vcNamespace := createVCNamespaceSpec(namespace, supervisorID, vpcPath)
	data, err := json.Marshal(vcNamespace)
	if err != nil {
		return fmt.Errorf("unable convert vcNamespace object to json bytes: %v", err)
	}
	request, err := c.prepareRequest(http.MethodPost, "/api/vcenter/namespaces/instances/v2", data)
	if err != nil {
		return fmt.Errorf("failed to parepare http request with vcNamespace data: %v", err)
	}
	if _, err = c.handleRequest(request, nil); err != nil {
		return err
	}
	return nil
}

// GetNamespaceInfoByName retrieves namespace information by name
func (c *VCClient) GetNamespaceInfoByName(namespace string) (*VCNamespaceGetInfo, int, error) {
	urlPath := fmt.Sprintf("/api/vcenter/namespaces/instances/v2/%s", namespace)
	request, err := c.prepareRequest(http.MethodGet, urlPath, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to prepare http request with vcNamespace get: %v", err)
	}
	result := &VCNamespaceGetInfo{}
	statusCode, err := c.handleRequest(request, result)
	if err != nil {
		return nil, statusCode, err
	}
	return result, statusCode, nil
}

// DeleteNamespace deletes a namespace
func (c *VCClient) DeleteNamespace(namespace string) error {
	urlPath := fmt.Sprintf("/api/vcenter/namespaces/instances/%s", namespace)
	request, err := c.prepareRequest(http.MethodDelete, urlPath, nil)
	if err != nil {
		return fmt.Errorf("failed to parepare http request with vcNamespace deletion: %v", err)
	}
	if _, err = c.handleRequest(request, nil); err != nil {
		return err
	}
	return nil
}

// CreateVCNamespace creates a VC namespace with the provided namespace
func (c *VCClient) CreateVCNamespace(namespace string) error {
	svID, err := c.GetSupervisorID()
	if err != nil {
		return err
	}

	_, storagePolicyID, err := c.GetStoragePolicyID()
	if err != nil {
		return err
	}
	log.V(1).Info("Get storage policy", "storagePolicyID", storagePolicyID)

	contentLibraryID, err := c.GetContentLibraryID()
	if err != nil {
		return err
	}
	log.V(1).Info("Get content library", "contentLibraryID", contentLibraryID)

	vcNamespace := &VCNamespaceCreateSpec{
		Supervisor: svID,
		Namespace:  namespace,
		StorageSpecs: []InstancesStorageSpec{
			{
				Policy: storagePolicyID,
			},
		},
		ContentLibraries: []InstancesContentLibrarySpec{
			{
				ContentLibrary: contentLibraryID,
			},
		},
		NetworkSpec: InstancesNetworkConfigInfo{
			NetworkProvider: "NSX_VPC",
			VpcNetwork: InstancesVpcNetworkInfo{
				DefaultSubnetSize: 16,
			},
		},
		VmServiceSpec: &InstancesVMServiceSpec{
			ContentLibraries: []string{contentLibraryID},
			VmClasses:        []string{"best-effort-xsmall"},
		},
	}

	err = c.StartSession()
	if err != nil {
		return err
	}
	defer c.CloseSession()

	dataJson, err := json.Marshal(vcNamespace)
	log.V(1).Info("Data json", "dataJson", string(dataJson))
	if err != nil {
		log.Error(err, "Unable convert vcNamespace object to json bytes", "namespace", namespace)
		return fmt.Errorf("unable convert vcNamespace object to json bytes: %v", err)
	}

	request, err := c.prepareRequest(http.MethodPost, "/api/vcenter/namespaces/instances/v2", dataJson)
	if err != nil {
		log.Error(err, "Failed to prepare http request with vcNamespace data", "namespace", namespace)
		return fmt.Errorf("failed to parepare http request with vcNamespace data: %v", err)
	}

	if _, err = c.handleRequest(request, nil); err != nil {
		log.Error(err, "Failed to create VC namespace", "namespace", namespace)
		return err
	}

	return nil
}

// createVCNamespaceSpec creates a VCNamespaceCreateSpec
func createVCNamespaceSpec(namespace string, svID string, vpcPath string) *VCNamespaceCreateSpec {
	return &VCNamespaceCreateSpec{
		Supervisor: svID,
		Namespace:  namespace,
		NetworkSpec: InstancesNetworkConfigInfo{
			NetworkProvider: "NSX_VPC",
			VpcNetwork: InstancesVpcNetworkInfo{
				Vpc:               vpcPath,
				DefaultSubnetSize: 16,
			},
		},
	}
}
