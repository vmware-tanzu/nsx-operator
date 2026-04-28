/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsxserviceaccount

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	nsxvmwarecomv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxsasvc "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/nsxserviceaccount"
)

type fakeRecorder struct {
}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
}
func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}
func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {

}
func newFakeNSXServiceAccountReconciler() *NSXServiceAccountReconciler {
	scheme := clientgoscheme.Scheme
	nsxvmwarecomv1alpha1.AddToScheme(scheme)
	return &NSXServiceAccountReconciler{
		Client:   fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&nsxvmwarecomv1alpha1.NSXServiceAccount{}).Build(),
		Scheme:   scheme,
		Service:  nil,
		Recorder: fakeRecorder{},
	}
}

func TestNSXServiceAccountReconciler_Reconcile(t *testing.T) {
	deletionTimestamp := &metav1.Time{
		Time: time.Date(1, time.January, 15, 0, 0, 0, 0, time.Local),
	}
	createTimestamp := &metav1.Time{
		Time: time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC),
	}
	type args struct {
		req controllerruntime.Request
	}
	requestArgs := args{
		req: controllerruntime.Request{NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "name"}},
	}
	tests := []struct {
		name        string
		prepareFunc func(*testing.T, *NSXServiceAccountReconciler, context.Context) *gomonkey.Patches
		args        args
		want        controllerruntime.Result
		wantErr     bool
		expectedCR  *nsxvmwarecomv1alpha1.NSXServiceAccount
	}{
		{
			name:        "NotFound",
			prepareFunc: nil,
			args:        requestArgs,
			want:        ResultNormal,
			wantErr:     false,
			expectedCR:  nil,
		},
		{
			name: "NSXVersionCheckFailed",
			prepareFunc: func(t *testing.T, r *NSXServiceAccountReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				patches = gomonkey.ApplyMethodSeq(r.Service.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{false},
					Times:  1,
				}})
				return patches
			},
			args:    requestArgs,
			want:    ResultRequeueAfter5mins,
			wantErr: false,
			expectedCR: &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:       requestArgs.req.Namespace,
					Name:            requestArgs.req.Name,
					ResourceVersion: "2",
				},
				Spec: nsxvmwarecomv1alpha1.NSXServiceAccountSpec{},
				Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
					Phase:  nsxvmwarecomv1alpha1.NSXServiceAccountPhaseFailed,
					Reason: "Error: NSX version check failed, NSXServiceAccount feature is not supported",
					Conditions: []metav1.Condition{
						{
							Type:    nsxvmwarecomv1alpha1.ConditionTypeRealized,
							Status:  metav1.ConditionFalse,
							Reason:  nsxvmwarecomv1alpha1.ConditionReasonRealizationError,
							Message: "Error: NSX version check failed, NSXServiceAccount feature is not supported",
						},
					},
				},
			},
		},
		{
			name: "AddFinalizerFailed",
			prepareFunc: func(t *testing.T, r *NSXServiceAccountReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				patches = gomonkey.ApplyMethodSeq(r.Service.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{true},
					Times:  1,
				}})
				patches.ApplyMethodSeq(r.Client, "Update", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("mock error")},
					Times:  1,
				}, {
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
			args:    requestArgs,
			want:    ResultRequeue,
			wantErr: true,
			expectedCR: &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:       requestArgs.req.Namespace,
					Name:            requestArgs.req.Name,
					ResourceVersion: "2",
					Finalizers:      nil,
				},
				Spec: nsxvmwarecomv1alpha1.NSXServiceAccountSpec{},
				Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
					Phase:  "failed",
					Reason: "Error: mock error",
					Conditions: []metav1.Condition{{
						Type:    nsxvmwarecomv1alpha1.ConditionTypeRealized,
						Status:  metav1.ConditionFalse,
						Reason:  nsxvmwarecomv1alpha1.ConditionReasonRealizationError,
						Message: "Error: mock error",
					}},
				},
			},
		},
		{
			name: "CreateError",
			prepareFunc: func(t *testing.T, r *NSXServiceAccountReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				patches = gomonkey.ApplyMethodSeq(r.Service.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{true},
					Times:  1,
				}})
				patches.ApplyMethodSeq(r.Service, "CreateOrUpdateNSXServiceAccount", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("mock error")},
					Times:  1,
				}})
				return patches
			},
			args:    requestArgs,
			want:    ResultRequeue,
			wantErr: true,
			expectedCR: &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:       requestArgs.req.Namespace,
					Name:            requestArgs.req.Name,
					Finalizers:      []string{servicecommon.NSXServiceAccountFinalizerName},
					ResourceVersion: "3",
				},
				Spec: nsxvmwarecomv1alpha1.NSXServiceAccountSpec{},
				Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
					Phase:  nsxvmwarecomv1alpha1.NSXServiceAccountPhaseFailed,
					Reason: "Error: mock error",
					Conditions: []metav1.Condition{
						{
							Type:    nsxvmwarecomv1alpha1.ConditionTypeRealized,
							Status:  metav1.ConditionFalse,
							Reason:  nsxvmwarecomv1alpha1.ConditionReasonRealizationError,
							Message: "Error: mock error",
						},
					},
				},
			},
		},
		{
			name: "CreateSkip",
			prepareFunc: func(t *testing.T, r *NSXServiceAccountReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						Phase: nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
						Conditions: []metav1.Condition{
							{
								Type:   "Dummy",
								Status: metav1.ConditionFalse,
							},
						},
					},
				}))
				// Service accounts are supported, but restoring realized accounts is not.
				patches = gomonkey.ApplyMethod(r.Service.NSXClient, "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
					return feature == nsx.ServiceAccount
				})
				patches.ApplyMethodSeq(r.Service, "UpdateConnectionInfoIfNeeded", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
			args:    requestArgs,
			want:    ResultNormal,
			wantErr: false,
			expectedCR: &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:       requestArgs.req.Namespace,
					Name:            requestArgs.req.Name,
					Finalizers:      []string{servicecommon.NSXServiceAccountFinalizerName},
					ResourceVersion: "2",
				},
				Spec: nsxvmwarecomv1alpha1.NSXServiceAccountSpec{},
				Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
					Phase: nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
					Conditions: []metav1.Condition{
						{
							Type:   "Dummy",
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
		},
		{
			name: "RefreshNSXManagersAfterRestart",
			prepareFunc: func(t *testing.T, r *NSXServiceAccountReconciler, ctx context.Context) *gomonkey.Patches {
				r.Service.Client = r.Client
				r.Service.NSXConfig.NsxApiManagers = []string{"new-manager-1:443", "new-manager-2:443"}
				require.NoError(t, r.Client.Create(ctx, &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:       requestArgs.req.Name,
						Namespace:  requestArgs.req.Namespace,
						Finalizers: []string{servicecommon.NSXServiceAccountFinalizerName},
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						Phase:       nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
						NSXManagers: []string{"old-manager:443"},
						ClusterID:   "existing-cluster",
						Secrets:     []nsxvmwarecomv1alpha1.NSXSecret{{Name: "existing-secret", Namespace: "ns1"}},
					},
				}))
				patches := gomonkey.ApplyMethodReturn(r.Service.NSXClient, "NSXCheckVersion", true)
				patches.ApplyMethodReturn(r.Service, "RestoreRealizedNSXServiceAccount", nil)
				return patches
			},
			args: requestArgs,
			want: ResultNormal,
			expectedCR: &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:            requestArgs.req.Name,
					Namespace:       requestArgs.req.Namespace,
					Finalizers:      []string{servicecommon.NSXServiceAccountFinalizerName},
					ResourceVersion: "2",
				},
				Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
					Phase:       nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
					NSXManagers: []string{"new-manager-1:443", "new-manager-2:443"},
					ClusterID:   "existing-cluster",
					Secrets:     []nsxvmwarecomv1alpha1.NSXSecret{{Name: "existing-secret", Namespace: "ns1"}},
				},
			},
		},
		{
			name: "RestoreFail",
			prepareFunc: func(t *testing.T, r *NSXServiceAccountReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						Phase: nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
					},
				}))
				// Both service accounts and their restoration are supported.
				patches = gomonkey.ApplyMethod(r.Service.NSXClient, "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
					return feature == nsx.ServiceAccount || feature == nsx.ServiceAccountRestore
				})
				patches.ApplyMethodSeq(r.Service, "RestoreRealizedNSXServiceAccount", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("mock error")},
					Times:  1,
				}})
				return patches
			},
			args:    requestArgs,
			want:    ResultRequeue,
			wantErr: true,
			expectedCR: &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:       requestArgs.req.Namespace,
					Name:            requestArgs.req.Name,
					Finalizers:      []string{servicecommon.NSXServiceAccountFinalizerName},
					ResourceVersion: "2",
				},
				Spec: nsxvmwarecomv1alpha1.NSXServiceAccountSpec{},
				Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
					Phase: nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
				},
			},
		},
		{
			name: "CreateSuccess",
			prepareFunc: func(t *testing.T, r *NSXServiceAccountReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				assert.NoError(t, r.Client.Create(ctx, &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestArgs.req.Namespace,
						Name:      requestArgs.req.Name,
					},
				}))
				patches = gomonkey.ApplyMethodSeq(r.Service.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{true},
					Times:  1,
				}})
				patches.ApplyMethodSeq(r.Service, "CreateOrUpdateNSXServiceAccount", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
			args:    requestArgs,
			want:    ResultNormal,
			wantErr: false,
			expectedCR: &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:       requestArgs.req.Namespace,
					Name:            requestArgs.req.Name,
					Finalizers:      []string{servicecommon.NSXServiceAccountFinalizerName},
					ResourceVersion: "2",
				},
				Spec:   nsxvmwarecomv1alpha1.NSXServiceAccountSpec{},
				Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{},
			},
		},
		{
			name: "DeleteWithoutFinalizer",
			prepareFunc: func(t *testing.T, r *NSXServiceAccountReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				// Fake client strips DeletionTimestamp on Create; wrap Get so Reconcile sees a terminating CR.
				base := fake.NewClientBuilder().WithScheme(r.Scheme).WithStatusSubresource(&nsxvmwarecomv1alpha1.NSXServiceAccount{}).Build()
				obj := &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:         requestArgs.req.Namespace,
						Name:              requestArgs.req.Name,
						CreationTimestamp: *createTimestamp,
					},
				}
				assert.NoError(t, base.Create(ctx, obj))
				wrapped := interceptor.NewClient(base, interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, o client.Object, opts ...client.GetOption) error {
						if err := c.Get(ctx, key, o, opts...); err != nil {
							return err
						}
						if key.Namespace == requestArgs.req.Namespace && key.Name == requestArgs.req.Name {
							if nsxsa, ok := o.(*nsxvmwarecomv1alpha1.NSXServiceAccount); ok {
								ts := deletionTimestamp.DeepCopy()
								nsxsa.SetDeletionTimestamp(ts)
							}
						}
						return nil
					},
				})
				r.Client = wrapped
				patches = gomonkey.ApplyMethodSeq(r.Service.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{true},
					Times:  1,
				}})
				return patches
			},
			args:    requestArgs,
			want:    ResultNormal,
			wantErr: false,
			expectedCR: &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:       requestArgs.req.Namespace,
					Name:            requestArgs.req.Name,
					ResourceVersion: "1",
				},
				Spec:   nsxvmwarecomv1alpha1.NSXServiceAccountSpec{},
				Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{},
			},
		},
		{
			name: "DeleteError",
			prepareFunc: func(t *testing.T, r *NSXServiceAccountReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				obj := &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:  requestArgs.req.Namespace,
						Name:       requestArgs.req.Name,
						Finalizers: []string{servicecommon.NSXServiceAccountFinalizerName},
					},
				}
				assert.NoError(t, r.Client.Create(ctx, obj))
				assert.NoError(t, r.Client.Delete(ctx, obj))
				patches = gomonkey.ApplyMethodSeq(r.Service.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{true},
					Times:  1,
				}})
				patches.ApplyMethodSeq(r.Service, "DeleteNSXServiceAccount", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("mock error")},
					Times:  1,
				}})
				return patches
			},
			args:    requestArgs,
			want:    ResultRequeue,
			wantErr: true,
			expectedCR: &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:       requestArgs.req.Namespace,
					Name:            requestArgs.req.Name,
					Finalizers:      []string{servicecommon.NSXServiceAccountFinalizerName},
					ResourceVersion: "1",
				},
				Spec: nsxvmwarecomv1alpha1.NSXServiceAccountSpec{},
			},
		},
		{
			name: "RemoveFinalizerFailed",
			prepareFunc: func(t *testing.T, r *NSXServiceAccountReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				obj := &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:  requestArgs.req.Namespace,
						Name:       requestArgs.req.Name,
						Finalizers: []string{servicecommon.NSXServiceAccountFinalizerName},
					},
				}
				assert.NoError(t, r.Client.Create(ctx, obj))
				assert.NoError(t, r.Client.Delete(ctx, obj))
				patches = gomonkey.ApplyMethodSeq(r.Service.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{true},
					Times:  1,
				}})
				patches.ApplyMethodSeq(r.Service, "DeleteNSXServiceAccount", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				patches.ApplyMethodSeq(r.Client, "Update", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("mock error")},
					Times:  1,
				}, {
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
			args:    requestArgs,
			want:    ResultRequeue,
			wantErr: true,
			expectedCR: &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:       requestArgs.req.Namespace,
					Name:            requestArgs.req.Name,
					Finalizers:      []string{servicecommon.NSXServiceAccountFinalizerName},
					ResourceVersion: "1",
				},
				Spec: nsxvmwarecomv1alpha1.NSXServiceAccountSpec{},
			},
		},
		{
			name: "DeleteSuccess",
			prepareFunc: func(t *testing.T, r *NSXServiceAccountReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				obj := &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:  requestArgs.req.Namespace,
						Name:       requestArgs.req.Name,
						Finalizers: []string{servicecommon.NSXServiceAccountFinalizerName},
					},
				}
				assert.NoError(t, r.Client.Create(ctx, obj))
				assert.NoError(t, r.Client.Delete(ctx, obj))
				patches = gomonkey.ApplyMethodSeq(r.Service.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{true},
					Times:  1,
				}})
				patches.ApplyMethodSeq(r.Service, "DeleteNSXServiceAccount", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
			args:       requestArgs,
			want:       ResultNormal,
			wantErr:    false,
			expectedCR: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newFakeNSXServiceAccountReconciler()
			nsxvmwarecomv1alpha1.AddToScheme(r.Scheme)
			r.Service = &nsxsasvc.NSXServiceAccountService{
				Service: servicecommon.Service{
					NSXClient: &nsx.Client{},
					NSXConfig: &config.NSXOperatorConfig{
						NsxConfig: &config.NsxConfig{
							EnforcementPoint: "vmc-enforcementpoint",
						},
					},
				},
			}
			r.StatusUpdater = common.NewStatusUpdater(r.Client, r.Service.NSXConfig, r.Recorder, MetricResType, "ServiceAccount", "NSXServiceAccount")
			ctx := context.TODO()
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc(t, r, ctx)
				defer patches.Reset()
			}

			got, err := r.Reconcile(ctx, tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("Reconcile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Reconcile() got = %v, want %v", got, tt.want)
			}
			actualCR := &nsxvmwarecomv1alpha1.NSXServiceAccount{}
			err = r.Client.Get(ctx, tt.args.req.NamespacedName, actualCR)
			if tt.expectedCR == nil {
				assert.True(t, errors.IsNotFound(err))
			} else {
				expectedCR := tt.expectedCR.DeepCopy()
				actualCR.CreationTimestamp = metav1.Time{}
				expectedCR.CreationTimestamp = metav1.Time{}
				switch tt.name {
				case "DeleteError", "RemoveFinalizerFailed", "DeleteWithoutFinalizer":
					expectedCR.DeletionTimestamp = actualCR.DeletionTimestamp
					expectedCR.ResourceVersion = actualCR.ResourceVersion
				}
				assert.Equal(t, expectedCR.ObjectMeta, actualCR.ObjectMeta)
				assert.Equal(t, tt.expectedCR.Spec, actualCR.Spec)
				for i := range actualCR.Status.Conditions {
					actualCR.Status.Conditions[i].LastTransitionTime = metav1.Time{}
				}
				assert.Equal(t, tt.expectedCR.Status, actualCR.Status)
			}
		})
	}
}

