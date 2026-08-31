/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"reflect"
	"testing"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
)

func TestStatefulSetPodSubnetPortFeatureEnabled(t *testing.T) {
	nsxClient := &Client{}

	t.Run("nil client", func(t *testing.T) {
		assert.False(t, StatefulSetPodSubnetPortFeatureEnabled(nil))
	})

	t.Run("version supports", func(t *testing.T) {
		p := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *Client, feature int) bool {
			return feature == StatefulSetPod
		})
		defer p.Reset()
		assert.True(t, StatefulSetPodSubnetPortFeatureEnabled(nsxClient))
	})

	t.Run("version does not support", func(t *testing.T) {
		p := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *Client, feature int) bool {
			return false
		})
		defer p.Reset()
		assert.False(t, StatefulSetPodSubnetPortFeatureEnabled(nsxClient))
	})
}
