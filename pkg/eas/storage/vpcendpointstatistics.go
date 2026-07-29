package storage

import (
	"context"
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// VPCEndpointStatisticsStorage implements REST operations for VPCEndpointStatistics.
type VPCEndpointStatisticsStorage struct {
	nsxClient *nsx.Client
	k8sClient k8sclient.Client
}

// NewVPCEndpointStatisticsStorage creates a new storage instance.
func NewVPCEndpointStatisticsStorage(nsxClient *nsx.Client, k8sClient k8sclient.Client) *VPCEndpointStatisticsStorage {
	return &VPCEndpointStatisticsStorage{
		nsxClient: nsxClient,
		k8sClient: k8sClient,
	}
}

// Get retrieves VPCEndpointStatistics for the given VPCEndpoint CR name within the namespace.
func (s *VPCEndpointStatisticsStorage) Get(ctx context.Context, namespace, name string) (*easv1alpha1.VPCEndpointStatistics, error) {
	endpoint := &vpcv1alpha1.VPCEndpoint{}
	if err := s.k8sClient.Get(ctx, k8sclient.ObjectKey{Namespace: namespace, Name: name}, endpoint); err != nil {
		return nil, HandleEASError(err, "vpcendpointstatistics", name, fmt.Errorf("failed to get VPCEndpoint CR %s/%s: %w", namespace, name, err))
	}
	info, err := findEndpointByUID(s.nsxClient, "VpcEndpoint", common.TagScopeVPCEndpointCRUID, endpoint.UID)
	if err != nil {
		return nil, HandleEASError(err, "vpcendpointstatistics", name, fmt.Errorf("failed to find NSX endpoint for VPCEndpoint CR %s/%s: %w", namespace, name, err))
	}
	if info == nil {
		return nil, k8serrors.NewNotFound(schema.GroupResource{Group: easv1alpha1.GroupVersion.Group, Resource: "vpcendpointstatistics"}, name)
	}

	nsxStats, err := s.nsxClient.VPCEndpointStatisticsClient.Get(info.OrgID, info.ProjectID, info.VPCID, info.ID)
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
