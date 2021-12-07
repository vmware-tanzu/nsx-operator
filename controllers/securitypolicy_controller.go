/* Copyright © 2020 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	policyclient "github.com/vmware/vsphere-automation-sdk-go/runtime/protocol/client"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	securitypolicyv1 "github.com/vmware-tanzu/nsx-operator/api/v1"
)

// SecurityPolicyReconciler reconciles a SecurityPolicy object
type SecurityPolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	*policyclient.RestConnector
}

//+kubebuilder:rbac:groups=securitypolicy.github.com,resources=securitypolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=securitypolicy.github.com,resources=securitypolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=securitypolicy.github.com,resources=securitypolicies/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the SecurityPolicy object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile
func (r *SecurityPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	// TODO(user): your logic here

	// 获取当前的 CR，并打印
	obj := &securitypolicyv1.SecurityPolicy{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		print(err, "Unable to fetch object")
	} else {
		print("Geeting from Kubebuilder to", obj.Spec.Foo)
	}

	// 初始化 CR 的 Status 为 Running
	obj.Status.Status = "Running"
	if err := r.Status().Update(ctx, obj); err != nil {
		print(err, "unable to update status")
	}

	queryClient := search.NewQueryClient(r.RestConnector)
	queryParam := "Segment AND tags.scope:ncp/cluster AND tags.tag:k8scl-one"
	queryClient.List(queryParam, nil, nil, nil, nil, nil)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecurityPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&securitypolicyv1.SecurityPolicy{}).
		Complete(r)
}
