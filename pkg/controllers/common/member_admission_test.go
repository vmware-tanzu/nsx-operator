/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package common

import (
	"context"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	pkgmock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestPrecreatedMemberAdmissionUsesLiveProvider(t *testing.T) {
	patches := gomonkey.ApplyFunc(config.IsPerNamespaceProvidersSupported, func() bool { return true })
	defer patches.Reset()
	for _, tc := range []struct {
		name         string
		shared       bool
		cachedShared bool
		mixed        bool
		requestedIP  bool
		ownerFailure bool
		wantErr      string
		wantMember   string
	}{
		{name: "managed member with stale VPC namespace", wantErr: "not eligible"},
		{name: "stale shared annotation cannot admit managed member", cachedShared: true, wantErr: "not eligible"},
		{name: "shared member remains usable", shared: true, wantMember: "member"},
		{name: "skip managed member and use eligible shared member", mixed: true, wantMember: "shared"},
		{name: "rejected matching member cannot fall back outside CIDR", mixed: true, requestedIP: true, wantErr: "not eligible"},
		{name: "live owner read retries without reserving capacity", shared: true, ownerFailure: true, wantMember: "member"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			child := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "child", Annotations: map[string]string{servicecommon.AnnotationSharedVPCNamespace: "owner"}}}
			owner := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "owner", Annotations: map[string]string{servicecommon.AnnotationVPCNetworkConfig: "network"}}}
			member := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "member", Namespace: child.Name}}
			path := "/orgs/default/projects/default/vpcs/vpc/subnets/"
			if tc.shared {
				member.Annotations = map[string]string{servicecommon.AnnotationAssociatedResource: path + member.Name}
			}
			names := []string{member.Name}
			objects := []client.Object{child, owner, member}
			if tc.mixed {
				names = append(names, "shared")
				objects = append(objects, &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: child.Name, Annotations: map[string]string{servicecommon.AnnotationAssociatedResource: path + "shared"}}})
			}
			set := &v1alpha1.SubnetSet{ObjectMeta: metav1.ObjectMeta{Name: "set", Namespace: child.Name, UID: types.UID(t.Name())}, Spec: v1alpha1.SubnetSetSpec{SubnetNames: &names}}
			objects = append(objects, set)
			live := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			// The cache still exposes the previous explicit VPC provider. It may
			// also expose a shared annotation no longer present on the live member.
			cachedChild := child.DeepCopy()
			cachedChild.Annotations[servicecommon.AnnotationVPCNetworkConfig] = "old-network"
			cachedMember := member.DeepCopy()
			if tc.cachedShared {
				cachedMember.Annotations = map[string]string{servicecommon.AnnotationAssociatedResource: path + member.Name}
			}
			cachedObjects := append([]client.Object{cachedChild, owner, cachedMember}, objects[3:]...)
			cached := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cachedObjects...).Build()
			reader := &memberOwnerReader{Reader: live, failOwner: tc.ownerFailure}
			svc := &pkgmock.MockSubnetServiceProvider{}
			for _, name := range names {
				cidr := "192.0.2.0/24"
				if name == "shared" {
					cidr = "198.51.100.0/24"
				}
				backend := &model.VpcSubnet{Id: servicecommon.String(name), Path: servicecommon.String(path + name), IpAddresses: []string{cidr}}
				svc.On("GetSubnetByCR", mock.MatchedBy(func(s *v1alpha1.Subnet) bool { return s.Name == name })).Return(backend, nil).Maybe()
			}
			ports := &pkgmock.MockSubnetPortServiceProvider{}
			var allocated []string
			ports.On("AllocatePortFromSubnet", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				allocated = append(allocated, *args.Get(0).(*model.VpcSubnet).Id)
			}).Return(true, nil).Maybe()
			var bindings []v1alpha1.PortAddressBinding
			if tc.requestedIP {
				bindings = []v1alpha1.PortAddressBinding{{IPAddress: "192.0.2.10"}}
			}
			allocate := func() (string, error) {
				selected, _, uid, lock, err := AllocateSubnetFromSubnetSet(cached, reader, set, &pkgmock.MockVPCServiceProvider{}, svc, ports, v1alpha1.IPAddressTypeIPv4, "", bindings, true)
				if lock != nil {
					RUnlockSubnetSet(*uid, lock)
				}
				stored, ok := SubnetSetLocks.Load(set.UID)
				require.True(t, ok)
				mutex := stored.(*sync.RWMutex)
				require.True(t, mutex.TryLock(), "member admission must not leak the collection lock")
				mutex.Unlock()
				return selected, err
			}
			if tc.ownerFailure {
				selected, err := allocate()
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.Empty(t, selected)
				require.Empty(t, allocated, "unverified member must not reserve capacity")
				reader.failOwner = false
			}
			selected, err := allocate()
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.Empty(t, selected)
				require.Empty(t, allocated)
			} else {
				require.NoError(t, err)
				require.Equal(t, path+tc.wantMember, selected)
				require.Equal(t, []string{tc.wantMember}, allocated)
			}
			svc.AssertNotCalled(t, "CreateOrUpdateSubnet", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

type memberOwnerReader struct {
	client.Reader
	failOwner bool
}

func (r *memberOwnerReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, namespace := obj.(*corev1.Namespace); namespace && key.Name == "owner" && r.failOwner {
		return context.DeadlineExceeded
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}
