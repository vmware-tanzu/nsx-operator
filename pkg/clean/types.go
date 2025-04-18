package clean

import (
	"context"
)

const (
	vpcCleanerWorkers = 8
	maxRetries        = 12
)

type vpcPreCleaner interface {
	// CleanupBeforeVPCDeletion is called to clean up the VPC resources which may block the VPC recursive deletion, or
	// break the parallel deletion with other resource types, e.g., VpcSubnetPort, SubnetConnectionBindingMap, LBVirtualServer.
	// CleanupBeforeVPCDeletion is called before recursively deleting the VPCs in parallel.
	CleanupBeforeVPCDeletion(ctx context.Context) error
}

type vpcChildrenCleaner interface {
	// CleanupVPCChildResources is called when cleaning up the VPC related resources. For the resources in an auto-created
	// VPC, this function is called after the VPC is recursively deleted on NSX, so the providers only needs to clean up
	// with the local cache. It uses an empty string for vpcPath for all pre-created VPCs, so the providers should delete
	// resources both on NSX and from the local cache.
	// CleanupVPCChildResources is called after the VPC with path "vpcPath" is recursively deleted.
	CleanupVPCChildResources(ctx context.Context, vpcPath string) error
}

type infraCleaner interface {
	// CleanupInfraResources is to clean up the resources created under path /infra.
	CleanupInfraResources(ctx context.Context) error
}

type cleanupFunc func() (interface{}, error)
