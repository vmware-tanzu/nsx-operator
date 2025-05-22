package inventory

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/go-vmware-nsxt/common"
	"github.com/vmware/go-vmware-nsxt/containerinventory"
)

func TestCompareContainerApplicationInstance(t *testing.T) {
	testCases := []struct {
		name           string
		pre            interface{}
		cur            containerinventory.ContainerApplicationInstance
		expectedResult map[string]interface{}
	}{
		{
			name: "New resource with all fields",
			pre:  nil,
			cur: containerinventory.ContainerApplicationInstance{
				DisplayName:             "AppInstance1",
				ContainerClusterId:      "Cluster1",
				ContainerProjectId:      "Project1",
				Tags:                    []common.Tag{{Scope: "tag1"}, {Scope: "tag2"}},
				ClusterNodeId:           "Node1",
				ContainerApplicationIds: []string{"App1", "App2"},
				Status:                  "Running",
				OriginProperties:        []common.KeyValuePair{{Key: "ip", Value: "192.168.1.1"}},
			},
			expectedResult: map[string]interface{}{
				"display_name":              "AppInstance1",
				"container_cluster_id":      "Cluster1",
				"container_project_id":      "Project1",
				"tags":                      []common.Tag{{Scope: "tag1"}, {Scope: "tag2"}},
				"cluster_node_id":           "Node1",
				"container_application_ids": []string{"App1", "App2"},
				"status":                    "Running",
				"origin_properties":         []common.KeyValuePair{{Key: "ip", Value: "192.168.1.1"}},
				"network_status":            "",
				"network_errors":            []common.NetworkError(nil),
			},
		},
		{
			name: "Update resource with changed tags",
			pre: containerinventory.ContainerApplicationInstance{
				DisplayName: "AppInstance1",
				Tags:        []common.Tag{{Scope: "tag1"}},
			},
			cur: containerinventory.ContainerApplicationInstance{
				DisplayName: "AppInstance1",
				Tags:        []common.Tag{{Scope: "tag1"}, {Scope: "tag2"}},
			},
			expectedResult: map[string]interface{}{
				"tags": []common.Tag{{Scope: "tag1"}, {Scope: "tag2"}},
			},
		},
		{
			name: "Update resource with changed cluster node ID",
			pre: containerinventory.ContainerApplicationInstance{
				DisplayName:   "AppInstance1",
				ClusterNodeId: "Node1",
			},
			cur: containerinventory.ContainerApplicationInstance{
				DisplayName:   "AppInstance1",
				ClusterNodeId: "Node2",
			},
			expectedResult: map[string]interface{}{
				"cluster_node_id": "Node2",
			},
		},
		{
			name: "Update resource with changed origin properties",
			pre: containerinventory.ContainerApplicationInstance{
				DisplayName:      "AppInstance1",
				OriginProperties: []common.KeyValuePair{{Key: "ip", Value: "192.168.1.1"}},
			},
			cur: containerinventory.ContainerApplicationInstance{
				DisplayName:      "AppInstance1",
				OriginProperties: []common.KeyValuePair{{Key: "ip", Value: "192.168.1.2"}},
			},
			expectedResult: map[string]interface{}{
				"origin_properties": []common.KeyValuePair{{Key: "ip", Value: "192.168.1.2"}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			properties := make(map[string]interface{})
			compareContainerApplicationInstance(tc.pre, tc.cur, properties)
			assert.Equal(t, tc.expectedResult, properties)
		})
	}
}

func TestIsIPChanged(t *testing.T) {
	type testCase struct {
		name     string
		pre      containerinventory.ContainerApplicationInstance
		cur      containerinventory.ContainerApplicationInstance
		expected bool
	}

	buildInstance := func(ipValue string) containerinventory.ContainerApplicationInstance {
		var props []common.KeyValuePair
		if ipValue != "" {
			props = append(props, common.KeyValuePair{
				Key:   "ip",
				Value: ipValue,
			})
		}
		return containerinventory.ContainerApplicationInstance{
			DisplayName:      "test-pod",
			OriginProperties: props,
		}
	}

	tests := []testCase{
		{
			name:     "no ips",
			pre:      buildInstance(""),
			cur:      buildInstance(""),
			expected: false,
		},
		{
			name:     "ip added",
			pre:      buildInstance(""),
			cur:      buildInstance("192.168.1.1"),
			expected: true,
		},
		{
			name:     "ip count changed",
			pre:      buildInstance("10.0.0.1"),
			cur:      buildInstance("10.0.0.1,192.168.1.1"),
			expected: true,
		},
		{
			name:     "single ip same",
			pre:      buildInstance("192.168.1.1"),
			cur:      buildInstance("192.168.1.1"),
			expected: false,
		},
		{
			name:     "single ip different",
			pre:      buildInstance("192.168.1.1"),
			cur:      buildInstance("10.0.0.1"),
			expected: true,
		},
		{
			name:     "dual ips order changed",
			pre:      buildInstance("192.168.1.1,10.0.0.1"),
			cur:      buildInstance("10.0.0.1,192.168.1.1"),
			expected: false,
		},
		{
			name:     "dual ips content changed",
			pre:      buildInstance("192.168.1.1,10.0.0.1"),
			cur:      buildInstance("192.168.1.1,10.0.0.2"),
			expected: true,
		},
		{
			name:     "multiple ips same",
			pre:      buildInstance("a,b,c"),
			cur:      buildInstance("a,b,c"),
			expected: false,
		},
		{
			name:     "multiple ips different",
			pre:      buildInstance("a,b,c"),
			cur:      buildInstance("d,e,f"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isIPChanged(tt.pre, tt.cur)
			if got != tt.expected {
				t.Errorf("isIPChanged() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCompareNetworkPolicy(t *testing.T) {
	testCases := []struct {
		name           string
		pre            interface{}
		cur            containerinventory.ContainerNetworkPolicy
		expectedResult map[string]interface{}
	}{
		{
			name: "New resource with all fields filled",
			pre:  nil,
			cur: containerinventory.ContainerNetworkPolicy{
				DisplayName:        "NetworkPolicy1",
				ContainerClusterId: "Cluster1",
				ContainerProjectId: "Project1",
				Tags:               []common.Tag{{Scope: "tag1"}, {Scope: "tag2"}},
				Spec:               "spec-content",
				OriginProperties:   []common.KeyValuePair{{Key: "annotation", Value: "example"}},
				PolicyType:         NetworkPolicyType,
			},
			expectedResult: map[string]interface{}{
				"display_name":         "NetworkPolicy1",
				"container_cluster_id": "Cluster1",
				"container_project_id": "Project1",
				"tags":                 []common.Tag{{Scope: "tag1"}, {Scope: "tag2"}},
				"spec":                 "spec-content",
				"origin_properties":    []common.KeyValuePair{{Key: "annotation", Value: "example"}},
				"policy_type":          "NETWORK_POLICY",
				"network_status":       "",
				"network_errors":       []common.NetworkError(nil),
			},
		},
		{
			name: "Update resource with changed tags",
			pre: containerinventory.ContainerNetworkPolicy{
				Tags: []common.Tag{{Scope: "tag1"}},
			},
			cur: containerinventory.ContainerNetworkPolicy{
				Tags: []common.Tag{{Scope: "tag1"}, {Scope: "tag2"}},
			},
			expectedResult: map[string]interface{}{
				"tags": []common.Tag{{Scope: "tag1"}, {Scope: "tag2"}},
			},
		},
		{
			name: "Update resource with changed spec",
			pre: containerinventory.ContainerNetworkPolicy{
				Spec: "old-spec-content",
			},
			cur: containerinventory.ContainerNetworkPolicy{
				Spec: "new-spec-content",
			},
			expectedResult: map[string]interface{}{
				"spec": "new-spec-content",
			},
		},
		{
			name: "No changes between pre and cur",
			pre: containerinventory.ContainerNetworkPolicy{
				DisplayName:        "NetworkPolicy1",
				ContainerClusterId: "Cluster1",
				ContainerProjectId: "Project1",
				Tags:               []common.Tag{{Scope: "tag1"}},
				Spec:               "spec-content",
				OriginProperties:   []common.KeyValuePair{{Key: "annotation", Value: "example"}},
			},
			cur: containerinventory.ContainerNetworkPolicy{
				DisplayName:        "NetworkPolicy1",
				ContainerClusterId: "Cluster1",
				ContainerProjectId: "Project1",
				Tags:               []common.Tag{{Scope: "tag1"}},
				Spec:               "spec-content",
				OriginProperties:   []common.KeyValuePair{{Key: "annotation", Value: "example"}},
			},
			expectedResult: map[string]interface{}{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			properties := make(map[string]interface{})
			compareNetworkPolicy(tc.pre, tc.cur, properties)
			assert.Equal(t, tc.expectedResult, properties)
		})
	}
}
func TestCompareContainerApplication(t *testing.T) {
	testCases := []struct {
		name           string
		pre            interface{}
		cur            containerinventory.ContainerApplication
		expectedResult map[string]interface{}
	}{
		{
			name: "New resource with all fields",
			pre:  nil,
			cur: containerinventory.ContainerApplication{
				DisplayName:        "App1",
				ContainerClusterId: "Cluster1",
				ContainerProjectId: "Project1",
				Tags:               []common.Tag{{Scope: "tag1"}, {Scope: "tag2"}},
				Status:             "Running",
				NetworkStatus:      "Ready",
				NetworkErrors:      []common.NetworkError{{ErrorMessage: "none"}},
				OriginProperties:   []common.KeyValuePair{{Key: "key1", Value: "value1"}},
			},
			expectedResult: map[string]interface{}{
				"display_name":         "App1",
				"container_cluster_id": "Cluster1",
				"container_project_id": "Project1",
				"tags":                 []common.Tag{{Scope: "tag1"}, {Scope: "tag2"}},
				"status":               "Running",
				"network_status":       "Ready",
				"network_errors":       []common.NetworkError{{ErrorMessage: "none"}},
				"origin_properties":    []common.KeyValuePair{{Key: "key1", Value: "value1"}},
			},
		},
		{
			name: "Update resource with changed tags",
			pre: containerinventory.ContainerApplication{
				Tags: []common.Tag{{Scope: "tag1"}},
			},
			cur: containerinventory.ContainerApplication{
				Tags: []common.Tag{{Scope: "tag1"}, {Scope: "tag2"}},
			},
			expectedResult: map[string]interface{}{
				"tags": []common.Tag{{Scope: "tag1"}, {Scope: "tag2"}},
			},
		},
		{
			name: "Update resource with changed status",
			pre: containerinventory.ContainerApplication{
				Status: "Stopped",
			},
			cur: containerinventory.ContainerApplication{
				Status: "Running",
			},
			expectedResult: map[string]interface{}{
				"status": "Running",
			},
		},
		{
			name: "Update resource with changed network status",
			pre: containerinventory.ContainerApplication{
				NetworkStatus: "NotReady",
			},
			cur: containerinventory.ContainerApplication{
				NetworkStatus: "Ready",
			},
			expectedResult: map[string]interface{}{
				"network_status": "Ready",
			},
		},
		{
			name: "Update resource with changed network errors",
			pre: containerinventory.ContainerApplication{
				NetworkErrors: []common.NetworkError{{ErrorMessage: "old"}},
			},
			cur: containerinventory.ContainerApplication{
				NetworkErrors: []common.NetworkError{{ErrorMessage: "new"}},
			},
			expectedResult: map[string]interface{}{
				"network_errors": []common.NetworkError{{ErrorMessage: "new"}},
			},
		},
		{
			name: "Update resource with changed origin properties",
			pre: containerinventory.ContainerApplication{
				OriginProperties: []common.KeyValuePair{{Key: "key1", Value: "value1"}},
			},
			cur: containerinventory.ContainerApplication{
				OriginProperties: []common.KeyValuePair{{Key: "key1", Value: "value2"}},
			},
			expectedResult: map[string]interface{}{
				"origin_properties": []common.KeyValuePair{{Key: "key1", Value: "value2"}},
			},
		},
		{
			name: "No changes between pre and cur",
			pre: containerinventory.ContainerApplication{
				DisplayName:        "App1",
				ContainerClusterId: "Cluster1",
				ContainerProjectId: "Project1",
				Tags:               []common.Tag{{Scope: "tag1"}},
				Status:             "Running",
				NetworkStatus:      "Ready",
				NetworkErrors:      []common.NetworkError{{ErrorMessage: "none"}},
				OriginProperties:   []common.KeyValuePair{{Key: "key1", Value: "value1"}},
			},
			cur: containerinventory.ContainerApplication{
				DisplayName:        "App1",
				ContainerClusterId: "Cluster1",
				ContainerProjectId: "Project1",
				Tags:               []common.Tag{{Scope: "tag1"}},
				Status:             "Running",
				NetworkStatus:      "Ready",
				NetworkErrors:      []common.NetworkError{{ErrorMessage: "none"}},
				OriginProperties:   []common.KeyValuePair{{Key: "key1", Value: "value1"}},
			},
			expectedResult: map[string]interface{}{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			properties := make(map[string]interface{})
			compareContainerApplication(tc.pre, tc.cur, properties)
			assert.Equal(t, tc.expectedResult, properties)
		})
	}
}
