package clean

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// HealthCleaner is responsible for cleaning up health checker resources
type HealthCleaner struct {
	Service   common.Service
	log       *logr.Logger
	nsxClient *nsx.Client
	clusterID string
}

// NewHealthCleaner creates a new HealthCleaner instance
func NewHealthCleaner(service common.Service, log *logr.Logger, nsxClient *nsx.Client, clusterID string) *HealthCleaner {
	return &HealthCleaner{
		Service:   service,
		log:       log,
		nsxClient: nsxClient,
		clusterID: clusterID,
	}
}

// CleanupHealthResources deletes the health status resource from NSX
func (h *HealthCleaner) CleanupHealthResources(_ context.Context) error {
	// Delete the health status resource from NSX
	h.log.Info("Starting health status resource cleanup", "clusterID", h.clusterID, "status", "attempting")
	if h.nsxClient != nil && h.clusterID != "" {
		url := fmt.Sprintf("api/v1/systemhealth/container-cluster/%s/ncp/status", h.clusterID)
		h.log.Info("Attempting to delete health status resource", "clusterID", h.clusterID, "url", url)
		if err := h.nsxClient.Cluster.HttpDelete(url); err != nil {
			h.log.Error(err, "Failed to delete health status resource from NSX", "clusterID", h.clusterID, "url", url, "status", "failed")
			return err
		}
		h.log.Info("Successfully deleted health status resource from NSX", "clusterID", h.clusterID, "status", "success")
	} else {
		h.log.Info("Skipping health status resource cleanup - no client or cluster ID", "hasClient", h.nsxClient != nil, "hasClusterID", h.clusterID != "")
	}
	return nil
}