func newCapacityTestReconciler() *NSXServiceAccountReconciler {
	r := newFakeNSXServiceAccountReconciler()
	nsxvmwarecomv1alpha1.AddToScheme(r.Scheme)
	r.Service = &nsxsasvc.NSXServiceAccountService{
		Service: servicecommon.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
	}
	r.StatusUpdater = common.NewStatusUpdater(r.Client, r.Service.NSXConfig, r.Recorder, MetricResType, "ServiceAccount", "NSXServiceAccount")
	return r
}

func TestNSXServiceAccountReconciler_Reconcile_CapacityBackoff(t *testing.T) {
	for _, tt := range []struct {
		name       string
		realized   bool
		restoreErr error
	}{
		{name: "PendingAccountDeferred"},
		{name: "RealizedAccountContinues", realized: true},
		{name: "RealizedRestoreDeferred", realized: true, restoreErr: nsxsasvc.CCPBackoffError{RequeueAfter: 5 * time.Minute}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			nsxsasvc.MarkCCPConnectionCapacityFull()
			t.Cleanup(nsxsasvc.ClearCCPConnectionCapacityFull)
			ctx := context.Background()
			r := newCapacityTestReconciler()
			obj := &nsxvmwarecomv1alpha1.NSXServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "account", Namespace: "ns1"}}
			if tt.realized {
				obj.Status.Phase = nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized
			}
			require.NoError(t, r.Client.Create(ctx, obj))
			restoreCalls, connectionCalls := 0, 0
			patches := gomonkey.ApplyMethod(&nsx.Client{}, "NSXCheckVersion", func(_ *nsx.Client, _ int) bool { return true })
			patches.ApplyMethod(r.Service, "CreateOrUpdateNSXServiceAccount", func(_ *nsxsasvc.NSXServiceAccountService, _ context.Context, _ *nsxvmwarecomv1alpha1.NSXServiceAccount) error {
				t.Fatal("creation must be deferred during capacity backoff")
				return nil
			})
			patches.ApplyMethod(r.Service, "RestoreRealizedNSXServiceAccount", func(_ *nsxsasvc.NSXServiceAccountService, _ context.Context, _ *nsxvmwarecomv1alpha1.NSXServiceAccount) error {
				restoreCalls++
				return tt.restoreErr
			})
			patches.ApplyMethod(r.Service, "UpdateConnectionInfoIfNeeded", func(_ *nsxsasvc.NSXServiceAccountService, _ context.Context, _ *nsxvmwarecomv1alpha1.NSXServiceAccount) error {
				connectionCalls++
				return nil
			})
			defer patches.Reset()
			result, err := r.Reconcile(ctx, controllerruntime.Request{NamespacedName: client.ObjectKeyFromObject(obj)})
			require.NoError(t, err)
			if tt.realized && tt.restoreErr == nil {
				assert.Equal(t, ResultNormal, result)
				assert.Equal(t, 1, connectionCalls)
			} else {
				assert.GreaterOrEqual(t, result.RequeueAfter, time.Second)
				assert.LessOrEqual(t, result.RequeueAfter, 5*time.Minute)
				assert.Zero(t, connectionCalls)
			}
			assert.Equal(t, tt.realized, restoreCalls == 1)
			require.NoError(t, r.Client.Get(ctx, client.ObjectKeyFromObject(obj), obj))
			assert.Equal(t, tt.realized, nsxsasvc.IsNSXServiceAccountRealized(&obj.Status))
		})
	}
}

