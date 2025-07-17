package inventory

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/go-vmware-nsxt/common"
	"github.com/vmware/go-vmware-nsxt/containerinventory"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	networkv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/networkinfo"
	mockClient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	commonservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func createService(t *testing.T) (*InventoryService, *mockClient.MockClient) {
	config2 := nsx.NewConfig("localhost", "1", "1", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{"127.0.0.1"})

	cluster, _ := nsx.NewCluster(config2)
	rc := cluster.NewRestConnector()

	mockCtrl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtrl)
	httpClient := http.DefaultClient
	cf := &config.NSXOperatorConfig{
		CoeConfig: &config.CoeConfig{
			Cluster: "k8scl-one:test",
		},
		NsxConfig: &config.NsxConfig{NsxApiManagers: []string{"127.0.0.1"}},
	}
	nsxApiClient, _ := nsx.CreateNsxtApiClient(cf, httpClient)
	commonservice := commonservice.Service{
		Client: k8sClient,
		NSXClient: &nsx.Client{
			RestConnector: rc,
			NsxConfig:     cf,
			NsxApiClient:  nsxApiClient,
		},
		NSXConfig: &config.NSXOperatorConfig{
			CoeConfig: &config.CoeConfig{
				Cluster: "k8scl-one:test",
			},
			NsxConfig: &config.NsxConfig{
				InventoryBatchSize: 2,
			},
		},
	}

	service, _ := InitializeService(commonservice, false)
	return service, k8sClient
}

func TestBuildPod(t *testing.T) {
	labels := make(map[string]string)
	labels["app"] = "inventory"
	labels["role"] = "test-only"
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node",
			UID:    "111111111",
			Labels: labels,
		},
	}
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "namespace",
			UID:    "222222222",
			Labels: labels,
		},
	}
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       "pod-uid-123",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
	}

	t.Run("NormalFlow", func(t *testing.T) {
		inventoryService, k8sClient := createService(t)

		k8sClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.ListOption) error {
				ns, ok := obj.(*corev1.Namespace)
				if !ok {
					return nil
				}
				namespace.DeepCopyInto(ns)
				return nil
			})
		k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.ListOption) error {
				result, ok := obj.(*corev1.Node)
				if !ok {
					return nil
				}
				node.DeepCopyInto(result)
				return nil
			})

		testPod.Status = corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIPs: []corev1.PodIP{
				{IP: "192.168.1.1"},
			},
		}
		applicationInstance := &containerinventory.ContainerApplicationInstance{}
		applicationInstance.ContainerApplicationIds = []string{"app-id-123"}
		inventoryService.pendingAdd[string(testPod.UID)] = applicationInstance
		retry := inventoryService.BuildPod(testPod)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "pod-uid-123")
		applicationInstance = inventoryService.pendingAdd["pod-uid-123"].(*containerinventory.ContainerApplicationInstance)
		assert.Equal(t, applicationInstance.ContainerApplicationIds, []string{"app-id-123"})
		assert.Equal(t, applicationInstance.ContainerProjectId, string(namespace.UID))
		assert.Equal(t, applicationInstance.ClusterNodeId, string(node.UID))
		assert.Equal(t, applicationInstance.ExternalId, string(testPod.UID))
		assert.Equal(t, applicationInstance.ContainerClusterId, inventoryService.NSXConfig.Cluster)

		keypaire := common.KeyValuePair{Key: "ip", Value: "192.168.1.1"}
		assert.Contains(t, applicationInstance.OriginProperties, keypaire)
	})

	t.Run("NamespaceNotFound", func(t *testing.T) {
		inventoryService, k8sClient := createService(t)
		k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("not found"))
		retry := inventoryService.BuildPod(testPod)
		assert.True(t, retry)
	})

	t.Run("NodeNotFoundWithRetry", func(t *testing.T) {
		inventoryService, k8sClient := createService(t)
		k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("node not found"))
		retry := inventoryService.BuildPod(testPod)
		assert.True(t, retry)
	})

	t.Run("PodWithStatusMessage", func(t *testing.T) {
		inventoryService, k8sClient := createService(t)

		k8sClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.ListOption) error {
				ns, ok := obj.(*corev1.Namespace)
				if !ok {
					return nil
				}
				namespace.DeepCopyInto(ns)
				return nil
			})
		k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.ListOption) error {
				result, ok := obj.(*corev1.Node)
				if !ok {
					return nil
				}
				node.DeepCopyInto(result)
				return nil
			})

		// Create a pod with a status message
		podWithStatusMessage := testPod.DeepCopy()
		podWithStatusMessage.Status = corev1.PodStatus{
			Phase:   corev1.PodPending,
			Message: "failed to pull images",
			Conditions: []corev1.PodCondition{
				{
					Type:               corev1.PodReady,
					Status:             corev1.ConditionFalse,
					Message:            "Pod is not ready",
					LastTransitionTime: metav1.Time{Time: time.Now().Add(-1 * time.Hour)},
				},
			},
		}

		retry := inventoryService.BuildPod(podWithStatusMessage)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, string(podWithStatusMessage.UID))

		// Verify the network errors include the status message
		applicationInstance := inventoryService.pendingAdd[string(podWithStatusMessage.UID)].(*containerinventory.ContainerApplicationInstance)
		assert.Len(t, applicationInstance.NetworkErrors, 2)
		assert.Equal(t, "failed to pull images", applicationInstance.NetworkErrors[0].ErrorMessage)
		assert.Equal(t, "Pod is not ready", applicationInstance.NetworkErrors[1].ErrorMessage)
	})
}

