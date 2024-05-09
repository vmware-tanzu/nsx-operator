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
	if err != nil {
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