func TestNSXServiceAccountReconciler_RestoreReconcile(t *testing.T) {
	t.Run("Success_OnlyRealizedReconciled", func(t *testing.T) {
		r := newFakeNSXServiceAccountReconciler()
		nsxvmwarecomv1alpha1.AddToScheme(r.Scheme)
		ctx := context.TODO()

		realizedCR1 := &nsxvmwarecomv1alpha1.NSXServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns1",
				Name:      "realized-1",
			},
			Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
				Phase: nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
			},
		}
		realizedCR2 := &nsxvmwarecomv1alpha1.NSXServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns2",
				Name:      "realized-2",
			},
			Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
				Conditions: []metav1.Condition{
					{
						Type:   nsxvmwarecomv1alpha1.ConditionTypeRealized,
						Status: metav1.ConditionTrue,
					},
				},
			},
		}
		unrealizedCR := &nsxvmwarecomv1alpha1.NSXServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns1",
				Name:      "unrealized-1",
			},
			Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
				Phase: nsxvmwarecomv1alpha1.NSXServiceAccountPhaseInProgress,
			},
		}
		require.NoError(t, r.Client.Create(ctx, realizedCR1))
		require.NoError(t, r.Client.Create(ctx, realizedCR2))
		require.NoError(t, r.Client.Create(ctx, unrealizedCR))

		var reconciledNames []string
		patches := gomonkey.ApplyFunc((*NSXServiceAccountReconciler).Reconcile, func(_ *NSXServiceAccountReconciler, _ context.Context, req reconcile.Request) (reconcile.Result, error) {
			reconciledNames = append(reconciledNames, req.Name)
			return ResultNormal, nil
		})
		defer patches.Reset()

		err := r.RestoreReconcile()
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"realized-1", "realized-2"}, reconciledNames)
	})

	t.Run("ListError", func(t *testing.T) {
		r := newFakeNSXServiceAccountReconciler()
		mockCtl := gomock.NewController(t)
		defer mockCtl.Finish()
		k8sClient := mock_client.NewMockClient(mockCtl)
		k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(fmt.Errorf("mock list error"))
		r.Client = k8sClient

		err := r.RestoreReconcile()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get NSXServiceAccount restore list")
	})

	for _, tt := range []struct {
		name   string
		result reconcile.Result
		err    error
	}{
		{"ReconcileError", ResultRequeue, fmt.Errorf("mock reconcile error")},
		{"DeferredReconcile", reconcile.Result{RequeueAfter: time.Minute}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newFakeNSXServiceAccountReconciler()
			nsxvmwarecomv1alpha1.AddToScheme(r.Scheme)
			ctx := context.TODO()

			realizedCR := &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1",
					Name:      "realized-fail",
				},
				Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
					Phase: nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
				},
			}
			require.NoError(t, r.Client.Create(ctx, realizedCR))

			patches := gomonkey.ApplyFunc((*NSXServiceAccountReconciler).Reconcile, func(_ *NSXServiceAccountReconciler, _ context.Context, _ reconcile.Request) (reconcile.Result, error) {
				return tt.result, tt.err
			})
			defer patches.Reset()

			err := r.RestoreReconcile()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "errors found in NSXServiceAccount restore")
			assert.NotContains(t, err.Error(), "%!w")
			if tt.err == nil {
				assert.Contains(t, err.Error(), "reconciliation deferred")
			}
		})
	}
}

