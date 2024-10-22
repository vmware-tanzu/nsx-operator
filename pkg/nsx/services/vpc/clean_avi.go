package vpc

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

type (
	mapInterface = map[string]interface{}
)

const (
	PolicyAPI                = "policy/api/v1"
	AviSubnetPortsPathSuffix = "/subnets/%s/ports/"
)

var aviSubnetPortsPathSuffix = fmt.Sprintf(AviSubnetPortsPathSuffix, common.AVISubnetLBID)

func httpGetAviPortsPaths(cluster *nsx.Cluster, vpcPath string) (sets.Set[string], error) {
	aviSubnetPortsPath := vpcPath + aviSubnetPortsPathSuffix
	url := PolicyAPI + aviSubnetPortsPath

	resp, err := cluster.HttpGet(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Avi Subnet ports paths: %w", err)
	}

	aviPathSet := sets.New[string]()
	results, ok := resp["results"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected type for results: %T", resp["results"])
	}

	for _, item := range results {
		itemMap, ok := item.(mapInterface)
		if !ok {
			continue
		}
		path, ok := itemMap["path"].(string)
		if ok {
			aviPathSet.Insert(path)
		}
	}
	return aviPathSet, nil
}

func CleanAviSubnetPorts(ctx context.Context, cluster *nsx.Cluster, vpcPath string) error {
	log.Info("Deleting Avi Subnet ports started", "vpcPath", vpcPath)

	allPaths, err := httpGetAviPortsPaths(cluster, vpcPath)
	/*
	 in the e2e test, this GET operation return 400 instead of 404.
	 "error": "StatusCode is 400,ErrorCode is 500012,Detail is The path=[/orgs/default/projects/nsx_operator_e2e_test/vpcs/kube-system-c996c9c6-50df-429c-8202-2287c0822791/subnets/_AVI_SUBNET--LB] is invalid
	 so add checking HttpBadRequest.
	*/
	if err != nil {
		if errors.Is(err, nsxutil.HttpNotFoundError) || errors.Is(err, nsxutil.HttpBadRequest) {
			log.Info("No Avi Subnet ports found", "vpcPath", vpcPath)
			return nil
		}
		return fmt.Errorf("error getting Avi Subnet ports: %w", err)
	}

	log.Info("Deleting Avi Subnet port", "paths", allPaths)
	for path := range allPaths {
		url := PolicyAPI + path
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			if err := cluster.HttpDelete(url); err != nil {
				return fmt.Errorf("failed to delete Avi Subnet port at %s: %w", url, err)
			}
		}
	}
	return nil
}
