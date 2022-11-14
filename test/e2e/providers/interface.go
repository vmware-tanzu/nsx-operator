package providers

// ProviderInterface Hides away specific characteristics of the K8s cluster. This should enable the same tests to be
// run on a variety of providers.
type ProviderInterface interface {
	RunCommandOnNode(nodeName string, cmd string) (code int, stdout string, stderr string, err error)
	GetKubeconfigPath() (string, error)
}
