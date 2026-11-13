/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package vpcendpoint

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func scheme() *apimachineryruntime.Scheme {
	s := apimachineryruntime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	return s
}

// fakeIPAllocationService is a test double for the provider interface.
type fakeIPAllocationService struct {
	getByOwner func(owner metav1.Object) (*model.VpcIpAddressAllocation, error)
}

func (f *fakeIPAllocationService) GetIPAddressAllocationByOwner(owner metav1.Object) (*model.VpcIpAddressAllocation, error) {
	return f.getByOwner(owner)
}
func (f *fakeIPAllocationService) CreateIPAddressAllocationForAddressBinding(*v1alpha1.AddressBinding, *v1alpha1.SubnetPort, bool) error {
	return nil
}
func (f *fakeIPAllocationService) DeleteIPAddressAllocationForAddressBinding(metav1.Object) error {
	return nil
}
func (f *fakeIPAllocationService) BuildIPAddressAllocationID(metav1.Object) string { return "" }
func (f *fakeIPAllocationService) DeleteIPAddressAllocationByNSXResource(*model.VpcIpAddressAllocation) error {
	return nil
}
func (f *fakeIPAllocationService) ListIPAddressAllocationWithAddressBinding() []*model.VpcIpAddressAllocation {
	return nil
}

func TestResolveServiceEndpointPath(t *testing.T) {
	service := newTestService()

	path, err := service.resolveServiceEndpointPath("proj-1:vpc-1:se-1")
	assert.NoError(t, err)
	assert.Equal(t, "/orgs/default/projects/proj-1/vpcs/vpc-1/vpc-service-endpoints/se-1", path)

	_, err = service.resolveServiceEndpointPath("not-a-valid-name")
	assert.Error(t, err)
}

func TestResolveIPAllocationPath(t *testing.T) {
	ns, name := "ns-1", "ipa-1"
	ipAllocCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
	}

	t.Run("CR not found", func(t *testing.T) {
		service := &VPCEndpointService{}
		service.Client = fake.NewClientBuilder().WithScheme(scheme()).Build()
		_, err := service.resolveIPAllocationPath(context.Background(), ns, name)
		assert.ErrorContains(t, err, "not found")
	})

	t.Run("NSX allocation not found in store", func(t *testing.T) {
		service := &VPCEndpointService{}
		service.Client = fake.NewClientBuilder().WithScheme(scheme()).WithObjects(ipAllocCR).Build()
		service.IPAllocationService = &fakeIPAllocationService{
			getByOwner: func(metav1.Object) (*model.VpcIpAddressAllocation, error) { return nil, nil },
		}
		_, err := service.resolveIPAllocationPath(context.Background(), ns, name)
		assert.ErrorContains(t, err, "not found in store")
	})

	t.Run("NSX allocation has no path", func(t *testing.T) {
		service := &VPCEndpointService{}
		service.Client = fake.NewClientBuilder().WithScheme(scheme()).WithObjects(ipAllocCR).Build()
		service.IPAllocationService = &fakeIPAllocationService{
			getByOwner: func(metav1.Object) (*model.VpcIpAddressAllocation, error) {
				return &model.VpcIpAddressAllocation{}, nil
			},
		}
		_, err := service.resolveIPAllocationPath(context.Background(), ns, name)
		assert.ErrorContains(t, err, "no policy path")
	})

	t.Run("Success", func(t *testing.T) {
		service := &VPCEndpointService{}
		service.Client = fake.NewClientBuilder().WithScheme(scheme()).WithObjects(ipAllocCR).Build()
		wantPath := "/orgs/default/projects/proj-2/vpcs/vpc-2/ip-address-allocations/ipa-1"
		service.IPAllocationService = &fakeIPAllocationService{
			getByOwner: func(metav1.Object) (*model.VpcIpAddressAllocation, error) {
				return &model.VpcIpAddressAllocation{Path: &wantPath}, nil
			},
		}
		path, err := service.resolveIPAllocationPath(context.Background(), ns, name)
		assert.NoError(t, err)
		assert.Equal(t, wantPath, path)
	})
}
