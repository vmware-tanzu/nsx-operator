/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"reflect"
	"testing"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

func TestStatefulSetPodSubnetPortFeatureEnabled(t *testing.T) {
	f := false
	tr := true
	nsxClient := &Client{}

	t.Run("nil client", func(t *testing.T) {
		assert.False(t, StatefulSetPodSubnetPortFeatureEnabled(nil, &config.NSXOperatorConfig{}))
	})

	t.Run("version supports and vpc_wcp_enhance opt-in", func(t *testing.T) {
		p := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *Client, feature int) bool {
			return feature == StatefulSetPod
		})
		defer p.Reset()
		assert.False(t, StatefulSetPodSubnetPortFeatureEnabled(nsxClient, nil))
		assert.False(t, StatefulSetPodSubnetPortFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: nil}))
		assert.False(t, StatefulSetPodSubnetPortFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}))
		assert.True(t, StatefulSetPodSubnetPortFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{VpcWcpEnhance: &tr}}))
		assert.False(t, StatefulSetPodSubnetPortFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{VpcWcpEnhance: &f}}))
	})

	t.Run("version does not support", func(t *testing.T) {
		p := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *Client, feature int) bool {
			return false
		})
		defer p.Reset()
		assert.False(t, StatefulSetPodSubnetPortFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}))
	})
}

func TestMacPoolDHCPFeatureEnabled(t *testing.T) {
	f := false
	tr := true
	nsxClient := &Client{}

	t.Run("nil client", func(t *testing.T) {
		assert.False(t, MacPoolDHCPFeatureEnabled(nil, &config.NSXOperatorConfig{}))
	})

	t.Run("NSX 9.2+ with vpc_wcp_enhance opt-in", func(t *testing.T) {
		p := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *Client, feature int) bool {
			return feature == IPv6
		})
		defer p.Reset()
		assert.False(t, MacPoolDHCPFeatureEnabled(nsxClient, nil))
		assert.False(t, MacPoolDHCPFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}))
		assert.True(t, MacPoolDHCPFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{VpcWcpEnhance: &tr}}))
		assert.False(t, MacPoolDHCPFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{VpcWcpEnhance: &f}}))
	})

	t.Run("NSX 9.1.2 does not support MAC_POOL", func(t *testing.T) {
		// StatefulSetPod (9.1.2) is available but IPv6 (9.2.0) is not: MAC_POOL must stay off,
		// since 9.1.2 rejects a MAC_POOL port with no specific IP.
		p := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *Client, feature int) bool {
			return feature == StatefulSetPod
		})
		defer p.Reset()
		assert.False(t, MacPoolDHCPFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{VpcWcpEnhance: &tr}}))
	})
}

func TestNamespaceGroupFeatureEnabled(t *testing.T) {
	f := false
	tr := true
	nsxClient := &Client{}

	t.Run("nil client", func(t *testing.T) {
		assert.False(t, NamespaceGroupFeatureEnabled(nil, &config.NSXOperatorConfig{}))
	})

	t.Run("NSX 9.2+ with vpc_wcp_enhance opt-in", func(t *testing.T) {
		p := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *Client, feature int) bool {
			return feature == NamespaceGroup
		})
		defer p.Reset()
		assert.False(t, NamespaceGroupFeatureEnabled(nsxClient, nil))
		assert.False(t, NamespaceGroupFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: nil}))
		assert.False(t, NamespaceGroupFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}))
		assert.True(t, NamespaceGroupFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{VpcWcpEnhance: &tr}}))
		assert.False(t, NamespaceGroupFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{VpcWcpEnhance: &f}}))
	})

	t.Run("version does not support", func(t *testing.T) {
		p := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *Client, feature int) bool {
			return false
		})
		defer p.Reset()
		assert.False(t, NamespaceGroupFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{VpcWcpEnhance: &tr}}))
	})
}

func TestSubnetPortSettingFeatureEnabled(t *testing.T) {
	enabled, disabled := true, false
	configurations := []struct {
		name           string
		operatorConfig *config.NSXOperatorConfig
		enabled        bool
	}{
		{name: "nil operator config"},
		{name: "nil NSX config", operatorConfig: &config.NSXOperatorConfig{}},
		{name: "omitted flag", operatorConfig: &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}},
		{name: "disabled flag", operatorConfig: &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{VpcWcpEnhance: &disabled}}},
		{name: "enabled flag", operatorConfig: &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{VpcWcpEnhance: &enabled}}, enabled: true},
	}
	for _, cfg := range configurations {
		t.Run(cfg.name, func(t *testing.T) {
			assert.False(t, SubnetPortSettingFeatureEnabled(nil, cfg.operatorConfig))
			nsxClient := &Client{}
			supported := false
			patches := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *Client, feature int) bool {
				assert.Equal(t, SubnetPortSetting, feature)
				return supported
			})
			defer patches.Reset()
			assert.False(t, SubnetPortSettingFeatureEnabled(nsxClient, cfg.operatorConfig))
			supported = true
			assert.Equal(t, cfg.enabled, SubnetPortSettingFeatureEnabled(nsxClient, cfg.operatorConfig))
		})
	}
}

func TestRestoreVifFeatureEnabled(t *testing.T) {
	f := false
	tr := true
	nsxClient := &Client{}

	t.Run("nil client", func(t *testing.T) {
		assert.False(t, RestoreVifFeatureEnabled(nil, &config.NSXOperatorConfig{}))
	})

	t.Run("version supports and restore_vif opt-in", func(t *testing.T) {
		p := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *Client, feature int) bool {
			return feature == RestoreVIF
		})
		defer p.Reset()
		assert.False(t, RestoreVifFeatureEnabled(nsxClient, nil))
		assert.False(t, RestoreVifFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: nil}))
		assert.False(t, RestoreVifFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}))
		assert.True(t, RestoreVifFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{RestoreVif: &tr}}))
		assert.False(t, RestoreVifFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{RestoreVif: &f}}))
	})

	t.Run("version does not support", func(t *testing.T) {
		p := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *Client, feature int) bool {
			return false
		})
		defer p.Reset()
		assert.False(t, RestoreVifFeatureEnabled(nsxClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}))
	})
}

func TestPodV2FeatureEnabled(t *testing.T) {
	tr := true
	f := false
	nsxClient := &Client{}

	t.Run("basic", func(t *testing.T) {
		assert.False(t, PodV2FeatureEnabled(nil, &config.NSXOperatorConfig{}))
		assert.False(t, PodV2FeatureEnabled(nsxClient, nil))
		assert.False(t, PodV2FeatureEnabled(nsxClient, &config.NSXOperatorConfig{K8sConfig: nil}))
		assert.False(t, PodV2FeatureEnabled(nsxClient, &config.NSXOperatorConfig{K8sConfig: &config.K8sConfig{}}))
		assert.True(t, PodV2FeatureEnabled(nsxClient, &config.NSXOperatorConfig{K8sConfig: &config.K8sConfig{PodV2: &tr}}))
		assert.False(t, PodV2FeatureEnabled(nsxClient, &config.NSXOperatorConfig{K8sConfig: &config.K8sConfig{PodV2: &f}}))
	})
}
