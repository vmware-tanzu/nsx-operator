/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package identity resolves the source systems used by a migration.
package identity

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/vapi/rest"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/fabric"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/inspection"
)

// SupervisorZone is one zone returned by the authenticated vCenter Supervisor API.
type SupervisorZone struct {
	Zone     string   `json:"zone"`
	Clusters []string `json:"clusters"`
}

// SupervisorTopologyReader verifies that the external Supervisor ID belongs to
// the vCenter session used for this evaluation.
type SupervisorTopologyReader interface {
	ReadTopology(context.Context, string) ([]SupervisorZone, error)
}

type ComputeManager struct {
	ID         string
	OriginType string
	Server     string
}

// ComputeManagerReader returns NSX's registered compute managers.
type ComputeManagerReader interface {
	ListComputeManagers(context.Context) ([]ComputeManager, error)
}

// Resolver validates the external Supervisor ID and finds its matching NSX
// compute-manager record. It stores only the non-secret, reusable identities.
type Resolver struct {
	SupervisorTopology SupervisorTopologyReader
	ComputeManagers    ComputeManagerReader
	VCenterUUID        string
	VCenterServer      string
}

// NewResolver connects the authenticated clients to the identity lookups.
func NewResolver(clients *AuthenticatedClients) (Resolver, error) {
	if clients == nil || clients.VCenter == nil {
		return Resolver{}, fmt.Errorf("authenticated vCenter SOAP/REST and NSX clients are required")
	}
	if clients.VCenter.Client == nil || clients.VCenter.Client.Client == nil {
		return Resolver{}, fmt.Errorf("authenticated vCenter identity is incomplete")
	}
	if clients.VCenterREST == nil || clients.NSX == nil || clients.NSX.RestConnector == nil {
		return Resolver{}, fmt.Errorf("authenticated vCenter SOAP/REST and NSX clients are required")
	}
	vCenterUUID := clients.VCenter.ServiceContent.About.InstanceUuid
	vCenterURL := clients.VCenter.URL()
	if vCenterURL == nil {
		return Resolver{}, fmt.Errorf("authenticated vCenter identity is incomplete")
	}
	server := vCenterURL.Hostname()
	if vCenterUUID == "" || server == "" {
		return Resolver{}, fmt.Errorf("authenticated vCenter identity is incomplete")
	}
	return Resolver{
		SupervisorTopology: &VCenterSupervisorTopology{Client: clients.VCenterREST, credentials: clients.vCenterRESTCredentials},
		ComputeManagers:    NSXComputeManagers{Client: fabric.NewComputeManagersClient(clients.NSX.RestConnector)},
		VCenterUUID:        vCenterUUID,
		VCenterServer:      server,
	}, nil
}

func (resolver Resolver) Resolve(ctx context.Context, supervisorID string, generation int64) (api.ResolvedSourceContext, error) {
	if resolver.SupervisorTopology == nil || resolver.ComputeManagers == nil || resolver.VCenterUUID == "" || resolver.VCenterServer == "" || !validSupervisorID(supervisorID) || generation < 1 {
		return api.ResolvedSourceContext{}, fmt.Errorf("authenticated vCenter and NSX clients, Supervisor ID, and Evaluate generation are required")
	}

	zones, err := resolver.SupervisorTopology.ReadTopology(ctx, supervisorID)
	if err != nil {
		return api.ResolvedSourceContext{}, fmt.Errorf("validate Supervisor ID against vCenter topology: %w", err)
	}
	if err := validateTopology(zones); err != nil {
		return api.ResolvedSourceContext{}, fmt.Errorf("Supervisor topology is not ready: %w", err)
	}

	managers, err := resolver.ComputeManagers.ListComputeManagers(ctx)
	if err != nil {
		return api.ResolvedSourceContext{}, fmt.Errorf("list NSX compute managers: %w", err)
	}
	wantServer := normalizeServer(resolver.VCenterServer)
	var matchingIDs []string
	for _, manager := range managers {
		if strings.EqualFold(manager.OriginType, "VCENTER") && normalizeServer(manager.Server) == wantServer {
			matchingIDs = append(matchingIDs, manager.ID)
		}
	}
	if len(matchingIDs) != 1 {
		return api.ResolvedSourceContext{}, fmt.Errorf("expected one NSX vCenter compute manager for %q, found %d", resolver.VCenterServer, len(matchingIDs))
	}
	if matchingIDs[0] == "" {
		return api.ResolvedSourceContext{}, fmt.Errorf("NSX vCenter compute manager for %q has an empty ID", resolver.VCenterServer)
	}

	return api.ResolvedSourceContext{
		EvaluatedGeneration: generation,
		VCenterInstanceUUID: resolver.VCenterUUID,
		NSXComputeManagerID: matchingIDs[0],
	}, nil
}

