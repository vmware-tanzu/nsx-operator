/* Copyright © 2024 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package vpcnetwork

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	svccommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	systemNS = "kube-system"
)

func TestPredictNetworkUpdateEvent(t *testing.T) {
	oldDNS := []string{"1.1.1.1"}
	newDNS := []string{"1.1.1.1", "1.1.1.2"}
	nonSystemNS := "ns1"
	systemNetworkNamespaces = sets.New[string](systemNS)

	for _, tc := range []struct {
		name      string
		oldNet    *v1alpha1.Network
		newNet    *v1alpha1.Network
		expResult bool
	}{
		{
			name:      "Update event on non-default Network",
			oldNet:    createNetwork("non-default", systemNS, v1alpha1.NetworkTypeNSXT, nil, oldDNS),
			newNet:    createNetwork("non-default", systemNS, v1alpha1.NetworkTypeNSXTVPC, nil, newDNS),
			expResult: false,
		},
		{
			name:      "Update default VDS Network with no change on network type",
			oldNet:    createVDSDefaultNetwork("default", systemNS, oldDNS),
			newNet:    createVDSDefaultNetwork("default", systemNS, newDNS),
			expResult: false,
		},
		{
			name:      "Update default VPC Network with no change on network type",
			oldNet:    createVPCDefaultNetwork("default", nonSystemNS, oldDNS),
			newNet:    createVPCDefaultNetwork("default", nonSystemNS, newDNS),
			expResult: false,
		},
		{
			name:      "Update default Network to non-VPC type",
			oldNet:    createVDSDefaultNetwork("default", nonSystemNS, oldDNS),
			newNet:    createDefaultNetwork("default", nonSystemNS, v1alpha1.NetworkTypeNSXT, newDNS),
			expResult: false,
		},
		{
			name:      "Update default Network in a non-system Namespace",
			oldNet:    createVDSDefaultNetwork("default", nonSystemNS, oldDNS),
			newNet:    createVPCDefaultNetwork("default", nonSystemNS, oldDNS),
			expResult: false,
		},
		{
			name:      "Update default Network to VPC in system Namespaces",
			oldNet:    createVDSDefaultNetwork("default", systemNS, oldDNS),
			newNet:    createVPCDefaultNetwork("default", systemNS, oldDNS),
			expResult: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Network create event is ignored.
			createResult := PredicateFuncsByNetwork.Create(event.CreateEvent{
				Object: tc.oldNet,
			})
			assert.False(t, createResult)
			// Network update event is processed.
			updateEvent := event.UpdateEvent{
				ObjectOld: tc.oldNet,
				ObjectNew: tc.newNet,
			}
			updateResult := PredicateFuncsByNetwork.Update(updateEvent)
			assert.Equal(t, tc.expResult, updateResult)
			// Network delete event is ignored.
			deleteResult := PredicateFuncsByNetwork.Delete(event.DeleteEvent{
				Object: tc.newNet,
			})
			assert.False(t, deleteResult)
		})
	}
}

func TestIsDefaultNetwork(t *testing.T) {
	//systemNetworkNamespaces = sets.New[string]()
	for _, tc := range []struct {
		name      string
		network   *v1alpha1.Network
		expResult bool
	}{
		{
			name:      "Network without any labels",
			network:   createNetwork("net1", "ns1", v1alpha1.NetworkTypeNSXTVPC, nil, nil),
			expResult: false,
		},
		{
			name:      "Network without default label key",
			network:   createNetwork("net2", "ns1", v1alpha1.NetworkTypeNSXTVPC, map[string]string{"invalid-key": "true"}, nil),
			expResult: false,
		},
		{
			name:      "Network with default label key and value false",
			network:   createNetwork("net3", "ns1", v1alpha1.NetworkTypeNSXTVPC, map[string]string{defaultNetworkLabelKey: "false"}, nil),
			expResult: false,
		},
		{
			name:      "Network with default label key and value true",
			network:   createNetwork("net4", "ns1", v1alpha1.NetworkTypeNSXTVPC, map[string]string{defaultNetworkLabelKey: defaultNetworkLabelValue}, nil),
			expResult: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actResult := isDefaultNetwork(tc.network)
			assert.Equal(t, tc.expResult, actResult)
		})
	}
}

func TestReconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	reconciler := &NetworkReconciler{Client: k8sClient}
	systemNetworkNamespaces = sets.New[string]()

	ctx := context.Background()
	systemReq := types.NamespacedName{
		Namespace: systemNS,
		Name:      "default-net1",
	}
	nonSystemReq1 := types.NamespacedName{
		Namespace: "ns1",
		Name:      "default",
	}
	nonSystemReq2 := types.NamespacedName{
		Namespace: "ns2",
		Name:      "default",
	}

	for _, tc := range []struct {
		name          string
		existingNSs   map[string]bool
		request       ctrl.Request
		mockFunc      func()
		expErr        string
		expResult     ctrl.Result
		addToSystemNS bool
	}{
		{
			name:    "Error when fetching Network's Namespace ",
			request: ctrl.Request{NamespacedName: systemReq},
			mockFunc: func() {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("failure to get Namespace"))
			},
			expErr:    "failure to get Namespace",
			expResult: resultRequeue,
		},
		{
			name:    "Network's Namespace is annotated with VPC system false",
			request: ctrl.Request{NamespacedName: nonSystemReq1},
			mockFunc: func() {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Do(
					func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
						copyNamespace(obj, createWorkloadNamespace(nonSystemReq1.Namespace))
						return nil
					},
				)
			},
			expResult:     resultNormal,
			addToSystemNS: false,
		},
		{
			name:    "Network's Namespace has no system annotation key",
			request: ctrl.Request{NamespacedName: nonSystemReq2},
			mockFunc: func() {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Do(
					func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
						copyNamespace(obj, createWorkloadNamespace(nonSystemReq2.Namespace))
						return nil
					},
				)
			},
			expResult:     resultNormal,
			addToSystemNS: false,
		},
		{
			name:    "Network is in system Namespace",
			request: ctrl.Request{NamespacedName: systemReq},
			mockFunc: func() {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Do(
					func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
						copyNamespace(obj, createSystemNamespace(systemReq.Namespace))
						return nil
					},
				)
			},
			expResult:     resultNormal,
			addToSystemNS: true,
		},
		{
			name:          "Update Network type in system Namespace without read Namespace",
			request:       ctrl.Request{NamespacedName: systemReq},
			expResult:     resultNormal,
			addToSystemNS: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mockFunc != nil {
				tc.mockFunc()
			}
			result, err := reconciler.Reconcile(ctx, tc.request)
			if tc.expErr != "" {
				assert.EqualError(t, err, tc.expErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.expResult, result)
			if tc.addToSystemNS {
				assert.Contains(t, systemNetworkNamespaces, tc.request.Namespace)
			} else {
				assert.NotContains(t, systemNetworkNamespaces, tc.request.Namespace)
			}
		})
	}
}

func TestIsVPCEnabledOnNamespace(t *testing.T) {
	defer func() {
		systemNetworkNamespaces = nil
	}()

	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	reconciler := &NetworkReconciler{Client: k8sClient}
	netNSForSystem := "kube-system"
	defaultNetworkInSystem := createVPCDefaultNetwork("default", netNSForSystem, nil)
	systemNetworkNamespaces = sets.New[string](netNSForSystem)

	for _, tc := range []struct {
		name       string
		ns         string
		listNs     string
		netListErr error
		isSystemNS bool
		nsGetErr   error
		listNets   []v1alpha1.Network
		expErr     string
		expResult  bool
	}{
		{
			name:      "Error when checking system Namespaces",
			ns:        "ns0",
			nsGetErr:  fmt.Errorf("failed to list Namespaces"),
			expErr:    "failed to list Namespaces",
			expResult: false,
		},
		{
			name:       "Error when listing Network CRs",
			ns:         "ns1",
			listNs:     "ns1",
			netListErr: fmt.Errorf("failed to list Networks in Namespace"),
			expErr:     "failed to list Networks in Namespace",
			expResult:  false,
		},
		{
			name:      "No default network exists in Namespace",
			ns:        "ns2",
			listNs:    "ns2",
			listNets:  []v1alpha1.Network{},
			expErr:    "no default network found in Namespace ns2",
			expResult: false,
		},
		{
			name:   "Default network type is VDS in Namespace ns3",
			ns:     "ns3",
			listNs: "ns3",
			listNets: []v1alpha1.Network{
				*createVDSDefaultNetwork("default-ns3", "ns3", nil),
			},
			expResult: false,
		},
		{
			name:   "Default network type is VPC in Namespace ns4",
			ns:     "ns4",
			listNs: "ns4",
			listNets: []v1alpha1.Network{
				*createVPCDefaultNetwork("default-ns4", "ns4", nil),
			},
			expResult: true,
		},
		{
			name:       "Default system network type is VPC",
			ns:         netNSForSystem,
			isSystemNS: true,
			listNs:     netNSForSystem,
			listNets:   []v1alpha1.Network{*defaultNetworkInSystem},
			expResult:  true,
		},
		{
			name:       "Check VPC from a different system Namespace",
			ns:         "system",
			isSystemNS: true,
			listNs:     netNSForSystem,
			listNets:   []v1alpha1.Network{*defaultNetworkInSystem},
			expResult:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.nsGetErr != nil {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(tc.nsGetErr)
			} else {
				var namespace *corev1.Namespace
				if tc.isSystemNS {
					namespace = createSystemNamespace(tc.ns)
				} else {
					namespace = createWorkloadNamespace(tc.ns)
				}
				k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: tc.ns, Name: tc.ns}, &corev1.Namespace{}).Return(nil).Do(
					func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
						copyNamespace(obj, namespace)
						return nil
					},
				)
				matchingLabels := client.MatchingLabels{defaultNetworkLabelKey: defaultNetworkLabelValue}
				listOptions := []client.ListOption{
					client.InNamespace(tc.listNs), matchingLabels,
				}
				k8sClient.EXPECT().List(gomock.Any(), &v1alpha1.NetworkList{}, listOptions).Return(tc.netListErr).Do(
					func(_ context.Context, obj client.ObjectList, opts ...client.ListOption) error {
						netList := obj.(*v1alpha1.NetworkList)
						netList.Items = tc.listNets
						return tc.netListErr
					},
				)
			}

			result, err := reconciler.IsVPCEnabledOnNamespace(tc.ns)
			if tc.expErr != "" {
				assert.EqualError(t, err, tc.expErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.expResult, result)
		})
	}
}

func TestReconcileWithVPCFilters(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	reconciler := &NetworkReconciler{Client: k8sClient}
	ctx := context.Background()
	var innerHandledRequest types.NamespacedName
	innerHandled := false

	innerfunc := func(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
		innerHandled = true
		innerHandledRequest = req.NamespacedName
		return resultNormal, nil
	}
	for _, tc := range []struct {
		name            string
		request         ctrl.Request
		listErr         error
		listNets        []v1alpha1.Network
		expErr          string
		expResult       ctrl.Result
		expInnerHandled bool
	}{
		{
			name:      "Error when listing Network CRs",
			listErr:   fmt.Errorf("failed to list Networks in Namespace"),
			expErr:    "failed to list Networks in Namespace",
			expResult: common.ResultRequeue,
		},
		{
			name:    "Default network type is VDS",
			request: ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "pod1"}},
			listNets: []v1alpha1.Network{
				{ObjectMeta: v1.ObjectMeta{
					Namespace: "ns1",
					Name:      "default",
					Labels:    map[string]string{defaultNetworkLabelKey: defaultNetworkLabelValue}},
					Spec: v1alpha1.NetworkSpec{
						Type: v1alpha1.NetworkTypeVDS,
					}},
			},
			expResult:       common.ResultNormal,
			expInnerHandled: false,
		},
		{
			name:    "Default network type is VPC",
			request: ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns2", Name: "pod2"}},
			listNets: []v1alpha1.Network{
				{ObjectMeta: v1.ObjectMeta{
					Namespace: "ns2",
					Name:      "default",
					Labels:    map[string]string{defaultNetworkLabelKey: defaultNetworkLabelValue}},
					Spec: v1alpha1.NetworkSpec{
						Type: v1alpha1.NetworkTypeNSXTVPC,
					}},
			},
			expResult:       common.ResultNormal,
			expInnerHandled: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			innerHandled = false
			k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(
				func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					copyNamespace(obj, createWorkloadNamespace(tc.request.Namespace))
					return nil
				},
			)
			k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(tc.listErr).Do(
				func(_ context.Context, obj client.ObjectList, options ...client.ListOption) error {
					netList := obj.(*v1alpha1.NetworkList)
					netList.Items = tc.listNets
					return tc.listErr
				},
			)

			result, err := reconciler.ReconcileWithVPCFilters("test", ctx, tc.request, innerfunc)
			if tc.expErr != "" {
				assert.EqualError(t, err, tc.expErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.expResult, result)
			assert.Equal(t, tc.expInnerHandled, innerHandled)
			if tc.expInnerHandled {
				assert.Equal(t, tc.request.NamespacedName, innerHandledRequest)
			}
		})
	}
}

func TestEnqueueRequestForNetwork(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	nsAdd, nsUpdate, nsDelete, nsGeneric := "add", "update", "delete", "unknown"
	systemNS1 := "system"
	systemNetworkNamespaces = sets.New[string](systemNS)

	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	enquedItems := make([]types.NamespacedName, 0)
	wg := sync.WaitGroup{}
	wg.Add(1)

	// Start a goroutine to get items from the queue.
	go func() {
		defer wg.Done()
		for {
			obj, _ := queue.Get()
			item := obj.(reconcile.Request)
			if item.Namespace == "stop" {
				return
			}
			enquedItems = append(enquedItems, item.NamespacedName)
			queue.Forget(obj)
			queue.Done(obj)
		}
	}()

	ctx := context.Background()
	lister := fakeLister{items: []types.NamespacedName{
		{Namespace: nsAdd, Name: "obj11"},
		{Namespace: nsUpdate, Name: "obj21"},
		{Namespace: nsUpdate, Name: "obj22"},
		{Namespace: nsDelete, Name: "obj31"},
		{Namespace: nsGeneric, Name: "obj41"},
		{Namespace: systemNS, Name: "obj-system"},
		{Namespace: systemNS1, Name: "obj-system1"},
	}}

	enqueueRequest := EnqueueRequestForNetwork{Client: k8sClient, Lister: lister.list}
	// Call create event
	netInAdd := createVPCDefaultNetwork("net1", nsAdd, nil)
	enqueueRequest.Create(ctx, event.CreateEvent{Object: netInAdd}, queue)
	// Call update event
	netInUpdateOld := createVDSDefaultNetwork("net1", nsUpdate, nil)
	netInUpdateNew := createVPCDefaultNetwork("net1", nsUpdate, nil)
	enqueueRequest.Update(ctx, event.UpdateEvent{ObjectOld: netInUpdateOld, ObjectNew: netInUpdateNew}, queue)
	// Call delete event
	netInDelete := createVPCDefaultNetwork("net1", nsDelete, nil)
	enqueueRequest.Delete(ctx, event.DeleteEvent{Object: netInDelete}, queue)
	// Call generic event
	netInGeneric := createVPCDefaultNetwork("net1", nsGeneric, nil)
	enqueueRequest.Generic(ctx, event.GenericEvent{Object: netInGeneric}, queue)

	// Call network update event in system NS
	systemNetUpdateOld := createVDSDefaultNetwork("net1", systemNS, nil)
	systemNetUpdateNew := createVPCDefaultNetwork("net1", systemNS, nil)
	k8sClient.EXPECT().List(gomock.Any(), &corev1.NamespaceList{}).Return(nil).Do(
		func(_ context.Context, obj client.ObjectList, options ...client.ListOption) error {
			nsList := obj.(*corev1.NamespaceList)
			nsList.Items = []corev1.Namespace{
				*createSystemNamespace(systemNS),
				*createSystemNamespace(systemNS1),
				*createWorkloadNamespace(nsAdd),
				*createWorkloadNamespace(nsUpdate),
				*createWorkloadNamespace(nsDelete),
				*createWorkloadNamespace(nsGeneric),
			}
			return nil
		},
	)
	enqueueRequest.Update(ctx, event.UpdateEvent{ObjectOld: systemNetUpdateOld, ObjectNew: systemNetUpdateNew}, queue)
	// Send stop event
	queue.Add(reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "stop"}})

	wg.Wait()
	// Validate only update events are enqueue.
	assert.Equal(t, 4, len(enquedItems))
	assert.Contains(t, enquedItems, types.NamespacedName{Namespace: nsUpdate, Name: "obj21"})
	assert.Contains(t, enquedItems, types.NamespacedName{Namespace: nsUpdate, Name: "obj22"})
	assert.Contains(t, enquedItems, types.NamespacedName{Namespace: systemNS, Name: "obj-system"})
	assert.Contains(t, enquedItems, types.NamespacedName{Namespace: systemNS1, Name: "obj-system1"})
	queue.ShutDown()
}

func TestWebhookHandle(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	reconciler := &NetworkReconciler{Client: k8sClient}

	vpcWlNS := "ns1"
	vdsWlNS := "ns2"
	vpcNetwork := createVPCDefaultNetwork("net1", vpcWlNS, nil)
	vdsNetwork := createVDSDefaultNetwork("net2", vdsWlNS, nil)
	networks := map[string][]v1alpha1.Network{
		vpcWlNS: {*vpcNetwork},
		vdsWlNS: {*vdsNetwork},
	}
	allowedResp := admission.Allowed("")
	disallowedResp := admission.Errored(http.StatusBadRequest, fmt.Errorf("VPC is not enabled in Namespace %s", vdsWlNS))
	systemNetworkNamespaces = sets.New[string]()

	ctx := context.Background()
	for _, tc := range []struct {
		name              string
		validatedResource bool
		requestKinds      []string
		requestNS         string
		isSystemNS        bool
		listNetInNS       bool
		req               admission.Request
		expResponse       admission.Response
	}{
		{
			name:              "Allow creating Pod in VPC NSs",
			validatedResource: false,
			requestKinds:      []string{"Pod"},
			requestNS:         vpcWlNS,
			listNetInNS:       true,
			expResponse:       allowedResp,
		},
		{
			name:              "Allow creating Pod in non-VPC NSs",
			validatedResource: false,
			requestKinds:      []string{"Pod"},
			requestNS:         vdsWlNS,
			listNetInNS:       true,
			expResponse:       allowedResp,
		},
		{
			name:              "Allow creating resources in VPC NSs",
			validatedResource: true,
			requestKinds:      []string{"IPPool", "NetworkInfo", "NSXServiceAccount", "SecurityPolicy", "StaticRoute", "SubnetPort", "Subnet", "SubnetSet"},
			requestNS:         vpcWlNS,
			listNetInNS:       true,
			expResponse:       allowedResp,
		},
		{
			name:              "Disallow creating resources in non-VPC NSs",
			validatedResource: true,
			requestKinds:      []string{"IPPool", "NetworkInfo", "NSXServiceAccount", "SecurityPolicy", "StaticRoute", "SubnetPort", "Subnet", "SubnetSet"},
			requestNS:         vdsWlNS,
			listNetInNS:       true,
			expResponse:       disallowedResp,
		}, {
			name:              "Error when listing resources in a system NS with no Networks",
			validatedResource: true,
			isSystemNS:        true,
			requestKinds:      []string{"IPPool"},
			requestNS:         "kube-system",
			listNetInNS:       false,
			expResponse: admission.Errored(http.StatusBadRequest,
				fmt.Errorf("unable to check the default network type in Namespace kube-system: no shared VPC namespace found with system Namespace kube-system")),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.validatedResource {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(
					func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
						if tc.isSystemNS {
							copyNamespace(obj, createSystemNamespace(tc.requestNS))
						} else {
							copyNamespace(obj, createWorkloadNamespace(tc.requestNS))
						}
						return nil
					},
				).Times(len(tc.requestKinds))
				if tc.listNetInNS {
					k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(
						func(_ context.Context, obj client.ObjectList, options ...client.ListOption) error {
							netList := obj.(*v1alpha1.NetworkList)
							netList.Items = networks[tc.requestNS]
							return nil
						},
					).Times(len(tc.requestKinds))
				}
			}
			for _, kind := range tc.requestKinds {
				req := admission.Request{
					AdmissionRequest: admissionv1.AdmissionRequest{Kind: v1.GroupVersionKind{Kind: kind}, Namespace: tc.requestNS},
				}
				response := reconciler.Handle(ctx, req)
				assert.Equal(t, tc.expResponse, response)
			}
		})
	}
}

type fakeLister struct {
	items []types.NamespacedName
	err   error
}

func (l *fakeLister) list(ns string) ([]types.NamespacedName, error) {
	results := make([]types.NamespacedName, 0)
	for _, item := range l.items {
		if item.Namespace == ns {
			results = append(results, item)
		}
	}
	return results, l.err
}

func createVPCDefaultNetwork(name, ns string, dns []string) *v1alpha1.Network {
	return createDefaultNetwork(name, ns, v1alpha1.NetworkTypeNSXTVPC, dns)
}

func createVDSDefaultNetwork(name, ns string, dns []string) *v1alpha1.Network {
	return createDefaultNetwork(name, ns, v1alpha1.NetworkTypeVDS, dns)
}

func createDefaultNetwork(name, ns string, networkType v1alpha1.NetworkType, dns []string) *v1alpha1.Network {
	return createNetwork(name, ns, networkType, map[string]string{defaultNetworkLabelKey: defaultNetworkLabelValue}, dns)
}

func createNetwork(name, ns string, networkType v1alpha1.NetworkType, labels map[string]string, dns []string) *v1alpha1.Network {
	return &v1alpha1.Network{
		ObjectMeta: v1.ObjectMeta{
			Namespace: ns,
			Name:      name,
			Labels:    labels,
		},
		Spec: v1alpha1.NetworkSpec{
			Type: networkType,
			DNS:  dns,
		},
	}
}

func copyNamespace(obj client.Object, namespace *corev1.Namespace) {
	nsObj := obj.(*corev1.Namespace)
	nsObj.Namespace = namespace.Namespace
	nsObj.Name = namespace.Name
	nsObj.Annotations = namespace.Annotations
	nsObj.Labels = namespace.Labels
}

func createSystemNamespace(name string) *corev1.Namespace {
	return createNamespace(name, map[string]string{})
}

func createWorkloadNamespace(name string) *corev1.Namespace {
	return createNamespace(name, map[string]string{svccommon.LabelWorkloadNamespace: "true"})
}

func createNamespace(name string, labels map[string]string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: v1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}
}
