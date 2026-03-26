/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"reflect"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestInitializeDNSRecordService(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()

	commonService := common.Service{
		Client:    fc,
		NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{}},
	}

	patch := gomonkey.ApplyMethod(reflect.TypeOf(&commonService), "InitializeResourceStore",
		func(_ *common.Service, wg *sync.WaitGroup, _ chan error, _ string, _ []model.Tag, _ common.Store) {
			wg.Done()
		})
	defer patch.Reset()

	svc, err := InitializeDNSRecordService(commonService, nil)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.NotNil(t, svc.DNSRecordStore)
	assert.Nil(t, svc.DNSRecordStore.BindingType)
}
