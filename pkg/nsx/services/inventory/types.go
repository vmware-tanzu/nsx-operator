package inventory

type InventoryType string

const (
	// ContainerCluster represents the inventory type for a container cluster,
	// which is a collection of container nodes managed as a single entity.
	ContainerCluster InventoryType = "ContainerCluster"
	// ContainerClusterNode represents the inventory type for a node within a container cluster.
	// Each node is an individual unit of compute resources.
	ContainerClusterNode InventoryType = "ContainerClusterNode"
	// ContainerProject represents the inventory type for a containerized project,
	// typically mapping to a Kubernetes namespace or similar concept.
	ContainerProject InventoryType = "ContainerProject"
	// ContainerApplication represents the inventory type for an application
	// typically mapping to cluster services.
	ContainerApplication InventoryType = "ContainerApplication"
	// ContainerApplicationInstance represents the inventory type for a specific instance
	// typically mapping to pods.
	ContainerApplicationInstance InventoryType = "ContainerApplicationInstance"
	// ContainerNetworkPolicy represents the inventory type for network policies
	// typically mapping to Kubernetes network policies.
	ContainerNetworkPolicy InventoryType = "ContainerNetworkPolicy"
	ContainerIngressPolicy InventoryType = "ContainerIngressPolicy"

	// InventoryClusterTypeWCP Inventory cluster type
	InventoryClusterTypeWCP = "WCP"
	InventoryClusterCNIType = "NCP"

	// NetworkStatusHealthy Inventory network status
	NetworkStatusHealthy   = "HEALTHY"
	NetworkStatusUnhealthy = "UNHEALTHY"
	NetworkPolicyType      = "NETWORK_POLICY"

	// InventoryInfraTypeVsphere Inventory infra
	InventoryInfraTypeVsphere = "vSphere"

	InventoryMaxDisTags = 20
	InventoryK8sPrefix  = "dis:k8s:"
	MaxTagLen           = 256
	MaxResourceTypeLen  = 128

	operationCreate = "CREATE"
	operationUpdate = "UPDATE"
	operationDelete = "DELETE"
	operationNone   = "NONE"

	InventoryStatusUp      = "UP"
	InventoryStatusDown    = "DOWN"
	InventoryStatusUnknown = "UNKNOWN"

	NcpLbError        = "ncp/error.loadbalancer"
	NcpLbPortError    = "ncp/error.loadbalancer.unrealized_ports"
	NcpLbEpError      = "ncp/error.loadbalancer_endpoints"
	NcpDlbError       = "ncp/error.distributed_loadbalancer"
	NcpSnatError      = "ncp/error.snat"
	NcpAccessLogError = "ncp/error.vc_access_log"
)

type InventoryKey struct {
	InventoryType InventoryType
	ExternalId    string
	Key           string
}

var ServiceNCPErrors = []string{NcpLbError, NcpLbPortError, NcpLbEpError, NcpDlbError, NcpSnatError, NcpAccessLogError}
