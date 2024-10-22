package providers

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"

	"github.com/kevinburke/ssh_config"
	// "golang.org/x/crypto/ssh"
)

var (
	homedir, _       = os.UserHomeDir()
	sshConfig        = flag.String("remote.sshconfig", path.Join(homedir, "ssh-config"), "Path of the sshconfig")
	remoteKubeconfig = flag.String("remote.kubeconfig", path.Join(homedir, "kube.conf"), "Path of the kubeconfig of the cluster")
)

// func convertConfig(inConfig *ssh_config.Config, name string) (string, *ssh.ClientConfig, error) {
// 	if inConfig == nil {
// 		return "", nil, fmt.Errorf("input config is nil")
// 	}
//
// 	getFromKeyStrict := func(key string) (string, error) {
// 		v, err := inConfig.Get(name, key)
// 		if err != nil {
// 			return "", fmt.Errorf("error when retrieving '%s' for '%s' in SSH config: %v", key, name, err)
// 		}
// 		if v == "" {
// 			return "", fmt.Errorf("unable to find '%s' for '%s' in SSH config", key, name)
// 		}
// 		return v, nil
// 	}
//
// 	keyList := []string{"HostName", "Port", "User"}
// 	values := make(map[string]string)
//
// 	for _, key := range keyList {
// 		if value, err := getFromKeyStrict(key); err != nil {
// 			return "", nil, err
// 		} else {
// 			values[key] = value
// 		}
// 	}
//
// 	// identityFile := values["IdentityFile"]
// 	// // Read the private key identified by identityFile.
// 	// key, err := os.ReadFile(identityFile)
// 	// if err != nil {
// 	// 	return "", nil, fmt.Errorf("unable to read private key from file '%s': %v", identityFile, err)
// 	// }
// 	// // Create the Signer for this private key.
// 	// signer, err := ssh.ParsePrivateKey(key)
// 	// if err != nil {
// 	// 	return "", nil, fmt.Errorf("unable to parse private key from file '%s': %v", identityFile, err)
// 	// }
// 	// #nosec G106: we are using ssh.InsecureIgnoreHostKey, but this is test code
// 	config := &ssh.ClientConfig{
// 		User: values["User"],
// 		// Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
// 		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
// 	}
// 	host := fmt.Sprintf("%s:%s", values["HostName"], values["Port"])
// 	return host, config, nil
// }

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
	// host, clientCfg, err := convertConfig(p.sshConfig, nodeName)
	// if err != nil {
	// 	return 0, "", "", err
	// }
	// return exec.RunSSHCommand(host, clientCfg, cmd)
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
