package storage

import (
	"context"
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// ServiceEndpointStatisticsStorage implements REST operations for ServiceEndpointStatistics.
type ServiceEndpointStatisticsStorage struct {
	nsxClient  *nsx.Client
	vpcService eas.VPCInfoProvider
}

// NewServiceEndpointStatisticsStorage creates a new storage instance.
func NewServiceEndpointStatisticsStorage(nsxClient *nsx.Client, vpcService eas.VPCInfoProvider) *ServiceEndpointStatisticsStorage {
	return &ServiceEndpointStatisticsStorage{
		nsxClient:  nsxClient,
		vpcService: vpcService,
	}
}

// Get retrieves ServiceEndpointStatistics for the given ServiceEndpoint CR name within the namespace.
func (s *ServiceEndpointStatisticsStorage) Get(ctx context.Context, namespace, name string) (*easv1alpha1.ServiceEndpointStatistics, error) {
	log := logger.Log

	// 1. Get VPC path using VPCInfoProvider
	vpcEntries := s.vpcService.ListVPCInfo(namespace)
	if len(vpcEntries) == 0 {
		return nil, HandleEASError(k8serrors.NewNotFound(schema.GroupResource{Group: easv1alpha1.GroupVersion.Group, Resource: "serviceendpointstatistics"}, name), "serviceendpointstatistics", name, nil)
	}

	info := vpcEntries[0].Info

	log.Debug("Fetching VPCServiceEndpoint statistics from NSX",
		"namespace", namespace, "name", name,
		"orgID", info.OrgID, "projectID", info.ProjectID, "vpcID", info.VPCID)

	// 2. List VPC service endpoints and find the one matching the CR name tag
	vpcServiceEndpoints, err := s.nsxClient.VPCServiceEndpointClient.List(info.OrgID, info.ProjectID, info.VPCID, nil, nil, nil, nil, nil, nil)
	if err != nil {
		return nil, HandleEASError(err, "serviceendpointstatistics", name, fmt.Errorf("failed to list VPC service endpoints from NSX: %w", err))
	}

	var nsxVPCServiceEndpointID string
	for _, ep := range vpcServiceEndpoints.Results {
		if ep.Id == nil {
			continue
		}
		// Check tags
		for _, t := range ep.Tags {
			if t.Scope != nil && *t.Scope == common.TagScopeVPCServiceEndpointCRName && t.Tag != nil && *t.Tag == name {
				nsxVPCServiceEndpointID = *ep.Id
				break
			}
		}
		if nsxVPCServiceEndpointID != "" {
			break
		}
	}

	if nsxVPCServiceEndpointID == "" {
		return nil, HandleEASError(k8serrors.NewNotFound(schema.GroupResource{Group: easv1alpha1.GroupVersion.Group, Resource: "serviceendpointstatistics"}, name), "serviceendpointstatistics", name, nil)
	}

	// 3. Get Statistics from NSX using ServiceEndpointStatisticsClient
	nsxStats, err := s.nsxClient.VPCServiceEndpointStatisticsClient.Get(info.OrgID, info.ProjectID, info.VPCID, nsxVPCServiceEndpointID)
	if err != nil {
		return nil, HandleEASError(err, "serviceendpointstatistics", name, fmt.Errorf("failed to get VPCServiceEndpoint statistics from NSX: %w", err))
	}

	return s.convertStatistics(&nsxStats, name, namespace), nil
}

func (s *ServiceEndpointStatisticsStorage) convertStatistics(nsxStats *model.VpcServiceEndpointStatistics, name, namespace string) *easv1alpha1.ServiceEndpointStatistics {
	stats := &easv1alpha1.ServiceEndpointStatistics{
		TypeMeta: metav1.TypeMeta{
			APIVersion: easv1alpha1.GroupVersion.String(),
			Kind:       "ServiceEndpointStatistics",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	if nsxStats == nil {
		return stats
	}

	if nsxStats.Rx != nil {
		stats.RX = easv1alpha1.EndpointStatistics{
			DroppedPackets: DerefInt64(nsxStats.Rx.DroppedPackets),
			TotalBytes:     DerefInt64(nsxStats.Rx.TotalBytes),
			TotalPackets:   DerefInt64(nsxStats.Rx.TotalPackets),
		}
	}
	if nsxStats.Tx != nil {
		stats.TX = easv1alpha1.EndpointStatistics{
			DroppedPackets: DerefInt64(nsxStats.Tx.DroppedPackets),
			TotalBytes:     DerefInt64(nsxStats.Tx.TotalBytes),
			TotalPackets:   DerefInt64(nsxStats.Tx.TotalPackets),
		}
	}

	return stats
}
