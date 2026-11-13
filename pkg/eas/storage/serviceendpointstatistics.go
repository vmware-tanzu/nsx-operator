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

// ServiceEndpointStatisticsStorage implements REST operations for ServiceEndpointStatistics.
type ServiceEndpointStatisticsStorage struct {
	nsxClient *nsx.Client
	k8sClient k8sclient.Client
}

// NewServiceEndpointStatisticsStorage creates a new storage instance.
func NewServiceEndpointStatisticsStorage(nsxClient *nsx.Client, k8sClient k8sclient.Client) *ServiceEndpointStatisticsStorage {
	return &ServiceEndpointStatisticsStorage{
		nsxClient: nsxClient,
		k8sClient: k8sClient,
	}
}

// Get retrieves ServiceEndpointStatistics for the given ServiceEndpoint CR name within the namespace.
func (s *ServiceEndpointStatisticsStorage) Get(ctx context.Context, namespace, name string) (*easv1alpha1.ServiceEndpointStatistics, error) {
	endpoint := &vpcv1alpha1.ServiceEndpoint{}
	if err := s.k8sClient.Get(ctx, k8sclient.ObjectKey{Namespace: namespace, Name: name}, endpoint); err != nil {
		return nil, HandleEASError(err, "serviceendpointstatistics", name, fmt.Errorf("failed to get ServiceEndpoint CR %s/%s: %w", namespace, name, err))
	}
	info, err := findEndpointByUID(s.nsxClient, "VpcServiceEndpoint", common.TagScopeServiceEndpointCRUID, endpoint.UID)
	if err != nil {
		return nil, HandleEASError(err, "serviceendpointstatistics", name, fmt.Errorf("failed to find NSX endpoint for ServiceEndpoint CR %s/%s: %w", namespace, name, err))
	}
	if info == nil {
		return nil, k8serrors.NewNotFound(schema.GroupResource{Group: easv1alpha1.GroupVersion.Group, Resource: "serviceendpointstatistics"}, name)
	}

	nsxStats, err := s.nsxClient.VPCServiceEndpointStatisticsClient.Get(info.OrgID, info.ProjectID, info.VPCID, info.ID)
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
