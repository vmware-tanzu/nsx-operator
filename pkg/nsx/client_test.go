// Copyright (c) 2021 VMware, Inc. All rights reserved. VMware Confidential.

package nsx

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"gitlab.eng.vmware.com/nsx-container-plugin/vmware-nsxlib-go/pkg/nsx/ratelimiter"
)

func TestClient(t *testing.T) {
	a := "10.187.135.151"
	config := NewConfig(a, "admin", "passw0rd", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil)
	cluster, _ := NewCluster(config)
	connector, _ := cluster.NewRestConnector()
	client := infra.NewDefaultTier1sClient(connector)
	tier1 := model.Tier1{}
	err := client.Patch("test-tier1-id", tier1)
	assert.Nil(t, err, fmt.Sprintf("create tier1 error %v", err))
}

func TestQuery(t *testing.T) {

}