func TestGetTagsFromLabels(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]string
		expected []common.Tag
	}{
		{
			name: "normal case with sorted keys",
			input: map[string]string{
				"app":     "nginx",
				"version": "1.19",
			},
			expected: []common.Tag{
				{Scope: "k8s.label.app", Tag: "nginx"},
				{Scope: "k8s.label.version", Tag: "1.19"},
			},
		},
		{
			name: "long label value truncation",
			input: map[string]string{
				"key": "this-is-a-very-long-value-that-exceeds-max-tag-length-1234567890",
			},
			expected: []common.Tag{
				{Scope: "k8s.label.key", Tag: "this-is-a-very-long-value-that-exceed"},
			},
		},
		{
			name: "long key normalization with hash suffix",
			input: map[string]string{
				"this-is-an-extra-long-label-key-that-needs-normalization": "value",
			},
			expected: []common.Tag{
				{
					Scope: "k8s.label.this-is-an-extra-long-label-key-that--4d35f159",
					Tag:   "value",
				},
			},
		},
		{
			name:     "empty labels input",
			input:    map[string]string{},
			expected: []common.Tag{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetTagsFromLabels(tt.input)

			// Validate tag count matches expectation
			assert.Equal(t, len(tt.expected), len(result), "Tag count mismatch")

			// Verify alphabetical order of scopes
			for i := 1; i < len(result); i++ {
				assert.True(t, result[i-1].Scope < result[i].Scope, "Tags not sorted alphabetically")
			}

			// Validate individual tag properties
			for i := range tt.expected {
				if i >= len(result) {
					break
				}

				// Check k8s label prefix
				assert.Contains(t, result[i].Scope, InventoryK8sPrefix, "Missing k8s label prefix")

				// Validate scope length limit
				assert.True(t, len(result[i].Scope) <= MaxResourceTypeLen,
					"Scope length exceeds limit, actual length: %d", len(result[i].Scope))

				// Verify value truncation when needed
				originalValue := tt.input[result[i].Scope[len(InventoryK8sPrefix):]]
				if len(originalValue) > MaxTagLen {
					assert.Equal(t, MaxTagLen, len(result[i].Tag), "Value not properly truncated")
				}
			}
		})
	}
}

