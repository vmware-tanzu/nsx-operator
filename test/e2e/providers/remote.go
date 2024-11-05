package providers

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
)

var (
	homedir, _       = os.UserHomeDir()
	remoteKubeconfig = flag.String("remote.kubeconfig", path.Join(homedir, "kube.conf"), "Path of the kubeconfig of the cluster")
)

type RemoteProvider struct {
}

func (p *RemoteProvider) RunCommandOnNode(nodeName string, cmd string) (code int, stdout string, stderr string, err error) {
	cmdRun := exec.Command("bash", "-c", cmd)

	var stdout0, stderr0 bytes.Buffer

	// Set the command's Stdout and Stderr to point to the buffers
	cmdRun.Stdout = &stdout0
	cmdRun.Stderr = &stderr0

	errRun := cmdRun.Run()
	// If there was an error running the command, handle it
	if errRun != nil {
		fmt.Printf("Error running command: %v", errRun)
	}
	stdout = stdout0.String()
	stderr = stderr0.String()
	return
}

func (p *RemoteProvider) GetKubeconfigPath() (string, error) {
	return *remoteKubeconfig, nil
}

// NewRemoteProvider returns an implementation of ProviderInterface which enables tests to run on a remote cluster.
// configPath is unused for the remote provider
func NewRemoteProvider(_ string) (ProviderInterface, error) {
	return &RemoteProvider{}, nil
}
