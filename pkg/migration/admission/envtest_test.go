/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package admission

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	webhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

// This test uses the actual CRDs and webhook configuration. Set
// KUBEBUILDER_ASSETS to Kubernetes 1.33 binaries to run it locally or in CI.
func TestTransitionValidatorOnAPIServer(t *testing.T) {
	assetsDir := os.Getenv("KUBEBUILDER_ASSETS")
	if assetsDir == "" {
		t.Skip("KUBEBUILDER_ASSETS with Kubernetes 1.33 is required")
	}
	assetRoot, err := os.OpenRoot(assetsDir)
	if err != nil {
		t.Skipf("KUBEBUILDER_ASSETS=%q is unavailable: %v", assetsDir, err)
	}
	t.Cleanup(func() { require.NoError(t, assetRoot.Close()) })
	for _, binary := range []string{"etcd", "kube-apiserver"} {
		if _, err := assetRoot.Stat(binary); err != nil {
			t.Skipf("KUBEBUILDER_ASSETS=%q is missing %s: %v", assetsDir, binary, err)
		}
	}
	abs := func(path string) string { p, err := filepath.Abs(path); require.NoError(t, err); return p }
	env := &envtest.Environment{
		BinaryAssetsDirectory: assetsDir,
		CRDDirectoryPaths: []string{
			abs("../../../build/yaml/crd/migration/migration.network.vmware.com_controlplanenetworktransitions.yaml"),
			abs("../../../build/yaml/crd/migration/migration.network.vmware.com_namespacenetworktransitions.yaml"),
		},
		WebhookInstallOptions: envtest.WebhookInstallOptions{Paths: []string{abs("../../../build/yaml/migration/admission/webhook.yaml")}},
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, env.Stop()) })
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	server := webhook.NewServer(webhook.Options{Host: env.WebhookInstallOptions.LocalServingHost, Port: env.WebhookInstallOptions.LocalServingPort, CertDir: env.WebhookInstallOptions.LocalServingCertDir})
	server.Register(TransitionValidationPath, &webhookadmission.Webhook{Handler: TransitionValidator{Reader: c}})
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Start(ctx) }()
	t.Cleanup(func() { cancel(); require.NoError(t, <-serverDone) })
	require.Eventually(t, func() bool { return server.StartedChecker()(nil) == nil }, 10*time.Second, 50*time.Millisecond)
	for _, stage := range []string{"controlplane", "namespaces"} {
		t.Run(stage, func(t *testing.T) {
			var obj client.Object
			if stage == "controlplane" {
				obj = &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName}, Spec: api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate, SourceMode: "VSPHERE_VDS", TargetMode: "NSX_VPC"}}}
			} else {
				obj = &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "transition-batch"}, Spec: api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate, SourceMode: "VSPHERE_VDS", TargetMode: "NSX_VPC"}, Namespaces: []string{"test-ns"}}}
			}
			// A client cannot create a transition that skips the first action.
			initialExecute := obj.DeepCopyObject().(client.Object)
			switch o := initialExecute.(type) {
			case *api.ControlPlaneNetworkTransition:
				o.Spec.Action = api.ActionExecute
			case *api.NamespaceNetworkTransition:
				o.Spec.Action = api.ActionExecute
			}
			require.ErrorContains(t, c.Create(ctx, initialExecute), "must start with Evaluate")
			require.NoError(t, c.Create(ctx, obj))
			snapshot := func() client.Object {
				v := obj.DeepCopyObject().(client.Object)
				require.NoError(t, c.Get(ctx, types.NamespacedName{Name: obj.GetName()}, v))
				return v
			}
			setAction := func(v client.Object, a api.Action) {
				switch o := v.(type) {
				case *api.ControlPlaneNetworkTransition:
					o.Spec.Action = a
				case *api.NamespaceNetworkTransition:
					o.Spec.Action = a
				}
			}
			setInput := func(v client.Object, value string) {
				switch o := v.(type) {
				case *api.ControlPlaneNetworkTransition:
					o.Spec.NetworkConfiguration = &api.ControlPlaneNetworkConfiguration{ResourceNaming: &api.ControlPlaneResourceNaming{LoadBalancerVPCName: api.NSXResourceName(value)}}
				case *api.NamespaceNetworkTransition:
					o.Spec.NetworkConfiguration = &api.NamespaceNetworkConfiguration{ResourceNaming: &api.NamespaceResourceNaming{Networks: []api.VDNResourceNaming{{VDNName: "test-vdn", VPCName: api.NSXResourceName(value)}}}}
				}
			}
			checkpoint := func(v client.Object, state api.ActionState, gen int64, observed ...int64) {
				observedGeneration := v.GetGeneration()
				if len(observed) > 0 {
					observedGeneration = observed[0]
				}
				s := api.TransitionStatus{LastAction: api.ActionEvaluate, ActionState: state, ActionGeneration: gen, ObservedGeneration: observedGeneration}
				switch o := v.(type) {
				case *api.ControlPlaneNetworkTransition:
					o.Status = &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: s}
				case *api.NamespaceNetworkTransition:
					o.Status = &api.NamespaceNetworkTransitionStatus{TransitionStatus: s}
				}
				require.NoError(t, c.Status().Update(ctx, v))
			}
			first := snapshot()
			checkpoint(first, api.ActionStateSucceeded, first.GetGeneration())
			if stage == "namespaces" {
				infra := &api.ControlPlaneNetworkTransition{}
				require.NoError(t, c.Get(ctx, types.NamespacedName{Name: api.ControlPlaneNetworkTransitionName}, infra))
				completedPhase := infra.Status.Phase
				infra.Status.Phase = api.PhaseEvaluated
				require.NoError(t, c.Status().Update(ctx, infra))
				premature := snapshot()
				setAction(premature, api.ActionExecute)
				require.ErrorContains(t, c.Update(ctx, premature), "Stage 1 infrastructure migration must complete")
				infra = &api.ControlPlaneNetworkTransition{}
				require.NoError(t, c.Get(ctx, types.NamespacedName{Name: api.ControlPlaneNetworkTransitionName}, infra))
				infra.Status.Phase = completedPhase
				require.NoError(t, c.Status().Update(ctx, infra))
			}
			staleObserved := snapshot()
			checkpoint(staleObserved, api.ActionStateSucceeded,
				staleObserved.GetGeneration(), staleObserved.GetGeneration()-1)
			staleExecute := snapshot()
			setAction(staleExecute, api.ActionExecute)
			require.ErrorContains(t, c.Update(ctx, staleExecute),
				"Complete Evaluate successfully")
			changed := snapshot()
			setInput(changed, "first-vpc")
			require.NoError(t, c.Update(ctx, changed))
			current := snapshot()
			require.Greater(t, current.GetGeneration(), first.GetGeneration())
			setAction(current, api.ActionExecute)
			err := c.Update(ctx, current)
			require.ErrorContains(t, err, "Complete Evaluate successfully")
			current = snapshot()
			for _, state := range []api.ActionState{api.ActionStateRunning, api.ActionStateFailed} {
				checkpoint(current, state, current.GetGeneration())
				execute := snapshot()
				setAction(execute, api.ActionExecute)
				require.ErrorContains(t, c.Update(ctx, execute), "Complete Evaluate successfully")
				current = snapshot()
			}
			checkpoint(current, api.ActionStateSucceeded, current.GetGeneration())
			combined := snapshot()
			setAction(combined, api.ActionExecute)
			setInput(combined, "changed-vpc")
			require.Error(t, c.Update(ctx, combined), "CRD must reject Execute plus input changes")
			execute := snapshot()
			setAction(execute, api.ActionExecute)
			require.NoError(t, c.Update(ctx, execute), "the old generation must match the checkpoint, even though Execute increments generation")
			current = snapshot()
			require.Greater(t, current.GetGeneration(), first.GetGeneration()+1)
			current.SetLabels(map[string]string{"test": "unchanged-execute"})
			require.NoError(t, c.Update(ctx, current), "unchanged Execute must not be gated")
			current = snapshot()
			checkpoint(current, api.ActionStateSucceeded, current.GetGeneration()-1)
			// A pending Execute has not completed, so it cannot skip to Execute or
			// be reset to Evaluate to reopen the frozen input configuration. An
			// unchanged Execute request remains idempotently admissible.
			current = snapshot()
			setAction(current, api.ActionExecute)
			require.NoError(t, c.Update(ctx, current), "an unchanged Execute request is idempotent")
			current = snapshot()
			setAction(current, api.ActionEvaluate)
			require.ErrorContains(t, c.Update(ctx, current), "Illegal action change")
			current = snapshot()
			completedExecute := api.TransitionStatus{LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded, ActionGeneration: current.GetGeneration(), ObservedGeneration: current.GetGeneration()}
			switch o := current.(type) {
			case *api.ControlPlaneNetworkTransition:
				o.Status = &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: completedExecute}
			case *api.NamespaceNetworkTransition:
				o.Status = &api.NamespaceNetworkTransitionStatus{TransitionStatus: completedExecute}
			}
			require.NoError(t, c.Status().Update(ctx, current))
			current = snapshot()
			setAction(current, api.ActionExecute)
			require.NoError(t, c.Update(ctx, current), "a completed Execute permits Execute")
			current = snapshot()
			unresolved := api.TransitionStatus{LastAction: api.ActionExecute, ActionState: api.ActionStateFailed, ActionGeneration: current.GetGeneration(), ObservedGeneration: current.GetGeneration(), Phase: "Failed"}
			switch o := current.(type) {
			case *api.ControlPlaneNetworkTransition:
				o.Status = &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: unresolved}
			case *api.NamespaceNetworkTransition:
				o.Status = &api.NamespaceNetworkTransitionStatus{TransitionStatus: unresolved}
			}
			require.NoError(t, c.Status().Update(ctx, current))
			require.ErrorContains(t, c.Delete(ctx, snapshot()), "Resolve the unfinished migration action")
			if stage == "controlplane" {
				// This API-server test shares the reader across both transition kinds.
				// Finish Execute so it does not occupy the global Execute slot needed
				// by the namespace case that follows.
				current = snapshot()
				o := current.(*api.ControlPlaneNetworkTransition)
				o.Status.TransitionStatus = api.TransitionStatus{
					LastAction:         api.ActionExecute,
					ActionState:        api.ActionStateSucceeded,
					ActionGeneration:   o.Generation,
					ObservedGeneration: o.Generation,
					Phase:              api.PhaseInfraTransitionComplete,
				}
				require.NoError(t, c.Status().Update(ctx, o))
			}
		})
	}
}
