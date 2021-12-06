// Copyright (c) 2021 VMware, Inc. All rights reserved. VMware Confidential.

package nsx

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func TestClient(t *testing.T) {
	host := "10.168.177.108"
	config := NewConfig(host, "admin", "Admin!23Admin", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil)
	cluster, _ := NewCluster(config)
	connector, _ := cluster.NewRestConnector()
	client := infra.NewTier1sClient(connector)
	tier1 := model.Tier1{}
	err := client.Patch("test-tier1-id", tier1)
	assert.Nil(t, err, fmt.Sprintf("create tier1 error %v", err))
}
