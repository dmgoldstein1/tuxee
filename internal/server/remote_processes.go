package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"tuxee/internal/i18n"
	"tuxee/internal/identity"
	"tuxee/internal/processes"
	"tuxee/internal/remotes"
)

func remoteProcessSnapshot(ctx context.Context, user identity.User, r *remotes.Remote, sortBy string) (*processes.Snapshot, error) {
	out, err := remotes.Exec(ctx, user.Command, r, "ps -eo pid,ppid,uid,stat,lstart,rss,args --no-headers")
	if err != nil {
		return nil, err
	}
	now := time.Now()
	items := make([]processes.Process, 0, 256)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		uid64, err3 := strconv.ParseUint(fields[2], 10, 32)
		if err1 != nil || err2 != nil || err3 != nil || pid <= 0 {
			continue
		}
		state := fields[3]
		// lstart is 5 fields: Day Mon DD HH:MM:SS YYYY
		start := strings.Join(fields[4:9], " ")
		rest := ""
		if len(fields) > 10 {
			rest = strings.Join(fields[10:], " ")
		}
		rssKB := uint64(0)
		if n, err := strconv.ParseUint(fields[9], 10, 64); err == nil {
			rssKB = n * 1024
		}
		name := rest
		if i := strings.Index(rest, " "); i > 0 {
			name = rest[:i]
		}
		if name == "" {
			name = fmt.Sprintf("pid %d", pid)
		}
		if len(name) > 250 {
			name = name[:250]
		}
		cmd := rest
		if len(cmd) > 250 {
			cmd = cmd[:250]
		}
		ram := rssKB
		items = append(items, processes.Process{
			Ref:          processes.Ref{PID: pid, Start: start},
			Parent:       ppid,
			Name:         name,
			CommandLine:  cmd,
			UID:          uint32(uid64),
			State:        state,
			RAM:          &ram,
			EstimatedRAM: true,
			CanSignal:    true,
		})
		if len(items) >= 5000 {
			break
		}
	}
	snapshot := &processes.Snapshot{Time: now, Processes: items}
	return snapshot, nil
}

func remoteSignalProcesses(ctx context.Context, user identity.User, r *remotes.Remote, refs []processes.Ref, signal int) error {
	if signal != 15 && signal != 9 {
		return i18n.New("backend.server.invalidProcessSignal")
	}
	pids := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.PID > 1 {
			pids = append(pids, strconv.Itoa(ref.PID))
		}
	}
	if len(pids) == 0 {
		return nil
	}
	cmd := "kill -TERM " + strings.Join(pids, " ")
	if signal == 9 {
		cmd = "kill -KILL " + strings.Join(pids, " ")
	}
	_, err := remotes.Exec(ctx, user.Command, r, cmd)
	return err
}
