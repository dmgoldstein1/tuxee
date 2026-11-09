package terminal

import (
	"context"
	"embed"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"tuxee/internal/identity"
	"tuxee/internal/remotes"
)

//go:embed shell-launch.sh
var shellLauncher string

//go:embed shell.bashrc prompt.bash prompt.zsh prompt.fish shell.zshenv shell.zprofile shell.zshrc shell.zlogin shell.fish shell.env
var shellFiles embed.FS

func prepareShell(user identity.User, shell, command string, prompt bool) (*exec.Cmd, func(), error) {
	if strings.TrimSpace(shell) == "" {
		shell = "bash"
	}
	cleanup := func() {}
	directory := ""
	if prompt || command != "" {
		var err error
		directory, err = os.MkdirTemp("/tmp", "tuxee-shell-*")
		if err != nil {
			return nil, cleanup, err
		}
		cleanup = func() { os.RemoveAll(directory) }
		entries, _ := shellFiles.ReadDir(".")
		for _, entry := range entries {
			if !prompt && strings.HasPrefix(entry.Name(), "prompt.") {
				continue
			}
			data, err := shellFiles.ReadFile(entry.Name())
			switch entry.Name() {
			case "shell.bashrc", "shell.zlogin", "shell.fish", "shell.env":
				data = append(data, []byte("\n"+command+"\n")...)
			}
			path := filepath.Join(directory, strings.Replace(entry.Name(), "shell.z", ".z", 1))
			if err == nil {
				err = os.WriteFile(path, data, 0600)
			}
			if err == nil {
				err = os.Chown(path, int(user.UID), int(user.GID))
			}
			if err != nil {
				cleanup()
				return nil, func() {}, err
			}
		}
		if err := os.Chown(directory, int(user.UID), int(user.GID)); err != nil {
			cleanup()
			return nil, func() {}, err
		}
	}
	cmd := user.Command(context.Background(), "/bin/sh", "-c", shellLauncher, "tuxee-shell", shell, command, directory)
	return cmd, cleanup, nil
}

// prepareRemoteShell builds a PTY-wrapped SSH client for agentless remotes.
// The SSH child runs under the signed-in UID so existing Tailscale/SSH-agent
// credentials apply; no passwords or keys are handled here.
func prepareRemoteShell(user identity.User, remote *remotes.Remote, shell, command string, prompt bool) (*exec.Cmd, func(), error) {
	if remote == nil {
		return prepareShell(user, shell, command, prompt)
	}
	// Remote shells always run through the remote login shell; the local
	// prompt scripts stay local-only to avoid leaking paths into the remote.
	// The initial command is passed through so it runs once on the remote.
	bin, args := remotes.ShellArgs(remote, shell, command)
	cmd := user.Command(context.Background(), bin, args...)
	return cmd, func() {}, nil
}
