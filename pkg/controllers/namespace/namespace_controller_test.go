/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package namespace

import (
	"errors"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	vpcnetworktesting "github.com/vmware-tanzu/nsx-operator/pkg/controllers/vpcnetwork/testing"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

func createNameSpaceReconciler() *NamespaceReconciler {
	service := &vpc.VPCService{
		Service: common.Service{
			Client:    nil,
			NSXClient: &nsx.Client{},

			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
		VPCNetworkConfigStore: vpc.VPCNetworkInfoStore{
			VPCNetworkConfigMap: map[string]common.VPCNetworkConfigInfo{},
		},
		VPCNSNetworkConfigStore: vpc.VPCNsNetworkConfigStore{
			VPCNSNetworkConfigMap: map[string]string{},
		},
	}

	return &NamespaceReconciler{
		Client:          fake.NewClientBuilder().Build(),
		Scheme:          fake.NewClientBuilder().Build().Scheme(),
		VPCService:      service,
		NetworkProvider: &vpcnetworktesting.FakeVPCNetworkProvider{},
	}
}

func TestGetDefaultNetworkConfigName(t *testing.T) {
	fakeNC := &common.VPCNetworkConfigInfo{
		IsDefault: true,
		Name:      "fake-name",
	}
	r := createNameSpaceReconciler()
	tests := []struct {
		name       string
		exist      bool
		nc         *common.VPCNetworkConfigInfo
		expectName string
	}{
		{"1", false, nil, ""},
		{"2", true, fakeNC, "fake-name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetDefaultNetworkConfig", func(_ *vpc.VPCService) (bool, *common.VPCNetworkConfigInfo) {
				return tt.exist, tt.nc
			})
			name, err := r.getDefaultNetworkConfigName()
			assert.Equal(t, tt.expectName, name)
			if name == "" {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
			patch.Reset()
		})
	}
}

func TestInsertNamespaceNetworkconfigBinding(t *testing.T) {
	r := createNameSpaceReconciler()
	fakeErr := errors.New("fake-error")
	tests := []struct {
		name       string
		ns         string
		exist      bool
		ncName     string
		annos      map[string]string
		expectName string
		err        error
	}{
		{"1", "test-ns1", false, "", nil, "", fakeErr},
		{"2", "test-ns2", true, "fake-nc1", map[string]string{}, "fake-nc1", nil},
		{"3", "test-ns3", true, "fake-nc2", map[string]string{common.AnnotationVPCNetworkConfig: "fake-name"}, "fake-name", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r.VPCService = &common.MockVPCServiceProvider{}
			patch := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getDefaultNetworkConfigName", func(_ *NamespaceReconciler, ns string, anno map[string]string) (string, error) {
				return tt.ncName, tt.err
			})
			r.VPCService.(*common.MockVPCServiceProvider).On("RegisterNamespaceNetworkconfigBinding", mock.Anything, mock.Anything).Return(nil)
			err := r.insertNamespaceNetworkconfigBinding(tt.ns, tt.annos)
			if err != nil {
				r.VPCService.(*common.MockVPCServiceProvider).AssertNotCalled(t, "RegisterNamespaceNetworkconfigBinding")
			} else {
				r.VPCService.(*common.MockVPCServiceProvider).AssertCalled(t, "RegisterNamespaceNetworkconfigBinding", tt.ns, tt.expectName)
			}
			patch.Reset()
		})
	}
}