func validSupervisorID(supervisorID string) bool {
	_, err := uuid.Parse(supervisorID)
	return err == nil
}

func validateTopology(zones []SupervisorZone) error {
	if len(zones) == 0 {
		return fmt.Errorf("no enabled zones were returned")
	}
	clusterCount := 0
	for _, zone := range zones {
		if zone.Zone == "" {
			return fmt.Errorf("topology contains a zone without an ID")
		}
		for _, cluster := range zone.Clusters {
			if cluster == "" {
				return fmt.Errorf("topology contains an empty cluster ID")
			}
		}
		clusterCount += len(zone.Clusters)
	}
	if clusterCount == 0 {
		return fmt.Errorf("topology contains no enabled clusters")
	}
	return nil
}

func normalizeServer(server string) string {
	server = strings.TrimSpace(server)
	if parsed, err := url.Parse(server); err == nil && parsed.Host != "" {
		server = parsed.Hostname()
	} else if host, _, err := net.SplitHostPort(server); err == nil {
		server = host
	}
	return strings.TrimSuffix(strings.ToLower(server), ".")
}

// VCenterSupervisorTopology reads topology through an authenticated REST client.
type VCenterSupervisorTopology struct {
	Client         *rest.Client
	credentials    *url.Userinfo
	sessionManager vCenterRESTSessionManager
	readTopologyFn func(context.Context, string) ([]SupervisorZone, error)
	sessionMu      sync.Mutex
}

func (reader *VCenterSupervisorTopology) ReadTopology(ctx context.Context, supervisorID string) ([]SupervisorZone, error) {
	if !validSupervisorID(supervisorID) {
		return nil, fmt.Errorf("authenticated vCenter REST client and Supervisor ID are required")
	}
	if reader.Client == nil && (reader.sessionManager == nil || reader.readTopologyFn == nil) {
		return nil, fmt.Errorf("authenticated vCenter REST client and Supervisor ID are required")
	}
	if _, err := reader.refreshSession(ctx); err != nil {
		return nil, err
	}
	zones, err := reader.readTopology(ctx, supervisorID)
	if err != nil {
		// The session can expire between this read and the topology request. The
		// GET is safe to retry once after checking and refreshing the session.
		refreshed, refreshErr := reader.refreshSession(ctx)
		if refreshErr != nil {
			return nil, fmt.Errorf("read Supervisor topology: %w (session refresh failed: %v)", err, refreshErr)
		}
		if !refreshed {
			return nil, err
		}
		zones, err = reader.readTopology(ctx, supervisorID)
		if err != nil {
			return nil, err
		}
	}
	return zones, nil
}

func (reader *VCenterSupervisorTopology) readTopology(ctx context.Context, supervisorID string) ([]SupervisorZone, error) {
	if reader.readTopologyFn != nil {
		return reader.readTopologyFn(ctx, supervisorID)
	}
	var zones []SupervisorZone
	resource := reader.Client.Resource("/api/vcenter/namespace-management/supervisors").WithSubpath(supervisorID).WithSubpath("topology")
	if err := reader.Client.Do(ctx, resource.Request(http.MethodGet), &zones); err != nil {
		return nil, err
	}
	return zones, nil
}

type vCenterRESTSessionManager interface {
	Session(context.Context) (*rest.Session, error)
	Login(context.Context, *url.Userinfo) error
}

func ensureVCenterRESTSession(ctx context.Context, manager vCenterRESTSessionManager, credentials *url.Userinfo) (bool, error) {
	session, err := manager.Session(ctx)
	if err != nil {
		return false, fmt.Errorf("check vCenter REST session: %w", err)
	}
	if session != nil {
		return false, nil
	}
	if credentials == nil {
		return false, fmt.Errorf("vCenter REST session expired and credentials are unavailable")
	}
	if err := manager.Login(ctx, credentials); err != nil {
		return false, fmt.Errorf("refresh vCenter REST session: %w", err)
	}
	return true, nil
}

func (reader *VCenterSupervisorTopology) refreshSession(ctx context.Context) (bool, error) {
	reader.sessionMu.Lock()
	defer reader.sessionMu.Unlock()
	manager := reader.sessionManager
	if manager == nil {
		manager = reader.Client
	}
	if manager == nil {
		return false, fmt.Errorf("authenticated vCenter REST client is required")
	}
	return ensureVCenterRESTSession(ctx, manager, reader.credentials)
}

type computeManagerPage struct {
	Managers []ComputeManager
	Cursor   *string
}

type computeManagerPageReader func(cursor *string, pageSize int64) (computeManagerPage, error)

