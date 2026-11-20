/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/govmomi/vapi/rest"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/soap"
	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
)

type nsxZonesStub struct{ available bool }

func (s nsxZonesStub) List(local, _, _, _, origin *string, _ *int64, _ *bool, _ *string) (mpmodel.ZoneListResult, error) {
	if !s.available {
		return mpmodel.ZoneListResult{}, nil
	}
	localID := "zone-a"
	if local != nil {
		localID = *local
	}
	id := *origin + ":" + localID
	return mpmodel.ZoneListResult{Results: []mpmodel.Zone{{CmLocalId: &localID, OriginId: origin, ExternalId: &id}}}, nil
}

func TestVCenterZoneResolver(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/vcenter/namespace-management/supervisors/supervisor-1/topology", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"zone":"zone-a","clusters":["domain-c1"]}]`))
	}))
	defer server.Close()
	endpoint, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := rest.NewClient(&vim25.Client{Client: soap.NewClient(endpoint, false)})
	resolver := VCenterZoneResolver{VCenter: client, NSXZones: nsxZonesStub{available: true}, ComputeManagerID: "vc-1", SupervisorID: "supervisor-1"}
	resolved, err := resolver.ResolveZones(context.Background(), []string{"domain-c1", "domain-c2"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"domain-c1": "vc-1:zone-a"}, resolved)
	resolver.NSXZones = nsxZonesStub{}
	_, err = resolver.ResolveZones(context.Background(), []string{"domain-c1"})
	require.ErrorContains(t, err, "not yet available in NSX")
}
