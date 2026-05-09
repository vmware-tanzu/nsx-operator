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
