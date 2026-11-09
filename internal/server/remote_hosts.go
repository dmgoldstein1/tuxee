package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
	"tuxee/internal/i18n"
	"tuxee/internal/identity"
	"tuxee/internal/monitor"
	"tuxee/internal/remotes"
)

// remoteForRequest resolves ?host=<remoteID> via the caller's saved remotes.
// Empty host means local execution; unknown IDs are rejected before any SSH.
func (s *Server) remoteForRequest(r *http.Request, session *login) (*remotes.Remote, error) {
	id := strings.TrimSpace(r.URL.Query().Get("host"))
	if id == "" {
		return nil, nil
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	settings, err := LoadSettings(r.Context(), session.user)
	if err != nil {
		return nil, err
	}
	found := remotes.Lookup(settings.Remotes, id)
	if found == nil {
		return nil, i18n.New("backend.server.invalidRequestBody")
	}
	snapshot := *found
	return &snapshot, nil
}

func remoteForID(ctx context.Context, user identity.User, id string) (*remotes.Remote, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	settings, err := LoadSettings(ctx, user)
	if err != nil {
		return nil, err
	}
	found := remotes.Lookup(settings.Remotes, id)
	if found == nil {
		return nil, i18n.New("backend.server.invalidRequestBody")
	}
	snapshot := *found
	return &snapshot, nil
}

func remoteHomeDirectory(ctx context.Context, user identity.User, r *remotes.Remote) (string, error) {
	out, err := remotes.Exec(ctx, user.Command, r, "pwd")
	if err != nil {
		return "", err
	}
	home := strings.TrimSpace(out)
	if !strings.HasPrefix(home, "/") || strings.ContainsRune(home, 0) {
		return "", i18n.New("backend.server.invalidRequestBody")
	}
	return home, nil
}

func remoteSystemInfo(ctx context.Context, version string, user identity.User, r *remotes.Remote) (systemInfo, error) {
	info, err := readSystemInfo(version, user)
	if err != nil {
		return info, err
	}
	out, err := remotes.Exec(ctx, user.Command, r, "uname -srmn; echo ---; cat /etc/os-release 2>/dev/null || cat /usr/lib/os-release 2>/dev/null || true")
	if err != nil {
		return info, err
	}
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) != "" {
		parts := strings.Fields(lines[0])
		if len(parts) >= 4 {
			// uname -srmn prints: sysname release machine nodename.
			info.OS = parts[0]
			info.Kernel = parts[1]
			info.Architecture = parts[2]
			info.Hostname = parts[3]
		} else if len(parts) >= 1 {
			info.OS = parts[0]
		}
	}
	joined := strings.Join(lines, "\n")
	if name := osReleaseName(joined); name != "" {
		info.OS = name
	}
	info.Home = ""
	info.Shell = ""
	return info, nil
}

func remoteMetrics(ctx context.Context, user identity.User, r *remotes.Remote) (*monitor.Snapshot, error) {
	out, err := remotes.Exec(ctx, user.Command, r, "nproc 2>/dev/null; echo ---; cat /proc/meminfo 2>/dev/null | head -20")
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "---")
	cores := 1.0
	if len(parts) > 0 {
		if n, err := strconv.Atoi(strings.TrimSpace(strings.Split(parts[0], "\n")[0])); err == nil && n > 0 && n < 8192 {
			cores = float64(n)
		}
	}
	snapshot := &monitor.Snapshot{Time: time.Now(), Scope: "remote", Disks: []monitor.Disk{}, Networks: []monitor.Network{}}
	snapshot.CPU.Cores = cores
	if len(parts) > 1 {
		fields := map[string]uint64{}
		for _, line := range strings.Split(parts[1], "\n") {
			name, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			f := strings.Fields(strings.TrimSpace(value))
			if len(f) == 0 {
				continue
			}
			n, err := strconv.ParseUint(f[0], 10, 64)
			if err != nil {
				continue
			}
			fields[strings.TrimSpace(name)] = n * 1024
		}
		if fields["MemTotal"] > 0 {
			total := fields["MemTotal"]
			avail := fields["MemAvailable"]
			if avail == 0 || avail > total {
				avail = fields["MemFree"]
			}
			if avail > total {
				avail = 0
			}
			used := total - avail
			snapshot.Memory = &monitor.Capacity{Total: total, Used: used, Available: avail}
			if total > 0 {
				p := float64(used) / float64(total) * 100
				snapshot.Memory.Percent = &p
			}
		}
		if fields["SwapTotal"] > 0 {
			total := fields["SwapTotal"]
			free := fields["SwapFree"]
			if free > total {
				free = 0
			}
			used := total - free
			snapshot.Swap = &monitor.Capacity{Total: total, Used: used, Available: free}
			if total > 0 {
				p := float64(used) / float64(total) * 100
				snapshot.Swap.Percent = &p
			}
		}
	}
	return snapshot, nil
}
