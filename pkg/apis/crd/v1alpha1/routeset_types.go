package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// RouteSet describe a set of routes.
type RouteSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   RouteSetSpec   `json:"spec"`
	Status RouteSetStatus `json:"status,omitempty"`
}

// RouteSetSpec defines the desired state of RouteSet.
type RouteSetSpec struct {
	// Routes is the set of desired routes.
	Routes []Route `json:"routes"`
}

// Route defines a route entry.
type Route struct {
	// Name is the name of this route entry.
	Name string `json:"name"`
	// Destination is the CIDR block used for the destination match.
	// It is network address in CIDR format, support both IPv4 and IPv6.
	Destination string `json:"destination"`
	// Target is the IP address used to determine where traffic goes to.
	// Support both IPv4 and IPv6, and it needs to be consistent with Destination.
	Target string `json:"target"`
}

// RouteSetStatus defines the realized state of RouteSet.
type RouteSetStatus struct {
	// Routes is the set of realized routes.
	Routes []Route `json:"routes"`
	// Conditions defines current state of the RouteSet.
	Conditions []RouteSetCondition `json:"conditions"`
}

// RouteSetConditionType describes the RouteSet condition type.
type RouteSetConditionType string

const (
	// RouteSetConditionTypeReady means RouteSet is healthy.
	RouteSetConditionTypeReady RouteSetConditionType = "Ready"
)

// RouteSetCondition defines the condition for the RouteSet.
type RouteSetCondition struct {
	// RouteSetConditionType defines the type of condition.
	Type RouteSetConditionType `json:"type"`
	// Status shows the status of condition, one of True, False, Unknown.
	Status corev1.ConditionStatus `json:"status"`
	// Reason shows a brief reason of condition.
	Reason string `json:"reason,omitempty"`
	// Message shows a human readable message about the condition.
	Message string `json:"message,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// RouteSetList is a list of RouteSet.
type RouteSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RouteSet `json:"items"`
}