func TestBuildNamespace(t *testing.T) {
	labels := make(map[string]string)
	labels["environment"] = "test"
	labels["team"] = "platform"

	testNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-namespace",
			UID:    "namespace-uid-123",
			Labels: labels,
		},
	}

	t.Run("NormalFlow", func(t *testing.T) {
		inventoryService, _ := createService(t)

		// Test with a new namespace
		retry := inventoryService.BuildNamespace(testNamespace)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "namespace-uid-123")

		containerProject := inventoryService.pendingAdd["namespace-uid-123"].(*containerinventory.ContainerProject)
		assert.Equal(t, "test-namespace", containerProject.DisplayName)
		assert.Equal(t, string(ContainerProject), containerProject.ResourceType)
		assert.Equal(t, string(testNamespace.UID), containerProject.ExternalId)
		assert.Equal(t, inventoryService.NSXConfig.Cluster, containerProject.ContainerClusterId)
		assert.Equal(t, NetworkStatusHealthy, containerProject.NetworkStatus)

		// Verify tags are created from labels
		expectedTags := GetTagsFromLabels(labels)
		assert.Equal(t, len(expectedTags), len(containerProject.Tags))
		for i, tag := range containerProject.Tags {
			assert.Equal(t, expectedTags[i].Scope, tag.Scope)
			assert.Equal(t, expectedTags[i].Tag, tag.Tag)
		}
	})

	t.Run("UpdateExistingNamespace", func(t *testing.T) {
		inventoryService, _ := createService(t)

		// Create a pre-existing namespace in the ProjectStore
		existingProject := &containerinventory.ContainerProject{
			DisplayName:        "old-name",
			ResourceType:       string(ContainerProject),
			Tags:               []common.Tag{},
			ContainerClusterId: inventoryService.NSXConfig.Cluster,
			ExternalId:         string(testNamespace.UID),
			NetworkStatus:      NetworkStatusHealthy,
		}

		// Add to ProjectStore
		inventoryService.ProjectStore.Add(existingProject)

		// Now build the namespace with updated information
		updatedNamespace := testNamespace.DeepCopy()
		updatedNamespace.Labels["new-label"] = "new-value"

		retry := inventoryService.BuildNamespace(updatedNamespace)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "namespace-uid-123")

		// Verify the updated namespace is in pendingAdd
		containerProject := inventoryService.pendingAdd["namespace-uid-123"].(*containerinventory.ContainerProject)
		assert.Equal(t, "test-namespace", containerProject.DisplayName)

		// Verify the updated tags include the new label
		found := false
		for _, tag := range containerProject.Tags {
			if tag.Scope == "dis:k8s:new-label" && tag.Tag == "new-value" {
				found = true
				break
			}
		}
		assert.True(t, found, "New label should be included in tags")
	})

	t.Run("NoChangeToNamespace", func(t *testing.T) {
		inventoryService, _ := createService(t)

		// First build creates the initial namespace
		inventoryService.BuildNamespace(testNamespace)

		// Clear pendingAdd to simulate a completed sync
		initialProject := inventoryService.pendingAdd[string(testNamespace.UID)]
		delete(inventoryService.pendingAdd, string(testNamespace.UID))

		// Add to ProjectStore to simulate it's already been processed
		inventoryService.ProjectStore.Add(initialProject)

		// Build the same namespace again without changes
		retry := inventoryService.BuildNamespace(testNamespace)

		assert.False(t, retry)
		// Since there are no changes, it shouldn't be added to pendingAdd
		assert.NotContains(t, inventoryService.pendingAdd, string(testNamespace.UID))
	})

	t.Run("NamespaceWithNetworkReadyCondition", func(t *testing.T) {
		inventoryService, _ := createService(t)

		// Create a namespace with NamespaceNetworkReady condition
		namespaceWithCondition := testNamespace.DeepCopy()

		// Add a condition with VPCNetConfigNotReady reason
		namespaceWithCondition.Status.Conditions = []corev1.NamespaceCondition{
			{
				Type:    networkinfo.NamespaceNetworkReady,
				Status:  corev1.ConditionFalse,
				Reason:  networkinfo.NSReasonVPCNetConfigNotReady,
				Message: "VPC network configuration is not ready",
			},
		}

		// Build the namespace
		retry := inventoryService.BuildNamespace(namespaceWithCondition)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "namespace-uid-123")

		// Verify the network errors are created with the reason code included
		containerProject := inventoryService.pendingAdd["namespace-uid-123"].(*containerinventory.ContainerProject)
		assert.Len(t, containerProject.NetworkErrors, 1)
		assert.Equal(t, "VPCNetworkConfigurationNotReady: VPC network configuration is not ready", containerProject.NetworkErrors[0].ErrorMessage)
		assert.Equal(t, NetworkStatusUnhealthy, containerProject.NetworkStatus)
	})

	t.Run("NamespaceWithVPCNotReadyCondition", func(t *testing.T) {
		inventoryService, _ := createService(t)

		// Create a namespace with NamespaceNetworkReady condition and VPCNotReady reason
		namespaceWithCondition := testNamespace.DeepCopy()

		namespaceWithCondition.Status.Conditions = []corev1.NamespaceCondition{
			{
				Type:    networkinfo.NamespaceNetworkReady,
				Status:  corev1.ConditionFalse,
				Reason:  networkinfo.NSReasonVPCNotReady,
				Message: "VPC is not ready",
			},
		}

		// Build the namespace
		retry := inventoryService.BuildNamespace(namespaceWithCondition)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "namespace-uid-123")

		// Verify the network errors are created with the reason code included
		containerProject := inventoryService.pendingAdd["namespace-uid-123"].(*containerinventory.ContainerProject)
		assert.Len(t, containerProject.NetworkErrors, 1)
		assert.Equal(t, "VPCNotReady: VPC is not ready", containerProject.NetworkErrors[0].ErrorMessage)
		assert.Equal(t, NetworkStatusUnhealthy, containerProject.NetworkStatus)
	})

	t.Run("NamespaceWithVPCSnatNotReadyCondition", func(t *testing.T) {
		inventoryService, _ := createService(t)

		// Create a namespace with NamespaceNetworkReady condition and VPCSnatNotReady reason
		namespaceWithCondition := testNamespace.DeepCopy()

		namespaceWithCondition.Status.Conditions = []corev1.NamespaceCondition{
			{
				Type:    networkinfo.NamespaceNetworkReady,
				Status:  corev1.ConditionFalse,
				Reason:  networkinfo.NSReasonVPCSnatNotReady,
				Message: "VPC SNAT is not ready",
			},
		}

		// Build the namespace
		retry := inventoryService.BuildNamespace(namespaceWithCondition)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "namespace-uid-123")

		// Verify the network errors are created with the reason code included
		containerProject := inventoryService.pendingAdd["namespace-uid-123"].(*containerinventory.ContainerProject)
		assert.Len(t, containerProject.NetworkErrors, 1)
		assert.Equal(t, "VPCSnatNotReady: VPC SNAT is not ready", containerProject.NetworkErrors[0].ErrorMessage)
		assert.Equal(t, NetworkStatusUnhealthy, containerProject.NetworkStatus)
	})

	t.Run("NamespaceWithNetworkReadyConditionTrue", func(t *testing.T) {
		inventoryService, _ := createService(t)

		// Create a namespace with NamespaceNetworkReady condition but status True
		namespaceWithCondition := testNamespace.DeepCopy()

		namespaceWithCondition.Status.Conditions = []corev1.NamespaceCondition{
			{
				Type:    networkinfo.NamespaceNetworkReady,
				Status:  corev1.ConditionTrue,
				Reason:  "",
				Message: "",
			},
		}

		// Build the namespace
		retry := inventoryService.BuildNamespace(namespaceWithCondition)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "namespace-uid-123")

		// Verify no network errors are created since the condition is True
		containerProject := inventoryService.pendingAdd["namespace-uid-123"].(*containerinventory.ContainerProject)
		assert.Len(t, containerProject.NetworkErrors, 0)
		assert.Equal(t, NetworkStatusHealthy, containerProject.NetworkStatus)
	})

	t.Run("NamespaceWithBothReadyAndNotReadyConditions", func(t *testing.T) {
		inventoryService, _ := createService(t)

		// Create a namespace with both ready and not ready conditions
		namespaceWithCondition := testNamespace.DeepCopy()

		namespaceWithCondition.Status.Conditions = []corev1.NamespaceCondition{
			{
				Type:    networkinfo.NamespaceNetworkReady,
				Status:  corev1.ConditionTrue,
				Reason:  "",
				Message: "",
			},
			{
				Type:    networkinfo.NamespaceNetworkReady,
				Status:  corev1.ConditionFalse,
				Reason:  networkinfo.NSReasonVPCNetConfigNotReady,
				Message: "VPC network configuration is not ready",
			},
		}

		// Build the namespace
		retry := inventoryService.BuildNamespace(namespaceWithCondition)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "namespace-uid-123")

		// Verify no network errors are created since the True condition takes precedence
		containerProject := inventoryService.pendingAdd["namespace-uid-123"].(*containerinventory.ContainerProject)
		assert.Len(t, containerProject.NetworkErrors, 0)
		assert.Equal(t, NetworkStatusHealthy, containerProject.NetworkStatus)
	})

	t.Run("NamespaceWithUnknownReasonCondition", func(t *testing.T) {
		inventoryService, _ := createService(t)

		// Create a namespace with NamespaceNetworkReady condition but unknown reason
		namespaceWithCondition := testNamespace.DeepCopy()

		namespaceWithCondition.Status.Conditions = []corev1.NamespaceCondition{
			{
				Type:    networkinfo.NamespaceNetworkReady,
				Status:  corev1.ConditionFalse,
				Reason:  "SomeOtherReason",
				Message: "Some other error occurred",
			},
		}

		// Build the namespace
		retry := inventoryService.BuildNamespace(namespaceWithCondition)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "namespace-uid-123")

		// Verify the network errors are created with the reason code in the message
		containerProject := inventoryService.pendingAdd["namespace-uid-123"].(*containerinventory.ContainerProject)
		assert.Len(t, containerProject.NetworkErrors, 1)
		assert.Equal(t, "SomeOtherReason: Some other error occurred", containerProject.NetworkErrors[0].ErrorMessage)
		assert.Equal(t, NetworkStatusUnhealthy, containerProject.NetworkStatus)
	})
}

