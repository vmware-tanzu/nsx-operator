/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package clean

import (
	"context"
	"errors"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func TestT1SecurityPolicyCleanerRegistersOnlyForInfraCleanup(t *testing.T) {
	cleaner := &t1SecurityPolicyCleaner{service: &securitypolicy.SecurityPolicyService{}}

	_, isInfraCleaner := interface{}(cleaner).(infraCleaner)
	_, isVPCPreCleaner := interface{}(cleaner).(vpcPreCleaner)
	_, isVPCChildrenCleaner := interface{}(cleaner).(vpcChildrenCleaner)
	assert.True(t, isInfraCleaner)
	assert.False(t, isVPCPreCleaner)
	assert.False(t, isVPCChildrenCleaner)

	cleanupService := NewCleanupService().AddCleanupService(func() (interface{}, error) {
		return cleaner, nil
	})
	assert.Len(t, cleanupService.infraCleaners, 1)
	assert.Empty(t, cleanupService.vpcPreCleaners)
	assert.Empty(t, cleanupService.vpcChildrenCleaners)
}

func TestT1SecurityPolicyCleanerDeletesEveryCachedPolicyByUID(t *testing.T) {
	service := &securitypolicy.SecurityPolicyService{VPCMode: false}
	service.SetUpStoreForTest(common.TagScopeSecurityPolicyCRUID, false)
	for _, policy := range []struct {
		id  string
		uid string
	}{
		{id: "nsx-policy-c", uid: "uid-c"},
		{id: "nsx-policy-b", uid: "uid-b"},
		{id: "nsx-policy-a", uid: "uid-a"},
	} {
		require.NoError(t, service.GetSecurityPolicyStoreForTest().Apply(&model.SecurityPolicy{
			Id: policyString(policy.id),
			Tags: []model.Tag{{
				Scope: policyString(common.TagScopeSecurityPolicyCRUID),
				Tag:   policyString(policy.uid),
			}},
		}))
	}

	expectedDeleteErrorA := errors.New("delete A failed")
	expectedDeleteErrorC := errors.New("delete C failed")
	deletedUIDs := make([]types.UID, 0, 3)
	patch := gomonkey.ApplyMethodFunc(service, "DeleteSecurityPolicy", func(uid types.UID, isGC bool, createdFor string) error {
		assert.True(t, isGC)
		assert.Equal(t, common.ResourceTypeSecurityPolicy, createdFor)
		deletedUIDs = append(deletedUIDs, uid)
		switch uid {
		case "uid-a":
			return expectedDeleteErrorA
		case "uid-c":
			return expectedDeleteErrorC
		}
		return nil
	})
	defer patch.Reset()

	err := (&t1SecurityPolicyCleaner{service: service}).CleanupInfraResources(context.Background())
	require.ErrorIs(t, err, expectedDeleteErrorA)
	require.ErrorIs(t, err, expectedDeleteErrorC)
	assert.Contains(t, err.Error(), `failed to clean up T1 SecurityPolicy "uid-a"`)
	assert.Contains(t, err.Error(), `failed to clean up T1 SecurityPolicy "uid-c"`)
	assert.Equal(t, []types.UID{"uid-a", "uid-b", "uid-c"}, deletedUIDs)
}

func TestT1SecurityPolicyCleanerStopsWhenContextIsCanceled(t *testing.T) {
	service := &securitypolicy.SecurityPolicyService{VPCMode: false}
	service.SetUpStoreForTest(common.TagScopeSecurityPolicyCRUID, false)
	require.NoError(t, service.GetSecurityPolicyStoreForTest().Apply(&model.SecurityPolicy{
		Id: policyString("nsx-policy"),
		Tags: []model.Tag{{
			Scope: policyString(common.TagScopeSecurityPolicyCRUID),
			Tag:   policyString("policy-uid"),
		}},
	}))

	deleteCalled := false
	patch := gomonkey.ApplyMethodFunc(service, "DeleteSecurityPolicy", func(uid types.UID, isGC bool, createdFor string) error {
		deleteCalled = true
		return nil
	})
	defer patch.Reset()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (&t1SecurityPolicyCleaner{service: service}).CleanupInfraResources(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, nsxutil.TimeoutFailed)
	assert.False(t, deleteCalled)
}

func policyString(value string) *string {
	return &value
}

type infraCleanerFunc func(context.Context) error

func (cleaner infraCleanerFunc) CleanupInfraResources(ctx context.Context) error {
	return cleaner(ctx)
}

func TestCleanupInfraResourcesJoinsConcurrentErrors(t *testing.T) {
	expectedErrorA := errors.New("infra cleaner A failed")
	expectedErrorB := errors.New("infra cleaner B failed")
	cleanupService := NewCleanupService()
	cleanupService.infraCleaners = []infraCleaner{
		infraCleanerFunc(func(context.Context) error {
			return errors.Join(nsxutil.TimeoutFailed, expectedErrorA)
		}),
		infraCleanerFunc(func(context.Context) error {
			return errors.Join(nsxutil.TimeoutFailed, expectedErrorB)
		}),
	}

	err := cleanupService.cleanupInfraResources(context.Background())
	require.ErrorIs(t, err, expectedErrorA)
	require.ErrorIs(t, err, expectedErrorB)
}
