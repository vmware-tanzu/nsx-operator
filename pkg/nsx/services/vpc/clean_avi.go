package vpc

import (
	"context"
	"errors"

	mapset "github.com/deckarep/golang-set"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

type (
	mapInterface = map[string]interface{}
)

const (
	PolicyAPI                = "policy/api/v1"
	AviSubnetPortsPathSuffix = "/subnets/_AVI_SUBNET--LB/ports/"
)

func httpGetAviPortsPaths(cluster *nsx.Cluster, vpcPath string) (mapset.Set, error) {
	aviSubnetPortsPath := vpcPath + AviSubnetPortsPathSuffix
	url := PolicyAPI + aviSubnetPortsPath

	resp, err := cluster.HttpGet(url)
	if err != nil {
		return nil, err
	}
	aviPathSet := mapset.NewSet()
	for _, item := range resp["results"].([]interface{}) {
		aviPathSet.Add(item.(mapInterface)["path"].(string))
	}
	return aviPathSet, nil
}

func CleanAviSubnetPorts(ctx context.Context, cluster *nsx.Cluster, vpcPath string) error {
	log.Info("Deleting Avi subnetports started")

	allPaths, err := httpGetAviPortsPaths(cluster, vpcPath)
	/*
	 in the e2e test, this GET operation return 400 instead of 404.
	 "error": "StatusCode is 400,ErrorCode is 500012,Detail is The path=[/orgs/default/projects/nsx_operator_e2e_test/vpcs/kube-system-c996c9c6-50df-429c-8202-2287c0822791/subnets/_AVI_SUBNET--LB] is invalid
	 so add checking HttpBadRequest.
	*/
	if err != nil {
		if errors.Is(err, nsxutil.HttpNotFoundError) || errors.Is(err, nsxutil.HttpBadRequest) {
			log.Info("No Avi subnetports found")
			return nil
		}
		return err
	}

	log.Info("Deleting Avi subnetport", "paths", allPaths)
	for _, path := range allPaths.ToSlice() {
		url := PolicyAPI + path.(string)
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			if err := cluster.HttpDelete(url); err != nil {
				return err
			}
		}
	}
	return nil
}