func TestSynchronizeServiceIDsWithApplicationInstances(t *testing.T) {
	t.Run("UpdateServiceIDs", func(t *testing.T) {
		inventoryService, k8sClient := createService(t)

		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-service",
				Namespace: "default",
				UID:       "service-uid-789",
			},
		}

		// Add an application instance to the store
		inventoryService.ApplicationInstanceStore.Add(&containerinventory.ContainerApplicationInstance{
			ExternalId:              "pod-uid-123",
			ContainerApplicationIds: []string{},
		})

		// Expect the List function to be called and mock the behavior
		k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		patches := gomonkey.ApplyFunc(GetPodByUID,
			func(ctx context.Context, client client.Client, uid types.UID, namespace string) (*corev1.Pod, error) {
				return &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						UID: uid,
					},
				}, nil
			},
		).ApplyFunc(GetServicesUIDByPodUID,
			func(_ context.Context, _ client.Client, _ types.UID, _ string) ([]string, error) {
				return []string{"service-uid-234", "service-uid-456"}, nil
			},
		)
		defer patches.Reset()

		podUIDs := []string{"pod-uid-123"}
		retry := inventoryService.synchronizeServiceIDsWithApplicationInstances(podUIDs, service)
		assert.False(t, retry)

		instance := inventoryService.ApplicationInstanceStore.GetByKey("pod-uid-123").(*containerinventory.ContainerApplicationInstance)
		assert.Contains(t, instance.ContainerApplicationIds, "service-uid-234")
		assert.Contains(t, instance.ContainerApplicationIds, "service-uid-456")
	})

	t.Run("RemoveStaleServiceIDs", func(t *testing.T) {
		inventoryService, _ := createService(t)

		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-service",
				Namespace: "default",
				UID:       "service-uid-789",
			},
		}

		staleInstance := &containerinventory.ContainerApplicationInstance{
			ExternalId:              "stale-pod-uid",
			ContainerApplicationIds: []string{"service-uid-789"},
		}

		inventoryService.ApplicationInstanceStore.Add(staleInstance)

		podUIDs := []string{"pod-uid-123"}
		inventoryService.removeStaleServiceIDsFromApplicationInstances(podUIDs, service)

		updatedInstance := inventoryService.ApplicationInstanceStore.GetByKey("stale-pod-uid").(*containerinventory.ContainerApplicationInstance)
		assert.NotContains(t, updatedInstance.ContainerApplicationIds, string(service.UID))
	})
}

func TestApplyServiceIDUpdates(t *testing.T) {
	inventoryService, _ := createService(t)

	instance := &containerinventory.ContainerApplicationInstance{
		ExternalId:              "pod-uid-123",
		ContainerApplicationIds: []string{},
	}
	updatedServiceIDs := []string{"service-uid-456", "service-uid-789"}

	inventoryService.applyServiceIDUpdates(instance, updatedServiceIDs)

	assert.Equal(t, updatedServiceIDs, instance.ContainerApplicationIds)
	assert.Equal(t, 1, len(inventoryService.requestBuffer))
	assert.Equal(t, instance.ExternalId, inventoryService.requestBuffer[0].ContainerObject["external_id"])
}

func TestUpdateServiceIDsForApplicationInstance(t *testing.T) {
	inventoryService, k8sClient := createService(t)

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-service",
			Namespace: "default",
			UID:       "service-uid-123",
		},
	}

	podUID := "pod-uid-123"
	instance := &containerinventory.ContainerApplicationInstance{
		ExternalId: podUID,
	}
	inventoryService.ApplicationInstanceStore.Add(instance)

	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	patches := gomonkey.ApplyFunc(GetPodByUID,
		func(ctx context.Context, client client.Client, uid types.UID, namespace string) (*corev1.Pod, error) {
			return &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: uid,
				},
			}, nil
		},
	).ApplyFunc(GetServicesUIDByPodUID,
		func(_ context.Context, _ client.Client, _ types.UID, _ string) ([]string, error) {
			return []string{"service-uid-234", "service-uid-345"}, nil
		},
	)
	defer patches.Reset()

	retry := inventoryService.updateServiceIDsForApplicationInstance(podUID, service)

	assert.False(t, retry)
	assert.Contains(t, instance.ContainerApplicationIds, "service-uid-234")
	assert.Contains(t, instance.ContainerApplicationIds, "service-uid-345")
}

func TestRemoveStaleServiceIDsFromApplicationInstances(t *testing.T) {
	inventoryService, _ := createService(t)

	// Create a service which has a UID that should be removed
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-service",
			Namespace: "default",
			UID:       "service-uid-789",
		},
	}

	// Add application instances to the store, some of which will have stale service UIDs
	instanceWithStaleID := &containerinventory.ContainerApplicationInstance{
		ExternalId:              "pod-uid-123",
		ContainerApplicationIds: []string{"service-uid-789", "service-uid-456"},
	}

	instanceWithValidID := &containerinventory.ContainerApplicationInstance{
		ExternalId:              "pod-uid-456",
		ContainerApplicationIds: []string{"service-uid-456"},
	}

	inventoryService.ApplicationInstanceStore.Add(instanceWithStaleID)
	inventoryService.ApplicationInstanceStore.Add(instanceWithValidID)

	// Simulate the list of pod UIDs that are currently valid
	podUIDs := []string{"pod-uid-456"}

	inventoryService.removeStaleServiceIDsFromApplicationInstances(podUIDs, service)

	// Verify that the stale UID is removed from the first instance
	updatedInstanceWithStaleID := inventoryService.ApplicationInstanceStore.GetByKey("pod-uid-123").(*containerinventory.ContainerApplicationInstance)
	assert.NotContains(t, updatedInstanceWithStaleID.ContainerApplicationIds, "service-uid-789")
	assert.Contains(t, updatedInstanceWithStaleID.ContainerApplicationIds, "service-uid-456")

	// Verify that the instance with valid IDs remains unchanged
	updatedInstanceWithValidID := inventoryService.ApplicationInstanceStore.GetByKey("pod-uid-456").(*containerinventory.ContainerApplicationInstance)
	assert.Contains(t, updatedInstanceWithValidID.ContainerApplicationIds, "service-uid-456")
}

