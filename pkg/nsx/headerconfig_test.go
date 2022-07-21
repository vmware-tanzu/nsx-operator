package nsx

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	policyclient "github.com/vmware/vsphere-automation-sdk-go/runtime/protocol/client"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func TestHeaderConfig(t *testing.T) {
	assert := assert.New(t)
	partial := false
	result := `{
		"healthy" : true,
		"components_health" : "POLICY:UP, SEARCH:UP, MANAGER:UP, NODE_MGMT:UP, UI:UP"
	  }`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Index(r.URL.Path, "tier-1s") > 0 {
			if r.Header.Get("nsx-enable-partial-patch") == "true" {
				partial = true
			}
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(result))
	}))
	defer ts.Close()
	httpClient := http.Client{
		Transport: &http.Transport{},
	}
	connector := policyclient.NewRestConnector(fmt.Sprintf("%s", ts.URL), httpClient)
	header := CreateHeaderConfig(false, false, false)
	header.SetNSXEnablePartialPatch(true).Done(connector)
	client := infra.NewTier1sClient(connector)
	tier1 := model.Tier1{}
	client.Patch("test-tier1-id", tier1)
	client.Patch("test-tier1-id", tier1)
	assert.True(partial)
}

func TestCreateHeaderConfig(t *testing.T) {
	cfg := CreateHeaderConfig(true, true, true)
	assert.NotNil(t, cfg)
	cfg.SetXAllowOverrite(false)
	cfg.SetConfigXallowOverwrite(false)
	cfg.SetNSXEnablePartialPatch(false)
}
