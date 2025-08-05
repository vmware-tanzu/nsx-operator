// Copyright © 2019-2021 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package framework

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

// CollectClusterInfo collects information about the Kubernetes cluster
func CollectClusterInfo() error {
	serverVersion, err := Data.ClientSet.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("error when detecting K8s server version %v", err)
	}
	ClusterInfoData.K8sServerVersion = serverVersion.String()

	// retrieve Node information
	nodes, err := Data.ClientSet.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("error when listing cluster Nodes: %v", err)
	}
	Log.Info("Found Nodes in the cluster", "nodes count", len(nodes.Items))
	workerIdx := 1
	ClusterInfoData.Nodes = make(map[int]ClusterNode)
	for _, node := range nodes.Items {
		isMaster := func() bool {
			_, ok := node.Labels["node-role.kubernetes.io/control-plane"]
			if !ok {
				// openshift has label node-role.kubernetes.io/master, but not node-role.kubernetes.io/control-plane
				_, ok = node.Labels["node-role.kubernetes.io/master"]
			}
			return ok
		}()

		var nodeIdx int
		// If multiple master Nodes (HA), we will select the last one in the list
		if isMaster {
			nodeIdx = 0
			ClusterInfoData.MasterNodeName = node.Name
		} else {
			nodeIdx = workerIdx
			workerIdx++
		}

		ClusterInfoData.Nodes[nodeIdx] = ClusterNode{
			Idx:  nodeIdx,
			Name: node.Name,
			UID:  string(node.UID),
		}
	}
	if ClusterInfoData.MasterNodeName == "" {
		return fmt.Errorf("error when listing cluster Nodes: master Node not found")
	}
	ClusterInfoData.NumNodes = workerIdx
	ClusterInfoData.NumWorkerNodes = ClusterInfoData.NumNodes - 1

	retrieveCIDRs := func(cmd string, reg string) ([]string, error) {
		res := make([]string, 2)
		rc, stdout, _, err := RunCommandOnNode(ClusterInfoData.MasterNodeName, cmd)
		if err != nil || rc != 0 {
			return res, fmt.Errorf("error when running the following command `%s` on master Node: %v, %s", cmd, err, stdout)
		}
		re := regexp.MustCompile(reg)
		if matches := re.FindStringSubmatch(stdout); len(matches) == 0 {
			return res, fmt.Errorf("cannot retrieve CIDR, unexpected kubectl output: %s", stdout)
		} else {
			cidrs := strings.Split(matches[1], ",")
			if len(cidrs) == 1 {
				_, cidr, err := net.ParseCIDR(cidrs[0])
				if err != nil {
					return res, fmt.Errorf("CIDR cannot be parsed: %s", cidrs[0])
				}
				if cidr.IP.To4() != nil {
					res[0] = cidrs[0]
				} else {
					res[1] = cidrs[0]
				}
			} else if len(cidrs) == 2 {
				_, cidr, err := net.ParseCIDR(cidrs[0])
				if err != nil {
					return res, fmt.Errorf("CIDR cannot be parsed: %s", cidrs[0])
				}
				if cidr.IP.To4() != nil {
					res[0] = cidrs[0]
					res[1] = cidrs[1]
				} else {
					res[0] = cidrs[1]
					res[1] = cidrs[0]
				}
			} else {
				return res, fmt.Errorf("unexpected cluster CIDR: %s", matches[1])
			}
		}
		return res, nil
	}

	// retrieve cluster CIDRs
	podCIDRs, err := retrieveCIDRs("kubectl cluster-info dump | grep cluster-cidr", `cluster-cidr=([^"]+)`)
	if err != nil {
		Log.Info("Failed to detect IPv4 or IPv6 Pod CIDR. Ignore.")
	} else {
		ClusterInfoData.PodV4NetworkCIDR = podCIDRs[0]
		ClusterInfoData.PodV6NetworkCIDR = podCIDRs[1]
	}

	return nil
}

// QueryResource is used to query resource by tags, not handling pagination
// tags should be present in pairs, the first tag is the scope, the second tag is the value
// caller should transform the response to the expected resource type
func (data *TestData) QueryResource(resourceType string, tags []string) (model.SearchResponse, error) {
	resourceParam := fmt.Sprintf("%s:%s", common.ResourceType, resourceType)
	queryParam := resourceParam
	if len(tags) >= 2 {
		tagscope := strings.Replace(tags[0], "/", "\\/", -1)
		tagtag := strings.Replace(tags[1], ":", "\\:", -1)
		tagParam := fmt.Sprintf("tags.scope:%s AND tags.tag:%s", tagscope, tagtag)
		queryParam = resourceParam + " AND " + tagParam
	}
	queryParam += " AND marked_for_delete:false"
	var cursor *string
	var pageSize int64 = 500
	response, err := data.NSXClient.QueryClient.List(queryParam, cursor, nil, &pageSize, nil, nil)
	if err != nil {
		Log.Info("Error when querying resource ", "resourceType", resourceType, "error", err)
		return model.SearchResponse{}, err
	}
	return response, nil
}

