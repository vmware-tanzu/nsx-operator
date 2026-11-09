/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	subnetservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	portservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
)

// subnetPortSupportsDNSAutoRecord limits automatic records to VM-owned ports.
// Image fetcher and Pod networking do not participate in this feature.
func subnetPortSupportsDNSAutoRecord(port *v1alpha1.SubnetPort) bool {
	if port == nil || port.Labels[servicecommon.LabelImageFetcher] == "true" {
		return false
	}
	for _, owner := range port.OwnerReferences {
		if owner.Kind == servicecommon.ResourceTypeVirtualMachine && strings.HasPrefix(owner.APIVersion, "vmoperator.vmware.com/") {
			return true
		}
	}
	return false
}

// subnetPortDNSSync runs on the leader and stops with the manager.
type subnetPortDNSSync struct {
	reconciler *SubnetPortReconciler
}

func (*subnetPortDNSSync) NeedLeaderElection() bool { return true }

func (s *subnetPortDNSSync) Start(ctx context.Context) error {
	ticker := time.NewTicker(portservice.DNSAutoRecordSyncInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := s.reconciler.syncSubnetPortDNSStatus(ctx); err != nil {
			log.Error(err, "Failed to sync SubnetPort DNS status")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	return nil
}

// syncSubnetPortDNSStatus collects ports and active projects, refreshes DNS
// mappings once per project, then updates status using the refreshed cache.
func (r *SubnetPortReconciler) syncSubnetPortDNSStatus(ctx context.Context) error {
	service := r.SubnetPortService
	if !nsx.DNSAutoRecordFeatureEnabled(service.NSXClient, service.NSXConfig) {
		return nil
	}
	ports := &v1alpha1.SubnetPortList{}
	if err := r.Client.List(ctx, ports); err != nil {
		return err
	}
	activeProjects := make(map[string]bool)
	type dnsStatusUpdate struct {
		port       *v1alpha1.SubnetPort
		subnet     *model.VpcSubnet
		nsxPort    *model.VpcSubnetPort
		projectKey string
	}
	var updates []dnsStatusUpdate
	subnets := make(map[string]*model.VpcSubnet)
	var syncErrors []error
	for i := range ports.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		port := &ports.Items[i]
		if !port.DeletionTimestamp.IsZero() || !subnetPortSupportsDNSAutoRecord(port) {
			continue
		}
		nsxPort, err := service.SubnetPortStore.GetVpcSubnetPortByUID(port.UID)
		if err != nil {
			syncErrors = append(syncErrors, err)
			continue
		}
		if nsxPort == nil || nsxPort.ParentPath == nil {
			continue
		}
		if nsxPort.DnsHostname == nil || strings.TrimSpace(*nsxPort.DnsHostname) == "" {
			continue
		}
		path := *nsxPort.ParentPath
		info, err := servicecommon.ParseVPCResourcePath(path)
		if err != nil {
			syncErrors = append(syncErrors, err)
			continue
		}
		projectKey := info.OrgID + "/" + info.ProjectID
		activeProjects[projectKey] = true
		subnet, loaded := subnets[path]
		if !loaded {
			subnet, err = r.SubnetService.GetSubnetByPath(path, false)
			// Shared subnets may live only in NSXSubnetCache rather than SubnetStore.
			// A matching store entry already includes IpBlocks and needs no fallback.
			if errors.Is(err, subnetservice.ErrSubnetNotFoundInStore) || (err == nil && (subnet == nil || subnet.Path == nil || *subnet.Path != path)) {
				var associatedResource string
				associatedResource, err = servicecommon.ConvertSubnetPathToAssociatedResource(path)
				if err == nil {
					subnet, err = r.SubnetService.GetNSXSubnetFromCacheOrAPI(associatedResource, false)
				}
			}
			if err != nil {
				syncErrors = append(syncErrors, err)
				subnet = nil
			}
			subnets[path] = subnet
		}
		if subnet == nil {
			continue
		}
		updates = append(updates, dnsStatusUpdate{port: port, subnet: subnet, nsxPort: nsxPort, projectKey: projectKey})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	refreshedProjects, err := service.RefreshDNSAutoRecordConfigs(activeProjects)
	if err != nil {
		syncErrors = append(syncErrors, err)
	}
	for _, update := range updates {
		if !refreshedProjects[update.projectKey] {
			continue
		}
		if err := r.updateSubnetPortDNSStatus(ctx, update.port, update.subnet, update.nsxPort); err != nil {
			syncErrors = append(syncErrors, err)
		}
	}
	// Do not evict entries on a partial store lookup failure.
	if len(syncErrors) == 0 {
		service.PruneDNSCache(activeProjects)
	}
	return errors.Join(syncErrors...)
}

// populateSubnetPortFQDNs prepares FQDNs for the controller's normal status update.
func (r *SubnetPortReconciler) populateSubnetPortFQDNs(port *v1alpha1.SubnetPort, subnet *model.VpcSubnet) error {
	if !subnetPortSupportsDNSAutoRecord(port) || !nsx.DNSAutoRecordFeatureEnabled(r.SubnetPortService.NSXClient, r.SubnetPortService.NSXConfig) {
		return nil
	}
	nsxPort, err := r.SubnetPortService.SubnetPortStore.GetVpcSubnetPortByUID(port.UID)
	if err != nil {
		return err
	}
	return r.updateSubnetPortCRFQDNs(port, subnet, nsxPort)
}

// updateSubnetPortCRFQDNs prepares the in-memory status for either the normal
// reconcile's status update or the periodic sync's status patch.
func (r *SubnetPortReconciler) updateSubnetPortCRFQDNs(port *v1alpha1.SubnetPort, subnet *model.VpcSubnet, nsxPort *model.VpcSubnetPort) error {
	if port == nil || nsxPort == nil || nsxPort.DnsHostname == nil || strings.TrimSpace(*nsxPort.DnsHostname) == "" {
		return nil
	}
	if subnet == nil || subnet.Path == nil {
		return nil
	}
	fqdns, err := r.SubnetPortService.ResolveSubnetPortFQDNs(subnet, *nsxPort.DnsHostname)
	if err != nil {
		return fmt.Errorf("failed to resolve SubnetPort FQDNs: %w", err)
	}
	port.Status.FQDNs = fqdns
	return nil
}

// updateSubnetPortDNSStatus persists periodic FQDN changes to Kubernetes.
// Normal reconcile instead saves FQDNs together with the other CR status fields.
func (r *SubnetPortReconciler) updateSubnetPortDNSStatus(ctx context.Context, candidate *v1alpha1.SubnetPort, subnet *model.VpcSubnet, nsxPort *model.VpcSubnetPort) error {
	// Most ports are unchanged: compare against the informer snapshot before
	// doing any API-server reads or writes.
	desired := candidate.DeepCopy()
	if err := r.updateSubnetPortCRFQDNs(desired, subnet, nsxPort); err != nil {
		return err
	}
	if slices.Equal(candidate.Status.FQDNs, desired.Status.FQDNs) {
		return nil
	}
	reader := client.Reader(r.Client)
	if r.APIReader != nil {
		reader = r.APIReader
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		port := &v1alpha1.SubnetPort{}
		if err := reader.Get(ctx, client.ObjectKeyFromObject(candidate), port); err != nil {
			return client.IgnoreNotFound(err)
		}
		if port.UID != candidate.UID || !port.DeletionTimestamp.IsZero() {
			return nil
		}
		if slices.Equal(port.Status.FQDNs, desired.Status.FQDNs) {
			return nil
		}
		before := port.DeepCopy()
		port.Status.FQDNs = slices.Clone(desired.Status.FQDNs)
		return r.Client.Status().Patch(ctx, port, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	})
}