func TestBuildIngress(t *testing.T) {
	tests := []struct {
		name           string
		ingress        *networkv1.Ingress
		existingPolicy *containerinventory.ContainerIngressPolicy
		namespace      *corev1.Namespace
		expectRetry    bool
	}{
		{
			name: "new ingress without existing policy",
			ingress: &networkv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "default",
					UID:       types.UID("test-uid"),
					Labels: map[string]string{
						"app": "test",
					},
				},
				Spec: networkv1.IngressSpec{
					Rules: []networkv1.IngressRule{
						{
							Host: "test.example.com",
						},
					},
				},
			},
			existingPolicy: nil,
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
					UID:  types.UID("ns-uid"),
				},
			},
			expectRetry: false,
		},
		{
			name: "update existing ingress",
			ingress: &networkv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "default",
					UID:       types.UID("test-uid"),
					Labels: map[string]string{
						"app": "test-updated",
					},
				},
				Spec: networkv1.IngressSpec{
					Rules: []networkv1.IngressRule{
						{
							Host: "test.example.com",
						},
					},
				},
			},
			existingPolicy: &containerinventory.ContainerIngressPolicy{
				DisplayName:        "test-ingress",
				ExternalId:         "test-uid",
				ContainerClusterId: "test-cluster",
				ContainerProjectId: "ns-uid",
				Tags: []common.Tag{
					{
						Scope: "app",
						Tag:   "test",
					},
				},
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
					UID:  types.UID("ns-uid"),
				},
			},
			expectRetry: false,
		},
		{
			name: "ingress with annotations",
			ingress: &networkv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "default",
					UID:       types.UID("test-uid"),
					Labels: map[string]string{
						"app": "test",
					},
					Annotations: map[string]string{
						"ncp/error.loadbalancer": "loadbalancer error message",
					},
				},
				Spec: networkv1.IngressSpec{
					Rules: []networkv1.IngressRule{
						{
							Host: "test.example.com",
						},
					},
				},
			},
			existingPolicy: nil,
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
					UID:  types.UID("ns-uid"),
				},
			},
			expectRetry: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inventoryService, _ := createService(t)
			// Mock GetNamespace
			patches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "GetNamespace", func(_ *InventoryService, namespace string) (*corev1.Namespace, error) {
				return tt.namespace, nil
			})
			patches.ApplyMethod(reflect.TypeOf(inventoryService.IngressPolicyStore), "GetByKey", func(_ *IngressPolicyStore, _ string) interface{} {
				if tt.existingPolicy != nil {
					return tt.existingPolicy
				} else {
					return nil
				}
			})
			defer patches.Reset()
			// Test
			retry := inventoryService.BuildIngress(tt.ingress)

			// Verify
			assert.Equal(t, tt.expectRetry, retry)

			// Verify pending operations
			if tt.existingPolicy == nil {
				// Should be in pendingAdd
				assert.NotNil(t, inventoryService.pendingAdd[string(tt.ingress.UID)])
				obj := inventoryService.pendingAdd[string(tt.ingress.UID)]
				newPolicy := obj.(*containerinventory.ContainerIngressPolicy)
				assert.Equal(t, tt.ingress.Name, newPolicy.DisplayName)
				assert.Equal(t, tt.ingress.Name, newPolicy.DisplayName)
				assert.Equal(t, string(tt.ingress.UID), newPolicy.ExternalId)
				assert.Equal(t, "k8scl-one:test", newPolicy.ContainerClusterId)
				assert.Equal(t, string(tt.namespace.UID), newPolicy.ContainerProjectId)

				// Verify network errors if the test case has annotations
				if tt.name == "ingress with annotations" {
					assert.Equal(t, 1, len(newPolicy.NetworkErrors))
					assert.Equal(t, "ncp/error.loadbalancer:loadbalancer error message", newPolicy.NetworkErrors[0].ErrorMessage)
				}
			}
		})
	}
}

func TestBuildIngress_ErrorCases(t *testing.T) {
	tests := []struct {
		name         string
		ingress      *networkv1.Ingress
		namespaceErr error
		expectRetry  bool
	}{
		{
			name: "namespace not found",
			ingress: &networkv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "non-existent",
					UID:       types.UID("test-uid"),
				},
			},
			namespaceErr: fmt.Errorf("namespace not found"),
			expectRetry:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			inventoryService, _ := createService(t)
			// Mock GetNamespace
			patches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "GetNamespace", func(_ *InventoryService, namespace string) (*corev1.Namespace, error) {
				if tt.namespaceErr != nil {
					return nil, tt.namespaceErr
				}
				return &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: namespace,
						UID:  types.UID("ns-uid"),
					},
				}, nil
			})
			defer patches.Reset()
			retry := inventoryService.BuildIngress(tt.ingress)
			assert.Equal(t, tt.expectRetry, retry)
		})
	}
}

