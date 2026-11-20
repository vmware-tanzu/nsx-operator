package subnet

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stderrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func TestPortSettingsNotFoundPreservesMirrorsAndRecovers(t *testing.T) {
	for name, unavailable := range map[string]error{
		"translated SDK":  nsxutil.NewNSXApiError(&model.ApiError{}, stderrors.ErrorType_NOT_FOUND),
		"raw SDK":         *stderrors.NewNotFound(),
		"raw SDK pointer": stderrors.NewNotFound(),
		"wrapped SDK":     fmt.Errorf("list: %w", *stderrors.NewNotFound()),
		"wrapped translated SDK": fmt.Errorf("list: %w",
			nsxutil.NewNSXApiError(&model.ApiError{}, stderrors.ErrorType_NOT_FOUND)),
	} {
		for _, recoverByPolling := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/poll-recovery=%t", name, recoverByPolling), func(t *testing.T) {
				ctx := context.Background()
				f := newSharedPortSettingsCacheFixture(t, 2)
				f.ids = []string{"old"}
				f.reconcileAll(t, nil)
				before := &v1alpha1.SubnetPortSettingList{}
				require.NoError(t, f.r.APIReader.List(ctx, before))
				require.Len(t, before.Items, 2)

				f.r.SubnetService.InvalidatePortSettingsCache("proj:vpc:subnet")
				f.listErr = unavailable
				f.ids = []string{"partial"}
				f.reconcileAll(t, nil)
				assert.Equal(t, 3, f.lists, "unavailable snapshots must not be cached as empty or partial results")
				for _, parent := range f.parents {
					stored := &v1alpha1.Subnet{}
					require.NoError(t, f.r.APIReader.Get(ctx, client.ObjectKeyFromObject(parent), stored))
					require.Len(t, stored.Status.Conditions, 1)
					assert.Equal(t, v1alpha1.Ready, stored.Status.Conditions[0].Type)
					assert.Equal(t, corev1.ConditionTrue, stored.Status.Conditions[0].Status)
				}

				for range 2 {
					f.r.pollAllSharedSubnets()
				}
				assert.Equal(t, 5, f.lists, "404 must not disable subsequent probes")
				assert.Equal(t, 4, f.enqueues, "parent refresh must continue despite PortSettings 404")
				after := &v1alpha1.SubnetPortSettingList{}
				require.NoError(t, f.r.APIReader.List(ctx, after))
				assert.Equal(t, before.Items, after.Items, "404 must preserve mirror data, status and resource version")

				f.listErr = nil
				f.ids = []string{"new"}
				if recoverByPolling {
					f.r.pollAllSharedSubnets()
				}
				f.reconcileAll(t, nil)
				assert.Equal(t, 6, f.lists, "successful recovery must resume discovery and cache the complete snapshot")
				f.assertMirrors(t, f.ids, corev1.ConditionTrue)

				f.ids = nil
				if recoverByPolling {
					f.r.pollAllSharedSubnets()
				} else {
					f.r.SubnetService.InvalidatePortSettingsCache("proj:vpc:subnet")
				}
				f.reconcileAll(t, nil)
				assert.Equal(t, 7, f.lists)
				f.assertMirrors(t, nil, corev1.ConditionTrue)
			})
		}
	}
}

func TestPortSettingsNotFoundWithoutExistingMirrors(t *testing.T) {
	f := newSharedPortSettingsCacheFixture(t, 1)
	f.listErr = *stderrors.NewNotFound()
	f.reconcileAll(t, nil)
	parent := &v1alpha1.Subnet{}
	require.NoError(t, f.r.APIReader.Get(context.Background(), client.ObjectKeyFromObject(f.parents[0]), parent))
	require.Len(t, parent.Status.Conditions, 1)
	assert.Equal(t, v1alpha1.Ready, parent.Status.Conditions[0].Type)
	assert.Equal(t, corev1.ConditionTrue, parent.Status.Conditions[0].Status)
	f.assertMirrors(t, nil, corev1.ConditionTrue)
	f.r.pollAllSharedSubnets()
	assert.Equal(t, 2, f.lists)
	assert.Equal(t, 1, f.enqueues)
	f.listErr = nil
	f.ids = []string{"new"}
	f.r.pollAllSharedSubnets()
	f.assertMirrors(t, f.ids, corev1.ConditionTrue)
}

func TestPortSettingsOtherErrorsRemainFailures(t *testing.T) {
	for name, sourceErr := range map[string]error{
		"server":                 nsxutil.NewNSXApiError(&model.ApiError{}, stderrors.ErrorType_INTERNAL_SERVER_ERROR),
		"forbidden":              nsxutil.NewNSXApiError(&model.ApiError{}, stderrors.ErrorType_UNAUTHORIZED),
		"timeout":                stderrors.NewTimedOut(),
		"404 text":               errors.New("PortSettings List failed with 404 in its message"),
		"error code":             nsxutil.NewNSXApiError(&model.ApiError{ErrorCode: common.Int64(404)}, stderrors.ErrorType_INTERNAL_SERVER_ERROR),
		"transport helper error": nsxutil.CreateResourceNotFound("manager", "list PortSettings"),
	} {
		t.Run(name, func(t *testing.T) {
			f := newSharedPortSettingsCacheFixture(t, 1)
			f.ids = []string{"old"}
			f.reconcileAll(t, nil)
			f.listErr = sourceErr
			f.ids = nil
			f.r.pollAllSharedSubnets()
			f.reconcileAll(t, sourceErr)
			assert.Equal(t, 3, f.lists)
			f.assertMirrors(t, []string{"old"}, corev1.ConditionTrue)
		})
	}
}

func TestPortSettingsNotFoundHandlingDoesNotMaskParentFailures(t *testing.T) {
	for _, failure := range []string{"parent", "status"} {
		t.Run(failure, func(t *testing.T) {
			f := newSharedPortSettingsCacheFixture(t, 1)
			f.r.SubnetPortService.(*subnetport.SubnetPortService).SubnetPortStore = &subnetport.SubnetPortStore{}
			f.ids = []string{"old"}
			f.reconcileAll(t, nil)
			sourceErr := nsxutil.NewNSXApiError(&model.ApiError{}, stderrors.ErrorType_NOT_FOUND)
			if failure == "parent" {
				f.parentErr = sourceErr
			} else {
				f.statusErr = sourceErr
			}
			f.r.pollAllSharedSubnets()
			f.reconcileAll(t, sourceErr)
			assert.Equal(t, 1, f.lists, "required lookup failures must stop before PortSettings List")
			assert.Zero(t, f.enqueues)
			f.assertMirrors(t, []string{"old"}, corev1.ConditionFalse)
			parent := &v1alpha1.Subnet{}
			require.NoError(t, f.r.APIReader.Get(context.Background(), client.ObjectKeyFromObject(f.parents[0]), parent))
			require.Len(t, parent.Status.Conditions, 1)
			assert.Equal(t, corev1.ConditionFalse, parent.Status.Conditions[0].Status)
		})
	}
}
