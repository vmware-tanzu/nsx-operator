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

// VPCEndpointStatisticsStorage implements REST operations for VPCEndpointStatistics.
type VPCEndpointStatisticsStorage struct {
	nsxClient  *nsx.Client
	vpcService eas.VPCInfoProvider
}

// NewVPCEndpointStatisticsStorage creates a new storage instance.
func NewVPCEndpointStatisticsStorage(nsxClient *nsx.Client, vpcService eas.VPCInfoProvider) *VPCEndpointStatisticsStorage {
	return &VPCEndpointStatisticsStorage{
		nsxClient:  nsxClient,
		vpcService: vpcService,
	}
}

// Get retrieves VPCEndpointStatistics for the given VPCEndpoint CR name within the namespace.
func (s *VPCEndpointStatisticsStorage) Get(ctx context.Context, namespace, name string) (*easv1alpha1.VPCEndpointStatistics, error) {
	log := logger.Log

	// 1. Get VPC path using VPCInfoProvider
	vpcEntries := s.vpcService.ListVPCInfo(namespace)
	if len(vpcEntries) == 0 {
		return nil, HandleEASError(k8serrors.NewNotFound(schema.GroupResource{Group: easv1alpha1.GroupVersion.Group, Resource: "vpcendpointstatistics"}, name), "vpcendpointstatistics", name, nil)
	}

	info := vpcEntries[0].Info

	log.Debug("Fetching VPCEndpoint statistics from NSX",
		"namespace", namespace, "name", name,
		"orgID", info.OrgID, "projectID", info.ProjectID, "vpcID", info.VPCID)

	// 2. List VPC endpoints and find the one matching the CR name tag
	vpcEndpoints, err := s.nsxClient.VPCEndpointClient.List(info.OrgID, info.ProjectID, info.VPCID, nil, nil, nil, nil, nil, nil)
	if err != nil {
		return nil, HandleEASError(err, "vpcendpointstatistics", name, fmt.Errorf("failed to list VPC endpoints from NSX: %w", err))
	}

	var nsxVPCEndpointID string
	for _, ep := range vpcEndpoints.Results {
		if ep.Id == nil {
			continue
		}
		// Check tags
		for _, t := range ep.Tags {
			if t.Scope != nil && *t.Scope == common.TagScopeVPCEndpointCRName && t.Tag != nil && *t.Tag == name {
				nsxVPCEndpointID = *ep.Id
				break
			}
		}
		if nsxVPCEndpointID != "" {
			break
		}
	}

	if nsxVPCEndpointID == "" {
		return nil, HandleEASError(k8serrors.NewNotFound(schema.GroupResource{Group: easv1alpha1.GroupVersion.Group, Resource: "vpcendpointstatistics"}, name), "vpcendpointstatistics", name, nil)
	}

	// 3. Get Statistics from NSX using VPCEndpointStatisticsClient
	nsxStats, err := s.nsxClient.VPCEndpointStatisticsClient.Get(info.OrgID, info.ProjectID, info.VPCID, nsxVPCEndpointID)
	if err != nil {
		return nil, HandleEASError(err, "vpcendpointstatistics", name, fmt.Errorf("failed to get VPCEndpoint statistics from NSX: %w", err))
	}

	return s.convertStatistics(&nsxStats, name, namespace), nil
}

func (s *VPCEndpointStatisticsStorage) convertStatistics(nsxStats *model.VpcEndpointStatistics, name, namespace string) *easv1alpha1.VPCEndpointStatistics {
	stats := &easv1alpha1.VPCEndpointStatistics{
		TypeMeta: metav1.TypeMeta{
			APIVersion: easv1alpha1.GroupVersion.String(),
			Kind:       "VPCEndpointStatistics",
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
