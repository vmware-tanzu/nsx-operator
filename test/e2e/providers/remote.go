package providers

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"

	"github.com/kevinburke/ssh_config"
)

var (
	homedir, _       = os.UserHomeDir()
	sshConfig        = flag.String("remote.sshconfig", path.Join(homedir, "ssh-config"), "Path of the sshconfig")
	remoteKubeconfig = flag.String("remote.kubeconfig", path.Join(homedir, "kube.conf"), "Path of the kubeconfig of the cluster")
)

func getSSHConfig() (*ssh_config.Config, error) {
	f, err := os.Open(*sshConfig)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	stat, _ := f.Stat()
	if stat.IsDir() {
		return nil, fmt.Errorf("%s is a directory", *sshConfig)
	}
	return ssh_config.Decode(f)
}

type RemoteProvider struct {
	sshConfig *ssh_config.Config
}

func (p *RemoteProvider) RunCommandOnNode(nodeName string, cmd string) (code int, stdout string, stderr string, err error) {
	cmdRun := exec.Command("bash", "-c", cmd)

	var stdout0, stderr0 bytes.Buffer

	// Set the command's Stdout and Stderr to point to the buffers
	cmdRun.Stdout = &stdout0
	cmdRun.Stderr = &stderr0

	err = cmdRun.Run()
	// If there was an error running the command, handle it
	if err != nil {
		fmt.Printf("Error running command: %v", err)
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
	sshConfig, err := getSSHConfig()
	if err != nil {
		return nil, err
	}
	return &RemoteProvider{sshConfig: sshConfig}, nil
}