// WaitForResourceExist waits for a resource to exist or not exist
func (data *TestData) WaitForResourceExist(namespace string, resourceType string, key string, value string, shouldExist bool) error {
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, DefaultTimeout, false, func(ctx context.Context) (bool, error) {
		exist := true
		resourceParam := fmt.Sprintf("%s:%s AND %s:*%s*", common.ResourceType, resourceType, key, value)
		queryParam := resourceParam

		// Only add the tag query if namespace is not empty and not for inventory resources
		if namespace != "" && !strings.HasPrefix(resourceType, "Container") {
			tagScopeClusterKey := strings.Replace(common.TagScopeNamespace, "/", "\\/", -1)
			tagScopeClusterValue := strings.Replace(namespace, ":", "\\:", -1)
			tagParam := fmt.Sprintf("tags.scope:%s AND tags.tag:%s", tagScopeClusterKey, tagScopeClusterValue)
			queryParam = resourceParam + " AND " + tagParam
			queryParam += " AND marked_for_delete:false"
		}

		var cursor *string
		var pageSize int64 = 500
		response, err := Data.NSXClient.QueryClient.List(queryParam, cursor, nil, &pageSize, nil, nil)
		if err != nil {
			Log.Info("Error when querying resource ", "resourceType", resourceType, "key", key, "value", value, "error", err)
			return false, err
		}
		if len(response.Results) == 0 {
			exist = false
		}
		Log.V(2).Info("", "QueryParam", queryParam, "exist", exist)
		if exist != shouldExist {
			return false, nil
		}
		return true, nil
	})
	return err
}

// WaitForResourceExistOrNot waits for a resource to exist or not exist by name
func (data *TestData) WaitForResourceExistOrNot(namespace string, resourceType string, resourceName string, shouldExist bool) error {
	return data.WaitForResourceExist(namespace, resourceType, "display_name", resourceName, shouldExist)
}

// WaitForResourceExistByPath waits for a resource to exist or not exist by path
func (data *TestData) WaitForResourceExistByPath(pathPolicy string, shouldExist bool) error {
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, DefaultTimeout, false, func(ctx context.Context) (bool, error) {
		exist := true

		fullURL := PolicyAPI + pathPolicy
		fullURL = strings.ReplaceAll(fullURL, "\"", "")
		fullURL = strings.ReplaceAll(fullURL, "\n", "")
		fullURL = strings.ReplaceAll(fullURL, "\r", "")
		_, err := url.Parse(fullURL)
		if err != nil {
			fmt.Println("Invalid URL:", err)
			return false, err
		}

		resp, err := Data.NSXClient.Client.Cluster.HttpGet(fullURL)
		if err != nil {
			if !shouldExist {
				return true, nil
			}
			if err == util.HttpNotFoundError && shouldExist {
				return false, nil
			}
			return false, err
		}
		id, ok := resp["id"].(string)
		if !ok || id == "" {
			exist = false
		}
		if exist != shouldExist {
			return false, nil
		}
		return true, nil
	})
	return err
}

// ApplyYAML applies a YAML file to the cluster
func ApplyYAML(filename string, ns string) error {
	cmd := fmt.Sprintf("kubectl apply -f %s -n %s", filename, ns)
	if ns == "" {
		cmd = fmt.Sprintf("kubectl apply -f %s", filename)
	}
	var stdout, stderr bytes.Buffer
	command := exec.Command("bash", "-c", cmd)
	command.Stdout = &stdout
	command.Stderr = &stderr

	Log.Info("Executing", "cmd", cmd)

	err := command.Run()
	_, errorString := stdout.String(), stderr.String()

	if err != nil {
		Log.Info("Failed to execute", "cmd error", err, "detail error", errorString)
		return fmt.Errorf("failed to apply YAML: %w", err)
	}
	return nil
}

// DeleteYAML deletes a YAML file from the cluster
func DeleteYAML(filename string, ns string) error {
	cmd := fmt.Sprintf("kubectl delete -f %s -n %s", filename, ns)
	if ns == "" {
		cmd = fmt.Sprintf("kubectl delete -f %s", filename)
	}
	var stdout, stderr bytes.Buffer
	command := exec.Command("bash", "-c", cmd)
	Log.Info("Executing", "cmd", cmd)
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err != nil {
		// Ignore error info
		// very short watch: k8s.io/client-go/tools/watch/informerwatcher.
		// go:146: Unexpected watch close - watch lasted less than a second and no items received
		// log.Error(err, "Error when deleting YAML file")
		return nil
	}
	_, _ = string(stdout.Bytes()), string(stderr.Bytes())
	return nil
}