func TestGetIngressAppIds(t *testing.T) {
	tests := []struct {
		name          string
		ingress       *networkv1.Ingress
		want          []string
		getServiceErr error
	}{
		{
			name: "ingress with backend/rules",
			ingress: &networkv1.Ingress{
				Spec: networkv1.IngressSpec{
					DefaultBackend: &networkv1.IngressBackend{
						Service: &networkv1.IngressServiceBackend{
							Name: "service2",
						},
					},
					Rules: []networkv1.IngressRule{
						{
							IngressRuleValue: networkv1.IngressRuleValue{
								HTTP: &networkv1.HTTPIngressRuleValue{
									Paths: []networkv1.HTTPIngressPath{
										{
											Backend: networkv1.IngressBackend{
												Service: &networkv1.IngressServiceBackend{
													Name: "service1",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			want:          []string{"service1", "service2"},
			getServiceErr: nil,
		},
		{
			name: "ingress with service in rules",
			ingress: &networkv1.Ingress{
				Spec: networkv1.IngressSpec{
					DefaultBackend: &networkv1.IngressBackend{
						Service: &networkv1.IngressServiceBackend{
							Name: "service2",
						},
					},
				},
			},
			want:          []string{"service2"},
			getServiceErr: nil,
		},
		{
			name: "ingress with rules, get service error",
			ingress: &networkv1.Ingress{
				Spec: networkv1.IngressSpec{
					Rules: []networkv1.IngressRule{
						{
							IngressRuleValue: networkv1.IngressRuleValue{
								HTTP: &networkv1.HTTPIngressRuleValue{
									Paths: []networkv1.HTTPIngressPath{
										{
											Backend: networkv1.IngressBackend{
												Service: &networkv1.IngressServiceBackend{
													Name: "service1",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			want:          []string{},
			getServiceErr: errors.New("failed to get service"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, k8sClient := createService(t)
			k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.ListOption) error {
				service, ok := obj.(*corev1.Service)
				if !ok {
					return nil
				}
				service.UID = types.UID(key.Name)
				if tt.getServiceErr != nil {
					return tt.getServiceErr
				}
				return nil
			}).AnyTimes()
			got := service.getIngressAppIds(tt.ingress)
			assert.ElementsMatch(t, tt.want, got)
		})
	}
}

func TestBuildNode(t *testing.T) {
	labels := map[string]string{
		"region": "us-west",
		"env":    "production",
	}
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-node",
			UID:    types.UID("node-uid-123"),
			Labels: labels,
		},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "192.168.99.1"},
			},
			NodeInfo: corev1.NodeSystemInfo{
				KubeletVersion: "v1.19.0",
				OSImage:        "Ubuntu 20.04.1 LTS",
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}

	t.Run("NormalFlow", func(t *testing.T) {
		inventoryService, _ := createService(t)

		retry := inventoryService.BuildNode(testNode)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "node-uid-123")

		containerClusterNode := inventoryService.pendingAdd["node-uid-123"].(*containerinventory.ContainerClusterNode)
		assert.Equal(t, "test-node", containerClusterNode.DisplayName)
		assert.Equal(t, string(ContainerClusterNode), containerClusterNode.ResourceType)
		assert.Equal(t, string(testNode.UID), containerClusterNode.ExternalId)
		assert.Equal(t, inventoryService.NSXConfig.Cluster, containerClusterNode.ContainerClusterId)
		assert.Equal(t, NetworkStatusHealthy, containerClusterNode.NetworkStatus)
		assert.Contains(t, containerClusterNode.IpAddresses, "192.168.99.1")

		// Verify tags are created from labels
		expectedTags := GetTagsFromLabels(labels)
		assert.Equal(t, len(expectedTags), len(containerClusterNode.Tags))
		for i, tag := range containerClusterNode.Tags {
			assert.Equal(t, expectedTags[i].Scope, tag.Scope)
			assert.Equal(t, expectedTags[i].Tag, tag.Tag)
		}
		assert.Contains(t, containerClusterNode.OriginProperties, common.KeyValuePair{Key: "kubelet_version", Value: "v1.19.0"})
		assert.Contains(t, containerClusterNode.OriginProperties, common.KeyValuePair{Key: "os_image", Value: "Ubuntu 20.04.1 LTS"})
	})

	t.Run("UpdateExistingNode", func(t *testing.T) {
		inventoryService, _ := createService(t)

		// Create a pre-existing node in the ClusterNodeStore
		existingNode := &containerinventory.ContainerClusterNode{
			DisplayName:        "old-node-name",
			ResourceType:       string(ContainerClusterNode),
			Tags:               []common.Tag{},
			ContainerClusterId: inventoryService.NSXConfig.Cluster,
			ExternalId:         string(testNode.UID),
			NetworkStatus:      NetworkStatusHealthy,
		}

		// Add to ClusterNodeStore
		inventoryService.ClusterNodeStore.Add(existingNode)

		// Now build the node with updated information
		updatedNode := testNode.DeepCopy()
		updatedNode.Labels["new-label"] = "new-value"

		retry := inventoryService.BuildNode(updatedNode)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "node-uid-123")

		// Verify the updated node is in pendingAdd
		containerClusterNode := inventoryService.pendingAdd["node-uid-123"].(*containerinventory.ContainerClusterNode)
		assert.Equal(t, "test-node", containerClusterNode.DisplayName)

		// Verify the updated tags include the new label
		found := false
		for _, tag := range containerClusterNode.Tags {
			if tag.Scope == "dis:k8s:new-label" && tag.Tag == "new-value" {
				found = true
				break
			}
		}
		assert.True(t, found, "New label should be included in tags")
	})

	t.Run("NoChangeToNode", func(t *testing.T) {
		inventoryService, _ := createService(t)

		// Create a pre-existing node in the ClusterNodeStore
		existingNode := &containerinventory.ContainerClusterNode{
			DisplayName:        "test-node",
			ResourceType:       string(ContainerClusterNode),
			Tags:               GetTagsFromLabels(labels),
			ContainerClusterId: inventoryService.NSXConfig.Cluster,
			ExternalId:         string(testNode.UID),
			NetworkErrors:      []common.NetworkError{},
			NetworkStatus:      NetworkStatusHealthy,
			IpAddresses:        []string{"192.168.99.1"},
			OriginProperties: []common.KeyValuePair{
				{Key: "kubelet_version", Value: "v1.19.0"},
				{Key: "os_image", Value: "Ubuntu 20.04.1 LTS"},
			},
		}

		// Add to ClusterNodeStore to simulate it's already been processed
		inventoryService.ClusterNodeStore.Add(existingNode)

		// Build the same node again without changes
		retry := inventoryService.BuildNode(testNode)

		assert.False(t, retry)
		// Since there are no changes, it shouldn't be added to pendingAdd
		assert.NotContains(t, inventoryService.pendingAdd, string(testNode.UID))
	})

	t.Run("NodeNetworkErrors", func(t *testing.T) {
		inventoryService, _ := createService(t)

		// Create a node with conditions
		testNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node",
				UID:  types.UID("node-uid-123"),
			},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{
						Type:               corev1.NodeReady,
						Status:             corev1.ConditionFalse,
						Message:            "Node is not ready",
						LastTransitionTime: metav1.Time{Time: time.Now().Add(-1 * time.Hour)},
					},
					{
						Type:               corev1.NodeReady,
						Status:             corev1.ConditionFalse,
						Message:            "Disk pressure",
						LastTransitionTime: metav1.Time{Time: time.Now()},
					},
				},
			},
		}

		// Build the node
		retry := inventoryService.BuildNode(testNode)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "node-uid-123")

		// Verify the network errors are created and sorted
		containerClusterNode := inventoryService.pendingAdd["node-uid-123"].(*containerinventory.ContainerClusterNode)
		assert.Len(t, containerClusterNode.NetworkErrors, 2)
		assert.Equal(t, "Disk pressure", containerClusterNode.NetworkErrors[0].ErrorMessage)
		assert.Equal(t, "Node is not ready", containerClusterNode.NetworkErrors[1].ErrorMessage)
	})
}

func TestBuildService(t *testing.T) {
	labels := map[string]string{
		"app":  "inventory",
		"role": "test-only",
	}

	testService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-service",
			Namespace: "default",
			UID:       types.UID("service-uid-123"),
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.0.0.1",
			Type:      corev1.ServiceTypeClusterIP,
			Selector:  map[string]string{"app": "inventory"},
		},
	}

	t.Run("NormalFlow", func(t *testing.T) {
		inventoryService, k8sClient := createService(t)

		// Add a pod to the ApplicationInstanceStore
		inventoryService.ApplicationInstanceStore.Add(&containerinventory.ContainerApplicationInstance{
			ExternalId:              "pod-uid-456",
			ContainerApplicationIds: []string{},
		})

		// Mock namespace retrieval
		k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.ListOption) error {
			ns, ok := obj.(*corev1.Namespace)
			if !ok {
				return nil
			}
			ns.ObjectMeta = metav1.ObjectMeta{
				Name: "default",
				UID:  "namespace-uid-123",
			}
			return nil
		})

		// Mock GetPodIDsFromEndpoint, GetPodByUID, and GetServicesUIDByPodUID
		patches := gomonkey.ApplyFunc(GetPodIDsFromEndpoint, func(ctx context.Context, c client.Client, name string, namespace string) ([]string, bool) {
			return []string{"pod-uid-456"}, true
		}).ApplyFunc(GetPodByUID, func(ctx context.Context, client client.Client, uid types.UID, namespace string) (*corev1.Pod, error) {
			return &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: uid,
				},
			}, nil
		}).ApplyFunc(GetServicesUIDByPodUID, func(_ context.Context, _ client.Client, _ types.UID, _ string) ([]string, error) {
			return []string{"service-uid-123"}, nil
		})
		defer patches.Reset()

		retry := inventoryService.BuildService(testService)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "service-uid-123")

		containerApplication := inventoryService.pendingAdd["service-uid-123"].(*containerinventory.ContainerApplication)
		assert.Equal(t, "test-service", containerApplication.DisplayName)
		assert.Equal(t, string(ContainerApplication), containerApplication.ResourceType)
		assert.Equal(t, string(testService.UID), containerApplication.ExternalId)
		assert.Equal(t, inventoryService.NSXConfig.Cluster, containerApplication.ContainerClusterId)
		assert.Equal(t, NetworkStatusHealthy, containerApplication.NetworkStatus)
		assert.Equal(t, InventoryStatusUp, containerApplication.Status)

		// Verify tags are created from labels
		expectedTags := GetTagsFromLabels(labels)
		assert.Equal(t, len(expectedTags), len(containerApplication.Tags))
		for i, tag := range containerApplication.Tags {
			assert.Equal(t, expectedTags[i].Scope, tag.Scope)
			assert.Equal(t, expectedTags[i].Tag, tag.Tag)
		}

		// Verify origin properties
		assert.Equal(t, 2, len(containerApplication.OriginProperties))
		assert.Equal(t, "type", containerApplication.OriginProperties[0].Key)
		assert.Equal(t, "ClusterIP", containerApplication.OriginProperties[0].Value)
		assert.Equal(t, "ip", containerApplication.OriginProperties[1].Key)
		assert.Equal(t, "10.0.0.1", containerApplication.OriginProperties[1].Value)
	})

	t.Run("NamespaceError", func(t *testing.T) {
		inventoryService, k8sClient := createService(t)

		// Simulate an error when retrieving the namespace
		k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(fmt.Errorf("namespace retrieval error"))

		retry := inventoryService.BuildService(testService)

		assert.True(t, retry)
	})

	t.Run("NoEndpoints", func(t *testing.T) {
		inventoryService, k8sClient := createService(t)

		// Mock namespace retrieval
		k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		// Mock GetPodIDsFromEndpoint to return no pods but has address
		patches := gomonkey.ApplyFunc(GetPodIDsFromEndpoint, func(ctx context.Context, c client.Client, name string, namespace string) ([]string, bool) {
			return []string{}, true
		})
		defer patches.Reset()

		retry := inventoryService.BuildService(testService)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "service-uid-123")

		containerApplication := inventoryService.pendingAdd["service-uid-123"].(*containerinventory.ContainerApplication)
		assert.Equal(t, InventoryStatusDown, containerApplication.Status)
		assert.Equal(t, NetworkStatusUnhealthy, containerApplication.NetworkStatus)
	})

	t.Run("NoEndpointsNoAddress", func(t *testing.T) {
		inventoryService, k8sClient := createService(t)

		// Mock namespace retrieval
		k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		// Mock GetPodIDsFromEndpoint to return no pods and no address
		patches := gomonkey.ApplyFunc(GetPodIDsFromEndpoint, func(ctx context.Context, c client.Client, name string, namespace string) ([]string, bool) {
			return []string{}, false
		})
		defer patches.Reset()

		retry := inventoryService.BuildService(testService)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "service-uid-123")

		containerApplication := inventoryService.pendingAdd["service-uid-123"].(*containerinventory.ContainerApplication)
		assert.Equal(t, InventoryStatusDown, containerApplication.Status)
		assert.Equal(t, NetworkStatusUnhealthy, containerApplication.NetworkStatus)
	})

	t.Run("UpdateExistingService", func(t *testing.T) {
		inventoryService, k8sClient := createService(t)

		// Add a pod to the ApplicationInstanceStore
		inventoryService.ApplicationInstanceStore.Add(&containerinventory.ContainerApplicationInstance{
			ExternalId:              "pod-uid-456",
			ContainerApplicationIds: []string{},
		})

		// Create a pre-existing service in the ApplicationStore
		existingApplication := &containerinventory.ContainerApplication{
			DisplayName:        "old-name",
			ResourceType:       string(ContainerApplication),
			Tags:               []common.Tag{},
			ContainerClusterId: inventoryService.NSXConfig.Cluster,
			ExternalId:         string(testService.UID),
			NetworkStatus:      NetworkStatusHealthy,
		}

		// Add to ApplicationStore
		inventoryService.ApplicationStore.Add(existingApplication)

		// Mock namespace retrieval
		k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.ListOption) error {
			ns, ok := obj.(*corev1.Namespace)
			if !ok {
				return nil
			}
			ns.ObjectMeta = metav1.ObjectMeta{
				Name: "default",
				UID:  "namespace-uid-123",
			}
			return nil
		})

		// Mock GetPodIDsFromEndpoint, GetPodByUID, and GetServicesUIDByPodUID
		patches := gomonkey.ApplyFunc(GetPodIDsFromEndpoint, func(ctx context.Context, c client.Client, name string, namespace string) ([]string, bool) {
			return []string{"pod-uid-456"}, true
		}).ApplyFunc(GetPodByUID, func(ctx context.Context, client client.Client, uid types.UID, namespace string) (*corev1.Pod, error) {
			return &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: uid,
				},
			}, nil
		}).ApplyFunc(GetServicesUIDByPodUID, func(_ context.Context, _ client.Client, _ types.UID, _ string) ([]string, error) {
			return []string{"service-uid-123"}, nil
		})
		defer patches.Reset()

		retry := inventoryService.BuildService(testService)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "service-uid-123")

		// Verify the updated service is in pendingAdd
		containerApplication := inventoryService.pendingAdd["service-uid-123"].(*containerinventory.ContainerApplication)
		assert.Equal(t, "test-service", containerApplication.DisplayName)
	})

	t.Run("ServiceWithAnnotations", func(t *testing.T) {
		inventoryService, k8sClient := createService(t)

		// Create a service with annotations that match the NCP error keys
		serviceWithAnnotations := testService.DeepCopy()
		serviceWithAnnotations.Annotations = map[string]string{
			NcpLbError:     "load balancer error message",
			NcpLbPortError: "port error message",
			NcpSnatError:   "snat error message",
		}

		// Mock namespace retrieval
		k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		// Mock GetPodIDsFromEndpoint to return no pods and no address (to ensure status is not "Up")
		patches := gomonkey.ApplyFunc(GetPodIDsFromEndpoint, func(ctx context.Context, c client.Client, name string, namespace string) ([]string, bool) {
			return []string{}, false
		})
		defer patches.Reset()

		retry := inventoryService.BuildService(serviceWithAnnotations)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "service-uid-123")

		// Verify the network errors are correctly extracted from the annotations
		containerApplication := inventoryService.pendingAdd["service-uid-123"].(*containerinventory.ContainerApplication)
		assert.Equal(t, InventoryStatusDown, containerApplication.Status)
		assert.Equal(t, NetworkStatusUnhealthy, containerApplication.NetworkStatus)

		// Verify network errors
		assert.Equal(t, 4, len(containerApplication.NetworkErrors))

		// Create a map of error messages for easier verification
		errorMessages := make(map[string]bool)
		for _, err := range containerApplication.NetworkErrors {
			errorMessages[err.ErrorMessage] = true
		}

		// Verify all expected error messages are present
		assert.True(t, errorMessages[NcpLbError+":load balancer error message"])
		assert.True(t, errorMessages[NcpLbPortError+":port error message"])
		assert.True(t, errorMessages[NcpSnatError+":snat error message"])
	})
}

