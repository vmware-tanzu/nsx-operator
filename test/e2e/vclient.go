package e2e

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

type vcClient struct {
	url          *url.URL
	httpClient   *http.Client
	sessionMutex sync.Mutex
	sessionKey   string
}

type supervisorInfo struct {
	Name         string `json:"name"`
	ConfigStatus string `json:"config_status"`
	K8sStatus    string `json:"kubernetes_status"`
}

type supervisorSummary struct {
	ID   string         `json:"supervisor"`
	Info supervisorInfo `json:"info"`
}

type InstancesIpv4Cidr struct {
	Address string `json:"address"`
	Prefix  int64  `json:"prefix"`
}

type InstancesVpcConfig struct {
	PrivateCidrs []InstancesIpv4Cidr `json:"private_cidrs"`
}

type InstancesVpcNetworkInfo struct {
	VpcConfig         InstancesVpcConfig `json:"vpc_config,omitempty"`
	Vpc               string             `json:"vpc,omitempty"`
	DefaultSubnetSize int64              `json:"default_subnet_size"`
}

type InstancesNetworkConfigInfo struct {
	NetworkProvider string                  `json:"network_provider"`
	VpcNetwork      InstancesVpcNetworkInfo `json:"vpc_network"`
}

type VCNamespaceCreateSpec struct {
	Supervisor  string                     `json:"supervisor"`
	Namespace   string                     `json:"namespace"`
	NetworkSpec InstancesNetworkConfigInfo `json:"network_spec"`
}

type VCNamespaceGetInfo struct {
	Supervisor  string                     `json:"supervisor"`
	NetworkSpec InstancesNetworkConfigInfo `json:"network_spec"`
}

var (
	sessionURLPath = "/api/session"
)

func newVcClient(hostname string, port int, userName, password string) *vcClient {
	httpClient := createHttpClient()
	baseurl := fmt.Sprintf("https://%s:%d/", hostname, port)
	vcurl, _ := url.Parse(baseurl)

	vcurl.User = url.UserPassword(userName, password)
	return &vcClient{
		url:        vcurl,
		httpClient: httpClient,
	}
}

func createHttpClient() *http.Client {
	tlsConfig := &tls.Config{InsecureSkipVerify: true} // #nosec G402: ignore insecure options
	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}
	return &http.Client{Transport: transport, Timeout: time.Minute}
}

func (c *vcClient) startSession() error {
	c.sessionMutex.Lock()
	defer c.sessionMutex.Unlock()
	if c.sessionKey == "" {
		url := fmt.Sprintf("%s://%s%s", c.url.Scheme, c.url.Host, sessionURLPath)
		request, err := http.NewRequest(http.MethodPost, url, nil)
		if err != nil {
			return err
		}
		username := c.url.User.Username()
		password, _ := c.url.User.Password()
		request.SetBasicAuth(username, password)

		var sessionData string
		if _, err = c.handleRequest(request, &sessionData); err != nil {
			return err
		}

		c.sessionKey = sessionData
	}
	return nil
}

func (c *vcClient) closeSession() error {
	c.sessionMutex.Lock()
	defer c.sessionMutex.Unlock()
	if c.sessionKey == "" {
		return nil
	}
	request, err := c.prepareRequest(http.MethodDelete, sessionURLPath, nil)
	if err != nil {
		return err
	}

	if _, err = c.handleRequest(request, nil); err != nil {
		return err
	}

	c.sessionKey = ""
	return nil
}

func (c *vcClient) getSupervisorID() (string, error) {
	urlPath := "/api/vcenter/namespace-management/supervisors/summaries"
	request, err := c.prepareRequest(http.MethodGet, urlPath, nil)
	if err != nil {
		return "", err
	}
	var response struct {
		Items []supervisorSummary `json:"items"`
	}
	if _, err = c.handleRequest(request, &response); err != nil {
		return "", err
	}

	for _, sv := range response.Items {
		if sv.Info.ConfigStatus == "RUNNING" {
			return sv.ID, nil
		}
	}
	return "", fmt.Errorf("no valid supervisor found on vCenter")
}

func (c *vcClient) createNamespaceWithPreCreatedVPC(namespace string, vpcPath string, supervisorID string) error {
	urlPath := "/api/vcenter/namespaces/instances/v2"
	vcNamespace := createVCNamespaceSpec(namespace, supervisorID, vpcPath)
	data, err := json.Marshal(vcNamespace)
	if err != nil {
		return fmt.Errorf("unable convert vcNamespace object to json bytes: %v", err)
	}
	request, err := c.prepareRequest(http.MethodPost, urlPath, data)
	if err != nil {
		return fmt.Errorf("failed to parepare http request with vcNamespace data: %v", err)
	}
	if _, err = c.handleRequest(request, nil); err != nil {
		return err
	}
	return nil
}

func (c *vcClient) getNamespaceInfoByName(namespace string) (*VCNamespaceGetInfo, int, error) {
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

func (c *vcClient) deleteNamespace(namespace string) error {
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

func (c *vcClient) prepareRequest(method string, urlPath string, data []byte) (*http.Request, error) {
	url := fmt.Sprintf("%s://%s%s", c.url.Scheme, c.url.Host, urlPath)
	req, err := http.NewRequest(method, url, bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("vmware-api-session-id", c.sessionKey)
	return req, nil
}

func (c *vcClient) handleRequest(request *http.Request, responseData interface{}) (int, error) {
	response, err := c.httpClient.Do(request)
	if err != nil {
		log.Error(err, "Failed to do HTTP request")
		return 0, err
	}
	return handleHTTPResponse(response, responseData)
}

func handleHTTPResponse(response *http.Response, result interface{}) (int, error) {
	statusCode := response.StatusCode
	if statusCode == http.StatusNoContent {
		return statusCode, nil
	}

	if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
		if result == nil {
			return statusCode, nil
		}
		body, err := io.ReadAll(response.Body)
		defer response.Body.Close()

		if err != nil {
			return statusCode, err
		}
		if err = json.Unmarshal(body, result); err != nil {
			return statusCode, err
		}
		return statusCode, nil
	}

	var err error
	if statusCode == http.StatusNotFound {
		err = util.HttpNotFoundError
	} else if statusCode == http.StatusBadRequest {
		err = util.HttpBadRequest
	} else {
		err = fmt.Errorf("HTTP response with errorcode %d", response.StatusCode)
	}
	return statusCode, err
}
