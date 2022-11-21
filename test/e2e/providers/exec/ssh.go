package exec

import (
	"bytes"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// RunSSHCommand runs the provided SSH command on the specified host. Returns the exit code of the
// command, along with the contents of stdout and stderr as strings. Note that if the command
// returns a non-zero error code, this function does not report it as an error.
func RunSSHCommand(host string, config *ssh.ClientConfig, cmd string) (code int, stdout string, stderr string, err error) {
	client, err := ssh.Dial("tcp", host, config)
	if err != nil {
		return 0, "", "", fmt.Errorf("cannot establish SSH connection to host: %v", err)
	}
	session, err := client.NewSession()
	if err != nil {
		return 0, "", "", fmt.Errorf("cannot create SSH session: %v", err)
	}
	defer func(session *ssh.Session) {
		_ = session.Close()
	}(session)

	var stdoutB, stderrB bytes.Buffer
	session.Stdout = &stdoutB
	session.Stderr = &stderrB
	if err := session.Run(cmd); err != nil {
		switch e := err.(type) {
		case *ssh.ExitMissingError:
			return 0, "", "", fmt.Errorf("did not get an exit status for SSH command: %v", e)
		case *ssh.ExitError:
			// SSH operation successful, but command returned error code
			return e.ExitStatus(), stdoutB.String(), stderrB.String(), nil
		default:
			return 0, "", "", fmt.Errorf("unknown error when executing SSH command: %v", err)
		}
	}
	// command is successful
	return 0, stdoutB.String(), stderrB.String(), nil
}