func TestNSXServiceAccountReconciler_GarbageCollector(t *testing.T) {
	tagScopeNamespace := servicecommon.TagScopeNamespace
	tagScopeNSXServiceAccountCRName := servicecommon.TagScopeNSXServiceAccountCRName
	tagScopeNSXServiceAccountCRUID := servicecommon.TagScopeNSXServiceAccountCRUID
	tests := []struct {
		name        string
		prepareFunc func(*testing.T, *NSXServiceAccountReconciler, context.Context) *gomonkey.Patches
	}{
		{name: "empty"},
		{
			name: "ListCRError",
			prepareFunc: func(t *testing.T, r *NSXServiceAccountReconciler, ctx context.Context) *gomonkey.Patches {
				namespace2 := "ns2"
				name2 := "name2"
				clusterName2 := "cl1-ns2-name2"
				uid2 := "00000000-0000-0000-0000-000000000002"
				assert.NoError(t, r.Service.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{
					Name: &clusterName2,
					Tags: []mpmodel.Tag{{
						Scope: &tagScopeNamespace,
						Tag:   &namespace2,
					}, {
						Scope: &tagScopeNSXServiceAccountCRName,
						Tag:   &name2,
					}, {
						Scope: &tagScopeNSXServiceAccountCRUID,
						Tag:   &uid2,
					}},
				}))
				return gomonkey.ApplyMethodSeq(r.Client, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("mock error")},
					Times:  1,
				}})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newFakeNSXServiceAccountReconciler()
			r.Service = &nsxsasvc.NSXServiceAccountService{
				Service: servicecommon.Service{
					NSXClient: &nsx.Client{},
					NSXConfig: &config.NSXOperatorConfig{
						NsxConfig: &config.NsxConfig{
							EnforcementPoint: "vmc-enforcementpoint",
						},
					},
				},
			}
			r.StatusUpdater = common.NewStatusUpdater(r.Client, r.Service.NSXConfig, r.Recorder, MetricResType, "ServiceAccount", "NSXServiceAccount")
			r.Service.SetUpStore()
			ctx := context.TODO()
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc(t, r, ctx)
				defer patches.Reset()
			}

			r.CollectGarbage(ctx)
		})
	}
}

