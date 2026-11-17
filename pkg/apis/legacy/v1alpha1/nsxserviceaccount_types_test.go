/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNSXServiceAccountMultiTenancyStatusCompatibility(t *testing.T) {
	var old NSXServiceAccountStatus
	require.NoError(t, json.Unmarshal([]byte(`{"clusterID":"existing","vpcPath":"/legacy"}`), &old))
	require.False(t, old.MultiTenancy)
	require.Equal(t, "existing", old.ClusterID)

	legacyJSON, err := json.Marshal(old)
	require.NoError(t, err)
	require.NotContains(t, string(legacyJSON), `"multiTenancy"`)

	old.MultiTenancy = true
	newJSON, err := json.Marshal(old)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(newJSON, &fields))
	require.Equal(t, true, fields["multiTenancy"])
	require.Equal(t, "existing", fields["clusterID"])
	require.Equal(t, "/legacy", fields["vpcPath"])
}
