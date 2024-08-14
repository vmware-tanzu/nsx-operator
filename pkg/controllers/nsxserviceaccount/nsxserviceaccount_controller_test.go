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
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	nsxvmwarecomv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/nsxserviceaccount"
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
				cluster := &nsx.Cluster{}
				patches = gomonkey.ApplyMethod(reflect.TypeOf(cluster), "GetVersion", func(_ *nsx.Cluster) (*nsx.NsxVersion, error) {
					nsxVersion := &nsx.NsxVersion{NodeVersion: "4.0.1"}
					return nsxVersion, nil
				})
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
				cluster := &nsx.Cluster{}
				patches = gomonkey.ApplyMethod(reflect.TypeOf(cluster), "GetVersion", func(_ *nsx.Cluster) (*nsx.NsxVersion, error) {
					nsxVersion := &nsx.NsxVersion{NodeVersion: "4.1.2"}
					return nsxVersion, nil
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
					ResourceVersion: "3",
				},
				Spec:   nsxvmwarecomv1alpha1.NSXServiceAccountSpec{},
				Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{},
			},
		},
		{
			name: "DeleteWithoutFinalizer",
			prepareFunc: func(t *testing.T, r *NSXServiceAccountReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				obj := &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:         requestArgs.req.Namespace,
						Name:              requestArgs.req.Name,
						CreationTimestamp: *createTimestamp,
					},
				}
				assert.NoError(t, r.Client.Create(ctx, obj))
				patches = gomonkey.ApplyMethodSeq(r.Service.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{true},
					Times:  1,
				}})
				patches.ApplyMethod(obj.DeletionTimestamp, "IsZero", func() bool { return false })
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
						Namespace:         requestArgs.req.Namespace,
						Name:              requestArgs.req.Name,
						DeletionTimestamp: deletionTimestamp,
						Finalizers:        []string{servicecommon.NSXServiceAccountFinalizerName},
					},
				}
				assert.NoError(t, r.Client.Create(ctx, obj))
				patches = gomonkey.ApplyMethodSeq(r.Service.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{true},
					Times:  1,
				}})
				patches.ApplyMethodSeq(r.Service, "DeleteNSXServiceAccount", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("mock error")},
					Times:  1,
				}})
				patches.ApplyMethod(obj.DeletionTimestamp, "IsZero", func() bool { return false })
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
			name: "RemoveFinalizerFailed",
			prepareFunc: func(t *testing.T, r *NSXServiceAccountReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				obj := &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:         requestArgs.req.Namespace,
						Name:              requestArgs.req.Name,
						DeletionTimestamp: deletionTimestamp,
						Finalizers:        []string{servicecommon.NSXServiceAccountFinalizerName},
					},
				}
				assert.NoError(t, r.Client.Create(ctx, obj))
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
				patches.ApplyMethod(obj.DeletionTimestamp, "IsZero", func() bool { return false })
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
					Phase:  "failed",
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
			name: "DeleteSuccess",
			prepareFunc: func(t *testing.T, r *NSXServiceAccountReconciler, ctx context.Context) (patches *gomonkey.Patches) {
				obj := &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:         requestArgs.req.Namespace,
						Name:              requestArgs.req.Name,
						DeletionTimestamp: deletionTimestamp,
						Finalizers:        []string{servicecommon.NSXServiceAccountFinalizerName},
					},
				}
				assert.NoError(t, r.Client.Create(ctx, obj))
				patches = gomonkey.ApplyMethodSeq(r.Service.NSXClient, "NSXCheckVersion", []gomonkey.OutputCell{{
					Values: gomonkey.Params{true},
					Times:  1,
				}})
				patches.ApplyMethodSeq(r.Service, "DeleteNSXServiceAccount", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				patches.ApplyMethod(obj.DeletionTimestamp, "IsZero", func() bool { return false })
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
			r.Service = &nsxserviceaccount.NSXServiceAccountService{
				Service: servicecommon.Service{
					NSXClient: &nsx.Client{},
					NSXConfig: &config.NSXOperatorConfig{
						NsxConfig: &config.NsxConfig{
							EnforcementPoint: "vmc-enforcementpoint",
						},
					},
				},
			}
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
			fmt.Printf("err: %+v", err)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Reconcile() got = %v, want %v", got, tt.want)
			}
			actualCR := &nsxvmwarecomv1alpha1.NSXServiceAccount{}
			err = r.Client.Get(ctx, tt.args.req.NamespacedName, actualCR)
			if tt.expectedCR == nil {
				assert.True(t, errors.IsNotFound(err))
			} else {
				actualCR.CreationTimestamp = metav1.Time{}
				assert.Equal(t, tt.expectedCR.ObjectMeta, actualCR.ObjectMeta)
				assert.Equal(t, tt.expectedCR.Spec, actualCR.Spec)
				for i := range actualCR.Status.Conditions {
					actualCR.Status.Conditions[i].LastTransitionTime = metav1.Time{}
				}
				assert.Equal(t, tt.expectedCR.Status, actualCR.Status)
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
			r.Service = &nsxserviceaccount.NSXServiceAccountService{
				Service: servicecommon.Service{
					NSXClient: &nsx.Client{},
					NSXConfig: &config.NSXOperatorConfig{
						NsxConfig: &config.NsxConfig{
							EnforcementPoint: "vmc-enforcementpoint",
						},
					},
				},
			}
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

func TestNSXServiceAccountReconciler_Start(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	service := &nsxserviceaccount.NSXServiceAccountService{}
	r := &NSXServiceAccountReconciler{
		Client:  k8sClient,
		Scheme:  nil,
		Service: service,
	}
	assert.Error(t, r.Start(nil))
}

func TestNSXServiceAccountReconciler_updateNSXServiceAccountStatus(t *testing.T) {
	ctx := context.TODO()
	err := fmt.Errorf("test error")
	type args struct {
		ctx *context.Context
		o   *nsxvmwarecomv1alpha1.NSXServiceAccount
		e   *error
	}
	tests := []struct {
		name     string
		initial  args
		args     args
		expected args
	}{
		{
			name: "success",
			initial: args{
				o: &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
					},
				},
			},
			args: args{
				ctx: &ctx,
				o: &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "name1",
						Namespace:       "ns1",
						ResourceVersion: "1",
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						Phase:  nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
						Reason: "testReason",
						Conditions: []metav1.Condition{
							{
								Type:    nsxvmwarecomv1alpha1.ConditionTypeRealized,
								Status:  metav1.ConditionTrue,
								Reason:  nsxvmwarecomv1alpha1.ConditionReasonRealizationSuccess,
								Message: "testReason",
							},
						},
						VPCPath:        "testVPCPath",
						NSXManagers:    []string{"dummyHost:443"},
						ProxyEndpoints: nsxvmwarecomv1alpha1.NSXProxyEndpoint{},
						ClusterID:      "testClusterID",
						ClusterName:    "testClusterName",
						Secrets: []nsxvmwarecomv1alpha1.NSXSecret{{
							Name:      "testSecret",
							Namespace: "ns1",
						}},
					},
				},
				e: nil,
			},
			expected: args{
				o: &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "name1",
						Namespace:       "ns1",
						ResourceVersion: "2",
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						Phase:  nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
						Reason: "testReason",
						Conditions: []metav1.Condition{
							{
								Type:    nsxvmwarecomv1alpha1.ConditionTypeRealized,
								Status:  metav1.ConditionTrue,
								Reason:  nsxvmwarecomv1alpha1.ConditionReasonRealizationSuccess,
								Message: "testReason",
							},
						},
						VPCPath:        "testVPCPath",
						NSXManagers:    []string{"dummyHost:443"},
						ProxyEndpoints: nsxvmwarecomv1alpha1.NSXProxyEndpoint{},
						ClusterID:      "testClusterID",
						ClusterName:    "testClusterName",
						Secrets: []nsxvmwarecomv1alpha1.NSXSecret{{
							Name:      "testSecret",
							Namespace: "ns1",
						}},
					},
				},
				e: nil,
			},
		},
		{
			name: "error",
			initial: args{
				o: &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name1",
						Namespace: "ns1",
					},
				},
			},
			args: args{
				ctx: &ctx,
				o: &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "name1",
						Namespace:       "ns1",
						ResourceVersion: "1",
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						VPCPath:        "testVPCPath",
						NSXManagers:    []string{"dummyHost:443"},
						ProxyEndpoints: nsxvmwarecomv1alpha1.NSXProxyEndpoint{},
						ClusterID:      "testClusterID",
						ClusterName:    "testClusterName",
						Secrets: []nsxvmwarecomv1alpha1.NSXSecret{{
							Name:      "testSecret",
							Namespace: "ns1",
						}},
					},
				},
				e: &err,
			},
			expected: args{
				o: &nsxvmwarecomv1alpha1.NSXServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "name1",
						Namespace:       "ns1",
						ResourceVersion: "2",
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						Phase:  nsxvmwarecomv1alpha1.NSXServiceAccountPhaseFailed,
						Reason: "Error: test error",
						Conditions: []metav1.Condition{
							{
								Type:    nsxvmwarecomv1alpha1.ConditionTypeRealized,
								Status:  metav1.ConditionFalse,
								Reason:  nsxvmwarecomv1alpha1.ConditionReasonRealizationError,
								Message: "Error: test error",
							},
						},
						VPCPath:        "testVPCPath",
						NSXManagers:    []string{"dummyHost:443"},
						ProxyEndpoints: nsxvmwarecomv1alpha1.NSXProxyEndpoint{},
						ClusterID:      "testClusterID",
						ClusterName:    "testClusterName",
						Secrets: []nsxvmwarecomv1alpha1.NSXSecret{{
							Name:      "testSecret",
							Namespace: "ns1",
						}},
					},
				},
				e: nil,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newFakeNSXServiceAccountReconciler()
			nsxvmwarecomv1alpha1.AddToScheme(r.Scheme)
			assert.NoError(t, r.Client.Create(ctx, tt.initial.o))

			r.updateNSXServiceAccountStatus(tt.args.ctx, tt.args.o, tt.args.e)
			actualNSXServiceAccount := &nsxvmwarecomv1alpha1.NSXServiceAccount{}
			assert.NoError(t, r.Client.Get(ctx, types.NamespacedName{
				Namespace: tt.args.o.Namespace,
				Name:      tt.args.o.Name,
			}, actualNSXServiceAccount))
			assert.Equal(t, tt.expected.o.ObjectMeta, actualNSXServiceAccount.ObjectMeta)
			assert.Equal(t, tt.expected.o.Spec, actualNSXServiceAccount.Spec)
			for i := range actualNSXServiceAccount.Status.Conditions {
				actualNSXServiceAccount.Status.Conditions[i].LastTransitionTime = metav1.Time{}
			}
			assert.Equal(t, tt.expected.o.Status, actualNSXServiceAccount.Status)
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
						if namespacedName.Namespace == "ns3" {
							return nil
						} else if namespacedName.Namespace == "ns4" {
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
			r.Service = &nsxserviceaccount.NSXServiceAccountService{
				Service: servicecommon.Service{
					NSXClient: &nsx.Client{},
					NSXConfig: &config.NSXOperatorConfig{
						NsxConfig: &config.NsxConfig{
							EnforcementPoint: "vmc-enforcementpoint",
						},
					},
				},
			}
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
			want:  1,
			want1: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &NSXServiceAccountReconciler{
				Service: &nsxserviceaccount.NSXServiceAccountService{},
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