type MockManager struct {
	controllerruntime.Manager
	client client.Client
	scheme *runtime.Scheme
}

func (m *MockManager) GetClient() client.Client {
	return m.client
}

func (m *MockManager) GetScheme() *runtime.Scheme {
	return m.scheme
}

func (m *MockManager) GetEventRecorderFor(name string) record.EventRecorder {
	return nil
}

func (m *MockManager) Add(runnable manager.Runnable) error {
	return nil
}

func (m *MockManager) Start(context.Context) error {
	return nil
}

func TestNSXServiceAccountReconciler_StartController(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects().Build()
	service := servicecommon.Service{
		Client: fakeClient,
	}
	mockMgr := &MockManager{scheme: runtime.NewScheme()}
	patches := gomonkey.ApplyFunc(nsxsasvc.InitializeNSXServiceAccount,
		func(service servicecommon.Service) (*nsxsasvc.NSXServiceAccountService, error) {
			return &nsxsasvc.NSXServiceAccountService{Service: service}, nil
		})
	patches.ApplyFunc((*NSXServiceAccountReconciler).setupWithManager, func(r *NSXServiceAccountReconciler, mgr manager.Manager) error {
		return nil
	})
	collectorCalled := make(chan struct{})
	patches.ApplyFunc(common.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context) error) {
		close(collectorCalled)
	})
	defer patches.Reset()
	reconciler := NewNSXServiceAccountReconciler(mockMgr, service)
	err := reconciler.StartController(mockMgr, nil)
	assert.Nil(t, err)
	// StartController launches the collector asynchronously. Keep its patch installed
	// until the goroutine has entered the mock.
	select {
	case <-collectorCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("garbage collector was not started")
	}
}