// NSXComputeManagers reads NSX's registered compute managers, following every
// cursor so identity resolution cannot silently miss a matching record.
type NSXComputeManagers struct {
	Client   fabric.ComputeManagersClient
	listPage computeManagerPageReader
}

func (reader NSXComputeManagers) ListComputeManagers(ctx context.Context) ([]ComputeManager, error) {
	if reader.Client == nil && reader.listPage == nil {
		return nil, fmt.Errorf("NSX compute-manager client is required")
	}
	listPage := reader.listPage
	if listPage == nil {
		listPage = func(cursor *string, pageSize int64) (computeManagerPage, error) {
			page, err := reader.Client.List(cursor, nil, nil, &pageSize, nil, nil, nil)
			if err != nil {
				return computeManagerPage{}, err
			}
			result := computeManagerPage{Cursor: page.Cursor}
			for _, manager := range page.Results {
				result.Managers = append(result.Managers, ComputeManager{ID: stringValue(manager.Id), OriginType: stringValue(manager.OriginType), Server: stringValue(manager.Server)})
			}
			return result, nil
		}
	}
	pageSize := int64(1000)
	var cursor *string
	seenCursors := make(map[string]struct{})
	var result []ComputeManager
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Filter locally because NSX versions may serialize the vCenter origin
		// type with different casing.
		page, err := listPage(cursor, pageSize)
		if err != nil {
			return nil, err
		}
		result = append(result, page.Managers...)
		if page.Cursor == nil || *page.Cursor == "" {
			return result, nil
		}
		if _, found := seenCursors[*page.Cursor]; found {
			return nil, fmt.Errorf("NSX repeated a compute-manager pagination cursor")
		}
		seenCursors[*page.Cursor] = struct{}{}
		cursor = page.Cursor
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// RecordResolvedSourceContext stores the non-secret result only after Evaluate
// has completed all of its checks. The caller should leave this field unset on
// a failed or incomplete Evaluate; the controller's status update then persists
// this value atomically with the successful Evaluate checkpoint.
func RecordResolvedSourceContext(status *api.ControlPlaneNetworkTransitionStatus, source api.ResolvedSourceContext, generation int64) error {
	if status == nil {
		return fmt.Errorf("control-plane transition status is required")
	}
	if generation < 1 || source.EvaluatedGeneration != generation {
		return fmt.Errorf("resolved source context generation %d does not match Evaluate generation %d", source.EvaluatedGeneration, generation)
	}
	if source.VCenterInstanceUUID == "" || source.NSXComputeManagerID == "" {
		return fmt.Errorf("resolved source context must include vCenter UUID and NSX compute-manager ID")
	}
	status.ResolvedSourceContext = source.DeepCopy()
	return nil
}

// ValidateReuse permits Execute to reuse only the resolved source context
// persisted by the immediately preceding successful Evaluate. The source
// context is the durable Evaluate checkpoint because the shared LastAction and
// ActionGeneration fields advance to Execute before its handler is called.
func ValidateReuse(status *api.ControlPlaneNetworkTransitionStatus, action api.Action, currentVCenterUUID string, currentGeneration int64) error {
	if status == nil || status.ResolvedSourceContext == nil {
		return fmt.Errorf("resolved source context is missing; run Evaluate")
	}
	source := status.ResolvedSourceContext
	if source.NSXComputeManagerID == "" || source.VCenterInstanceUUID == "" || source.EvaluatedGeneration < 1 {
		return fmt.Errorf("resolved source context is missing; run Evaluate")
	}
	if source.VCenterInstanceUUID != currentVCenterUUID {
		return fmt.Errorf("resolved source context belongs to a different vCenter; run Evaluate")
	}
	if action != api.ActionExecute {
		return fmt.Errorf("resolved source context cannot be reused for action %q", action)
	}
	evaluateGeneration := currentGeneration - 1
	if currentGeneration < 2 || source.EvaluatedGeneration != evaluateGeneration {
		return fmt.Errorf("resolved source context generation %d does not match the Evaluate generation for current Execute generation %d", source.EvaluatedGeneration, currentGeneration)
	}
	if !conditionSucceededAt(status.Conditions, inspection.ConditionNetworkInputsValidated, evaluateGeneration) ||
		!conditionSucceededAt(status.Conditions, inspection.ConditionDVPGConversionValidated, evaluateGeneration) {
		return fmt.Errorf("resolved source context is not backed by successful Evaluate checks for generation %d", evaluateGeneration)
	}
	return nil
}

func conditionSucceededAt(conditions []metav1.Condition, conditionType string, generation int64) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition.Status == metav1.ConditionTrue && condition.ObservedGeneration == generation
		}
	}
	return false
}
