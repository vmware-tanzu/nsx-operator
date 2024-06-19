package clean

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
	"strings"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"

	"github.com/go-logr/logr"
)

type (
	mapInterface = map[string]interface{}
)

const TagDLB = "DLB"

func appendIfNotExist(slice []string, s string) []string {
	for _, item := range slice {
		if item == s {
			return slice
		}
	}
	return append(slice, s)
}

func httpQueryDLBResources(cluster *nsx.Cluster, cf *config.NSXOperatorConfig, resource string) ([]string, error) {
	queryParam := "resource_type:" + resource +
		"&tags.scope:ncp\\/cluster" +
		"&tags.tag:" + cf.Cluster +
		"&tags.scope:ncp\\/created_for" +
		"&tags.tag:" + TagDLB

	pairs := strings.Split(queryParam, "&")
	params := make(map[string]string)
	for _, pair := range pairs {
		kv := strings.Split(pair, ":")
		if len(kv) == 2 {
			params[kv[0]] = kv[1]
		}
	}
	var encodedPairs []string
	for key, value := range params {
		encodedKey := neturl.QueryEscape(key)
		encodedValue := neturl.QueryEscape(value)
		encodedPairs = append(encodedPairs, fmt.Sprintf("%s:%s", encodedKey, encodedValue))
	}

	encodedQuery := strings.Join(encodedPairs, "%20AND%20")
	url := "policy/api/v1/search/query?query=" + encodedQuery

	resp, err := cluster.HttpGet(url)
	if err != nil {
		return nil, err
	}
	var resourcePath []string
	for _, item := range resp["results"].([]interface{}) {
		resourcePath = appendIfNotExist(resourcePath, item.(mapInterface)["path"].(string))
	}
	return resourcePath, nil
}

func CleanDLB(ctx context.Context, cluster *nsx.Cluster, cf *config.NSXOperatorConfig, log *logr.Logger) error {
	log.Info("Deleting DLB resources started")

	resources := []string{"Group", "LBVirtualServer", "LBService", "LBPool", "LBCookiePersistenceProfile"}
	var allPaths []string

	for _, resource := range resources {
		paths, err := httpQueryDLBResources(cluster, cf, resource)
		if err != nil {
			return err
		}
		log.Info(resource, "count", len(paths))
		allPaths = append(allPaths, paths...)
	}

	log.Info("Deleting DLB resources", "paths", allPaths)
	for _, path := range allPaths {
		url := "policy/api/v1" + path
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