func TestNSXServiceAccountReconciler_updateNSXServiceAccountStatuswithError(t *testing.T) {
	for _, failure := range []error{nil, fmt.Errorf("test error")} {
		name := "NoErrorLeavesStatusUnchanged"
		if failure != nil {
			name = "ErrorPreservesLatestRealizedFields"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			r := newFakeNSXServiceAccountReconciler()
			nsxvmwarecomv1alpha1.AddToScheme(r.Scheme)
			initial := &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{Name: "name1", Namespace: "ns1", Generation: 2},
				Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
					Phase: nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized, Reason: "Success",
					VPCPath: "latest-vpc", ClusterID: "latest-cluster", ClusterName: "latest-name",
					NSXManagers:      []string{"manager:443"},
					Secrets:          []nsxvmwarecomv1alpha1.NSXSecret{{Name: "cert", Namespace: "ns1"}},
					NSXRestoreStatus: &nsxvmwarecomv1alpha1.NSXRestoreStatus{Status: "SUCCESS"},
					Conditions:       []metav1.Condition{{Type: "Other", Status: metav1.ConditionTrue, Reason: "Preserved"}},
				},
			}
			assert.NoError(t, r.Client.Create(ctx, initial))
			// A stale reconcile must not overwrite status maintained by other operations.
			stale := &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{Name: initial.Name, Namespace: initial.Namespace, Generation: 1},
				Status:     nsxvmwarecomv1alpha1.NSXServiceAccountStatus{ClusterID: "stale-cluster"},
			}
			updateNSXServiceAccountStatuswithError(r.Client, ctx, stale, metav1.Now(), failure)
			actual := &nsxvmwarecomv1alpha1.NSXServiceAccount{}
			assert.NoError(t, r.Client.Get(ctx, client.ObjectKeyFromObject(initial), actual))
			expected := initial.DeepCopy()
			if failure != nil {
				expected.Status.Phase = nsxvmwarecomv1alpha1.NSXServiceAccountPhaseFailed
				expected.Status.Reason = "Error: test error"
				expected.Status.Conditions = nsxsasvc.GenerateNSXServiceAccountConditions(
					expected.Status.Conditions, initial.Generation, metav1.ConditionFalse,
					nsxvmwarecomv1alpha1.ConditionReasonRealizationError, expected.Status.Reason)
			}
			for i := range actual.Status.Conditions {
				actual.Status.Conditions[i].LastTransitionTime = metav1.Time{}
			}
			for i := range expected.Status.Conditions {
				expected.Status.Conditions[i].LastTransitionTime = metav1.Time{}
			}
			assert.Equal(t, expected.Status, actual.Status)
			if failure != nil {
				for i := range stale.Status.Conditions {
					stale.Status.Conditions[i].LastTransitionTime = metav1.Time{}
				}
				assert.Equal(t, actual.Status, stale.Status)
			}
		})
	}
}

func TestNSXServiceAccountReconciler_garbageCollector(t *testing.T) {
	tagScopeNamespace := servicecommon.TagScopeNamespace
	tagScopeNSXServiceAccountCRName := servicecommon.TagScopeNSXServiceAccountCRName
	tagScopeNSXServiceAccountCRUID := servicecommon.TagScopeNSXServiceAccountCRUID
	type args struct {
		nsxServiceAccountUIDSet sets.Set[string]
		nsxServiceAccountList   *nsxvmwarecomv1alpha1.NSXServiceAccountList
	}
	tests := []struct {
		name               string
		prepareFunc        func(*testing.T, *NSXServiceAccountReconciler, context.Context) *gomonkey.Patches
		args               args
		wantGcSuccessCount uint32
		wantGcErrorCount   uint32
	}{
		{
			name: "Delete",
			prepareFunc: func(t *testing.T, r *NSXServiceAccountReconciler, ctx context.Context) *gomonkey.Patches {
				namespace2 := "ns2"
				name2 := "name2"
				clusterName2 := "cl1-ns2-name2"
				uid2 := "00000000-0000-0000-0000-000000000002"
				assert.NoError(t, r.Service.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{
					Name: &clusterName2,
					Tags: []mpmodel.Tag{{
						Scope: &tagScopeNamespace,
						Tag:   &namespace2,
					}, {
						Scope: &tagScopeNSXServiceAccountCRName,
						Tag:   &name2,
					}, {
						Scope: &tagScopeNSXServiceAccountCRUID,
						Tag:   &uid2,
					}},
				}))
				namespace3 := "ns3"
				name3 := "name3"
				clusterName3 := "cl1-ns3-name3"
				uid3 := "00000000-0000-0000-0000-000000000003"
				assert.NoError(t, r.Service.PrincipalIdentityStore.Add(&mpmodel.PrincipalIdentity{
					Name: &clusterName3,
					Tags: []mpmodel.Tag{{
						Scope: &tagScopeNamespace,
						Tag:   &namespace3,
					}, {
						Scope: &tagScopeNSXServiceAccountCRName,
						Tag:   &name3,
					}, {
						Scope: &tagScopeNSXServiceAccountCRUID,
						Tag:   &uid3,
					}},
				}))
				namespace4 := "ns4"
				name4 := "name4"
				clusterName4 := "cl1-ns4-name4"
				uid4 := "00000000-0000-0000-0000-000000000004"
				assert.NoError(t, r.Service.ClusterControlPlaneStore.Add(&model.ClusterControlPlane{
					Id: &clusterName4,
					Tags: []model.Tag{{
						Scope: &tagScopeNamespace,
						Tag:   &namespace4,
					}, {
						Scope: &tagScopeNSXServiceAccountCRName,
						Tag:   &name4,
					}, {
						Scope: &tagScopeNSXServiceAccountCRUID,
						Tag:   &uid4,
					}},
				}))
				count := 0
				return gomonkey.ApplyMethodFunc(r.Service, "DeleteNSXServiceAccount", func(ctx context.Context, namespacedName types.NamespacedName, uid types.UID) error {
					count++
					if count <= 2 {
						switch namespacedName.Namespace {
						case "ns3":
							return nil
						case "ns4":
							return fmt.Errorf("mock error")
						}
					}
					t.Errorf("wrong DeleteNSXServiceAccount call, seq: %d, namespacedName: %v", count, namespacedName)
					return nil
				})
			},
			args: args{
				nsxServiceAccountUIDSet: sets.New[string]("00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000003",
					"00000000-0000-0000-0000-000000000004"),
				nsxServiceAccountList: &nsxvmwarecomv1alpha1.NSXServiceAccountList{Items: []nsxvmwarecomv1alpha1.NSXServiceAccount{{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:  "ns1",
						Name:       "name1",
						UID:        "00000000-0000-0000-0000-000000000001",
						Finalizers: []string{servicecommon.NSXServiceAccountFinalizerName},
					},
				}, {
					ObjectMeta: metav1.ObjectMeta{
						Namespace:  "ns2",
						Name:       "name2",
						UID:        "00000000-0000-0000-0000-000000000002",
						Finalizers: []string{servicecommon.NSXServiceAccountFinalizerName},
					},
				}}},
			},
			wantGcSuccessCount: 1,
			wantGcErrorCount:   1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newFakeNSXServiceAccountReconciler()
			r.Service = &nsxsasvc.NSXServiceAccountService{
				Service: servicecommon.Service{
					NSXClient: &nsx.Client{},
					NSXConfig: &config.NSXOperatorConfig{
						NsxConfig: &config.NsxConfig{
							EnforcementPoint: "vmc-enforcementpoint",
						},
					},
				},
			}
			r.StatusUpdater = common.NewStatusUpdater(r.Client, r.Service.NSXConfig, r.Recorder, MetricResType, "ServiceAccount", "NSXServiceAccount")
			r.Service.SetUpStore()
			ctx := context.TODO()
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc(t, r, ctx)
				defer patches.Reset()
			}

			gotGcSuccessCount, gotGcErrorCount := r.garbageCollector(tt.args.nsxServiceAccountUIDSet, tt.args.nsxServiceAccountList)
			if gotGcSuccessCount != tt.wantGcSuccessCount {
				t.Errorf("garbageCollector() gotGcSuccessCount = %v, want %v", gotGcSuccessCount, tt.wantGcSuccessCount)
			}
			if gotGcErrorCount != tt.wantGcErrorCount {
				t.Errorf("garbageCollector() gotGcErrorCount = %v, want %v", gotGcErrorCount, tt.wantGcErrorCount)
			}
		})
	}
}

