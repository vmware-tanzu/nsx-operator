package vpc

type VPCNetworkConfigInfo struct {
	Name                    string
	DefaultGatewayPath      string
	EdgeClusterPath         string
	NsxtProject             string
	ExternalIPv4Blocks      []string
	PrivateIPv4CIDRs        []string
	DefaultIPv4SubnetSize   int
	DefaultSubnetAccessMode string
}