func TestBuildNetworkPolicy(t *testing.T) {
	labels := map[string]string{
		"policy": "security",
		"env":    "production",
	}

	testNetworkPolicy := &networkv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-networkpolicy",
			Namespace: "default",
			UID:       types.UID("networkpolicy-uid-123"),
			Labels:    labels,
		},
		Spec: networkv1.NetworkPolicySpec{},
	}

	t.Run("NormalFlow", func(t *testing.T) {
		inventoryService, k8sClient := createService(t)

		k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.ListOption) error {
			ns, ok := obj.(*corev1.Namespace)
			if !ok {
				return nil
			}
			ns.ObjectMeta = metav1.ObjectMeta{
				Name: "default",
				UID:  "namespace-uid-123",
			}
			return nil
		})

		retry := inventoryService.BuildNetworkPolicy(testNetworkPolicy)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "networkpolicy-uid-123")

		containerNetworkPolicy := inventoryService.pendingAdd["networkpolicy-uid-123"].(*containerinventory.ContainerNetworkPolicy)
		assert.Equal(t, "test-networkpolicy", containerNetworkPolicy.DisplayName)
		assert.Equal(t, string(ContainerNetworkPolicy), containerNetworkPolicy.ResourceType)
		assert.Equal(t, string(testNetworkPolicy.UID), containerNetworkPolicy.ExternalId)
		assert.Equal(t, inventoryService.NSXConfig.Cluster, containerNetworkPolicy.ContainerClusterId)
		assert.Equal(t, "HEALTHY", containerNetworkPolicy.NetworkStatus)

		// Verify tags are created from labels
		expectedTags := GetTagsFromLabels(labels)
		assert.Equal(t, len(expectedTags), len(containerNetworkPolicy.Tags))
		for i, tag := range containerNetworkPolicy.Tags {
			assert.Equal(t, expectedTags[i].Scope, tag.Scope)
			assert.Equal(t, expectedTags[i].Tag, tag.Tag)
		}
	})
	t.Run("NamespaceError", func(t *testing.T) {
		inventoryService, k8sClient := createService(t)

		// Simulate an error when retrieving the namespace
		k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(fmt.Errorf("namespace retrieval error"))

		retry := inventoryService.BuildNetworkPolicy(testNetworkPolicy)

		assert.True(t, retry)
	})

	t.Run("SpecMarshalError", func(t *testing.T) {
		inventoryService, k8sClient := createService(t)

		// Mock namespace retrieval to succeed
		k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		// Simulate a failure in marshaling the NetworkPolicy spec
		patches := gomonkey.ApplyFunc(yaml.Marshal, func(in interface{}) ([]byte, error) {
			return nil, fmt.Errorf("failed to marshal spec")
		})
		defer patches.Reset()

		retry := inventoryService.BuildNetworkPolicy(testNetworkPolicy)

		assert.False(t, retry)
		// Since the spec couldn't be marshaled, ensure the object is not in pendingAdd
		assert.NotContains(t, inventoryService.pendingAdd, string(testNetworkPolicy.UID))
	})

	t.Run("NetworkPolicyWithAnnotations", func(t *testing.T) {
		inventoryService, k8sClient := createService(t)

		// Create a network policy with annotations
		networkPolicyWithAnnotations := testNetworkPolicy.DeepCopy()
		networkPolicyWithAnnotations.Annotations = map[string]string{
			"nsx-op/error": "network policy error message",
		}

		// Mock namespace retrieval
		k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.ListOption) error {
			ns, ok := obj.(*corev1.Namespace)
			if !ok {
				return nil
			}
			ns.ObjectMeta = metav1.ObjectMeta{
				Name: "default",
				UID:  "namespace-uid-123",
			}
			return nil
		})

		retry := inventoryService.BuildNetworkPolicy(networkPolicyWithAnnotations)

		assert.False(t, retry)
		assert.Contains(t, inventoryService.pendingAdd, "networkpolicy-uid-123")

		// Verify the network errors are correctly extracted from the annotations
		containerNetworkPolicy := inventoryService.pendingAdd["networkpolicy-uid-123"].(*containerinventory.ContainerNetworkPolicy)
		assert.Equal(t, 1, len(containerNetworkPolicy.NetworkErrors))
		assert.Equal(t, "nsx-op/error:network policy error message", containerNetworkPolicy.NetworkErrors[0].ErrorMessage)
	})
}
