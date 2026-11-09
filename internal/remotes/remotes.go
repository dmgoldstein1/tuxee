package remotes

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"tuxee/internal/i18n"
)

// Remote describes one agentless SSH target reachable over Tailscale.
// Auth always uses the Tailscale daemon/SSH agent; no passwords or keys
// are stored here or accepted by the API.
type Remote struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Host string `json:"host"`
	User string `json:"user"`
	Port int    `json:"port"`
	Mode string `json:"mode"`
}

const (
	maxRemoteIDLen   = 64
	maxRemoteNameLen = 64
	maxRemoteHostLen = 253
	maxRemoteUserLen = 32
	maxRemotes       = 32
)

// NewID is intentionally not used for deterministic tests; callers may supply IDs.
func Validate(list []Remote) error {
	if len(list) > maxRemotes {
		return i18n.New("backend.server.invalidRequestBody")
	}
	seen := make(map[string]bool, len(list))
	for i := range list {
		r := &list[i]
		r.ID = strings.TrimSpace(r.ID)
		r.Name = strings.TrimSpace(r.Name)
		r.Host = strings.TrimSpace(r.Host)
		r.User = strings.TrimSpace(r.User)
		if r.Mode == "" {
			r.Mode = "tailscale"
		}
		if len(r.ID) < 1 || len(r.ID) > maxRemoteIDLen || strings.ContainsRune(r.ID, 0) {
			return i18n.New("backend.server.invalidRequestBody")
		}
		for _, c := range r.ID {
			if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
				continue
			}
			return i18n.New("backend.server.invalidRequestBody")
		}
		if r.ID[0] == '-' || r.ID[0] == '_' {
			return i18n.New("backend.server.invalidRequestBody")
		}
		if seen[r.ID] {
			return i18n.New("backend.server.invalidRequestBody")
		}
		seen[r.ID] = true
		if r.Name == "" || len(r.Name) > maxRemoteNameLen || strings.ContainsRune(r.Name, 0) {
			return i18n.New("backend.server.invalidRequestBody")
		}
		if r.Host == "" || len(r.Host) > maxRemoteHostLen || strings.ContainsRune(r.Host, 0) {
			return i18n.New("backend.server.invalidRequestBody")
		}
		for _, c := range r.Host {
			if c <= ' ' || c == 0x7f {
				return i18n.New("backend.server.invalidRequestBody")
			}
		}
		if r.User == "" || len(r.User) > maxRemoteUserLen || strings.ContainsRune(r.User, 0) {
			return i18n.New("backend.server.invalidRequestBody")
		}
		for j, c := range r.User {
			ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || j > 0 && c >= 'A' && c <= 'Z'
			if !ok {
				return i18n.New("backend.server.invalidRequestBody")
			}
		}
		if r.Port < 1 || r.Port > 65535 {
			return i18n.New("backend.server.invalidRequestBody")
		}
		if r.Mode != "tailscale" && r.Mode != "ssh" {
			return i18n.New("backend.server.invalidRequestBody")
		}
	}
	return nil
}

// Lookup finds a remote by ID in an already validated list.
func Lookup(list []Remote, id string) *Remote {
	if id == "" {
		return nil
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

func target(r *Remote) string {
	if strings.Contains(r.Host, "@") {
		return r.Host
	}
	return r.User + "@" + r.Host
}

// ShellArgs builds the interactive shell command for a remote terminal.
// The caller runs it under the signed-in UID so SSH/Tailscale credentials apply.
func ShellArgs(r *Remote, shell, command string) (string, []string) {
	if strings.TrimSpace(shell) == "" {
		shell = "bash"
	}
	remoteCmd := shell + " -li"
	if strings.TrimSpace(command) != "" {
		remoteCmd = shell + " -li -c " + quoteForRemote(command)
	}
	if r.Mode == "ssh" {
		args := []string{"-tt", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "PasswordAuthentication=no", "-p", itoa(r.Port), target(r), remoteCmd}
		return "ssh", args
	}
	args := []string{"ssh", target(r), "--", remoteCmd}
	return "tailscale", args
}

// ExecArgs builds a non-interactive remote command execution.
func ExecArgs(r *Remote, command string) (string, []string) {
	if r.Mode == "ssh" {
		return "ssh", []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "PasswordAuthentication=no", "-p", itoa(r.Port), target(r), command}
	}
	return "tailscale", []string{"ssh", target(r), "--", "sh", "-c", command}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func quoteForRemote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func Exec(ctx context.Context, newCmd func(ctx context.Context, bin string, args ...string) *exec.Cmd, r *Remote, command string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	bin, args := ExecArgs(r, command)
	cmd := newCmd(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		if len(detail) > 2000 {
			detail = detail[:2000]
		}
		return "", i18n.Wrap("backend.server.invalidRequestBody", fmt.Errorf("%w: %s", err, detail), nil)
	}
	out := stdout.String()
	if len(out) > 1<<20 {
		out = out[:1<<20]
	}
	return out, nil
}

// TailscaleAvailable reports whether a tailscale client binary exists.
func TailscaleAvailable() bool {
	_, err := exec.LookPath("tailscale")
	return err == nil
}