func TestNSXServiceAccountReconciler_validateRealized(t *testing.T) {
	type args struct {
		count                 uint16
		ca                    []byte
		nsxServiceAccountList *nsxvmwarecomv1alpha1.NSXServiceAccountList
	}
	tests := []struct {
		name        string
		prepareFunc func(*testing.T, *NSXServiceAccountReconciler) *gomonkey.Patches
		args        args
		want        uint16
		want1       []byte
	}{
		{
			name:        "skip",
			prepareFunc: nil,
			args: args{
				count:                 1,
				ca:                    nil,
				nsxServiceAccountList: nil,
			},
			want:  2,
			want1: nil,
		},
		{
			name:        "last",
			prepareFunc: nil,
			args: args{
				count:                 719,
				ca:                    nil,
				nsxServiceAccountList: nil,
			},
			want:  0,
			want1: nil,
		},
		{
			name: "validate",
			prepareFunc: func(t *testing.T, r *NSXServiceAccountReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(r.Service, "ValidateAndUpdateRealizedNSXServiceAccount", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("mock error")},
					Times:  1,
				}})
				patches.ApplyMethodSeq(r.Service.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{false},
					Times:  1,
				}})
				return patches
			},
			args: args{
				count: 0,
				ca:    []byte("fakeCA"),
				nsxServiceAccountList: &nsxvmwarecomv1alpha1.NSXServiceAccountList{
					Items: []nsxvmwarecomv1alpha1.NSXServiceAccount{{
						ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "name1"},
						Status:     nsxvmwarecomv1alpha1.NSXServiceAccountStatus{Phase: nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized},
					}, {
						ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "name2"},
						Status:     nsxvmwarecomv1alpha1.NSXServiceAccountStatus{},
					}},
				},
			},
			want:  0,
			want1: nil,
		},
		{
			name: "error getting nsxRestoreStatus",
			prepareFunc: func(t *testing.T, r *NSXServiceAccountReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(r.Service, "ValidateAndUpdateRealizedNSXServiceAccount", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				patches.ApplyMethodSeq(r.Service.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{true},
					Times:  1,
				}})
				patches.ApplyMethodSeq(r.Service, "GetNSXRestoreStatus", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						nil,
						fmt.Errorf("fake getNSXRestoreStatus error"),
					},
					Times: 1,
				}})
				return patches
			},
			args: args{
				count: 0,
				ca:    []byte("fakeCA"),
				nsxServiceAccountList: &nsxvmwarecomv1alpha1.NSXServiceAccountList{
					Items: []nsxvmwarecomv1alpha1.NSXServiceAccount{{
						ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "name1"},
						Status:     nsxvmwarecomv1alpha1.NSXServiceAccountStatus{Phase: nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized},
					}, {
						ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "name2"},
						Status:     nsxvmwarecomv1alpha1.NSXServiceAccountStatus{},
					}},
				},
			},
			want:  0,
			want1: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &NSXServiceAccountReconciler{
				Service: &nsxsasvc.NSXServiceAccountService{
					Service: servicecommon.Service{
						NSXClient: &nsx.Client{},
					},
				},
			}
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc(t, r)
				defer patches.Reset()
			}
			got, got1 := r.validateRealized(tt.args.count, tt.args.ca, tt.args.nsxServiceAccountList)
			assert.Equalf(t, tt.want, got, "validateRealized(%v, %v, %v)", tt.args.count, tt.args.ca, tt.args.nsxServiceAccountList)
			assert.Equalf(t, tt.want1, got1, "validateRealized(%v, %v, %v)", tt.args.count, tt.args.ca, tt.args.nsxServiceAccountList)
		})
	}
}

func TestNSXServiceAccountReconciler_serviceMapFunc(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	r := &NSXServiceAccountReconciler{
		Client: k8sClient,
	}
	nsxServiceAccountList := &nsxvmwarecomv1alpha1.NSXServiceAccountList{}
	k8sClient.EXPECT().List(gomock.Any(), nsxServiceAccountList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*nsxvmwarecomv1alpha1.NSXServiceAccountList)
		a.Items = append(a.Items, nsxvmwarecomv1alpha1.NSXServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns-1",
				Name:      "nsxsa-1",
			},
		},
			nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns-2",
					Name:      "nsxsa-2",
				},
			})
		return nil
	})
	requests := r.serviceMapFunc(context.TODO(), nil)
	assert.Equal(t, 2, len(requests))
	assert.Equal(t, []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name:      "nsxsa-1",
				Namespace: "ns-1",
			},
		},
		{
			NamespacedName: types.NamespacedName{
				Name:      "nsxsa-2",
				Namespace: "ns-2",
			},
		},
	}, requests)
}

func TestCapacityBackoffAfterRestartKeepsRealizedAccountUsable(t *testing.T) {
	// Restart loses the old in-memory mark; the first rejected creation relearns it.
	nsxsasvc.MarkCCPConnectionCapacityFull()
	nsxsasvc.ClearCCPConnectionCapacityFull()
	t.Cleanup(nsxsasvc.ClearCCPConnectionCapacityFull)
	r := newFakeNSXServiceAccountReconciler()
	r.Service = &nsxsasvc.NSXServiceAccountService{Service: servicecommon.Service{
		NSXClient: &nsx.Client{}, NSXConfig: &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}},
	}}
	r.StatusUpdater = common.NewStatusUpdater(r.Client, r.Service.NSXConfig, r.Recorder, MetricResType, "ServiceAccount", "NSXServiceAccount")
	ctx := context.Background()
	a := &nsxvmwarecomv1alpha1.NSXServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-a", Namespace: "ns1"},
		Status:     nsxvmwarecomv1alpha1.NSXServiceAccountStatus{Phase: nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized},
	}
	b := &nsxvmwarecomv1alpha1.NSXServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "cluster-b", Namespace: "ns1"}}
	require.NoError(t, r.Client.Create(ctx, a))
	require.NoError(t, r.Client.Create(ctx, b))
	creationCalls, restoreCalls := 0, 0
	patches := gomonkey.ApplyMethod(&nsx.Client{}, "NSXCheckVersion", func(_ *nsx.Client, _ int) bool { return true })
	patches.ApplyMethod(r.Service, "CreateOrUpdateNSXServiceAccount", func(_ *nsxsasvc.NSXServiceAccountService, _ context.Context, _ *nsxvmwarecomv1alpha1.NSXServiceAccount) error {
		creationCalls++
		nsxsasvc.MarkCCPConnectionCapacityFull()
		return fmt.Errorf("NSX capacity full")
	})
	patches.ApplyMethod(r.Service, "RestoreRealizedNSXServiceAccount", func(_ *nsxsasvc.NSXServiceAccountService, _ context.Context, _ *nsxvmwarecomv1alpha1.NSXServiceAccount) error {
		restoreCalls++
		return nil
	})
	patches.ApplyMethod(r.Service, "UpdateConnectionInfoIfNeeded", func(_ *nsxsasvc.NSXServiceAccountService, _ context.Context, _ *nsxvmwarecomv1alpha1.NSXServiceAccount) error {
		return nil
	})
	defer patches.Reset()

	_, err := r.Reconcile(ctx, controllerruntime.Request{NamespacedName: client.ObjectKeyFromObject(b)})
	require.Error(t, err)
	result, err := r.Reconcile(ctx, controllerruntime.Request{NamespacedName: client.ObjectKeyFromObject(a)})
	require.NoError(t, err)
	assert.Equal(t, ResultNormal, result)
	assert.Equal(t, 1, restoreCalls)
	result, err = r.Reconcile(ctx, controllerruntime.Request{NamespacedName: client.ObjectKeyFromObject(b)})
	require.NoError(t, err)
	assert.Greater(t, result.RequeueAfter, time.Duration(0))
	assert.Equal(t, 1, creationCalls, "B must not send another creation request during cooldown")
	require.NoError(t, r.Client.Get(ctx, client.ObjectKeyFromObject(a), a))
	require.NoError(t, r.Client.Get(ctx, client.ObjectKeyFromObject(b), b))
	assert.True(t, nsxsasvc.IsNSXServiceAccountRealized(&a.Status))
	assert.False(t, nsxsasvc.IsNSXServiceAccountRealized(&b.Status))
}
