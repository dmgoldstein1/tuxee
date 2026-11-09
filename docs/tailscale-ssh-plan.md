# Tailscale SSH remote targets — plan

Goal: run the Tuxee container on one VM/host and control different VM(s) over Tailscale SSH, authenticating to the remote solely via Tailscale SSH with no remote username/password stored in Tuxee.

Accepted scope for this plan: both host-mounted and embedded `tailscaled`, multiple saved hosts, agentless-only remote (OpenSSH/SFTP, no remote `tuxee` binary), attempt full parity in v1.

## Implementation status (v1, this change)

Landed: `internal/remotes` (validation, `tailscale ssh`/OpenSSH arg builders, bounded exec under the signed-in UID), per-user `remotes` in settings with `PUT/GET /api/remotes`, `hostId` terminal creation over PTY-wrapped SSH with remote `pwd` for cwd, remote `?host=` system/metrics, remote process list/signal via `ps`/`kill`, core remote file list/stat/mkdir/delete/chmod/rename/move/copy/read/download/upload over SSH, nil-safe remote displays (existing display-unavailable error), and host-aware web plumbing (panels, terminal creation, file operations, transfers) with no new translation keys.

Deferred: visible host-switcher/settings UI (remotes are API-managed in v1), remote trash/archives/editor staging/recursive size (existing invalid-operation error), remote GUI/audio (existing display-unavailable error), remote `WorkingDirectory` beyond `pwd`, and a `tuxee remotes` CLI (per-user settings stay API-managed).

## Current constraints

- Login: `POST /api/login` → `Gate.Authenticate` → `identity.Authenticate` → `exec Self __auth` → `pam_authenticate + pam_acct_mgmt(login)` (`internal/server/server.go:528-557`, `internal/identity/identity.go:57-73`, `internal/identity/pam.go:30-36`).
- All execution assumes local Linux with dropped credentials: `User.Command(Self, "__files"/"__processes"/"__keeper")` (`internal/identity/identity.go:75-80`, `internal/files/client.go:39`, `internal/processes/worker.go:63`, `internal/keeper/worker.go:104`). Server identity never changes.
- Terminals assume local PTY: `pty.Open() + Setsid/Setctty`, `keeper.PTY/Spawn` over Unix `SCM_RIGHTS + SO_PEERCRED`, `WorkingDirectory` via `/proc/PID/cwd` (`internal/terminal/session.go:237-284,426-443`, `internal/keeper/client.go:108-148`).
- Files/monitor/processes assume local fs/procfs: direct syscalls, `/proc/stat,meminfo,mountinfo`, `statfs`, `PidfdSendSignal` (`internal/files/worker.go:86-146`, `internal/monitor/monitor.go:83-125`, `internal/processes/processes.go:76-120,561-631`).
- No SSH client or remote transport exists; WebSocket views attach directly to local PTYs (`internal/AGENTS.md:15`).

## Architecture

Add `internal/remotes/` + `internal/ssh/` (new child of `internal/AGENTS.md`):

```text
Browser → server (local login session) → BackendRouter(hostID)
  local: existing Manager/files/processes/monitor (unchanged)
  ssh:   SSHBackend { sshPool, sftpPool, procPoller }
```

- Terminal v1: add `Transport{localPTY | sshProc}` to `internal/terminal/session.go:72-85`. Remote terminal = local `creack/pty` wrapping a child `tailscale ssh user@host bash -li` (or `ssh user@host`). Reuses `readOutput/writeInput/Resize/Kill(-pgid)` (`session.go:646-789,854,869`). Keeper journal/persistence is reconnect-only remotely; document as limitation.
- Files v1: add `FileTransport` at the `internal/files/client.go:32-78` cut (`local-exec(__files)` vs `ssh-sftp`). List/stat/mkdir/read/write/rename/trash via SFTP; copy/move via remote `cp/mv` exec; archives via remote `tar/7zz` exec with download-fallback.
- Processes/metrics/system v1: SSH exec samplers reading remote `/proc` (`stat,cmdline,smaps_rollup/status`, `stat,meminfo,mounts,net/dev`) plus `uname/os-release`; signals via `ssh kill -TERM/-KILL` with remote start-time guard (no `pidfd` remotely).

## Tailscale modes (both)

1. Host mode (no extra privilege): use host `tailscale` socket/binary or plain `ssh` to `*.ts.net`; container only needs tailnet route. Preferred default.
2. Embedded mode (self-contained): optional `tailscaled` sidecar in image with `TUXEE_TAILSCALE_AUTHKEY`/`TUXEE_TAILSCALE_AUTHKEY_FILE`, `/dev/net/tun` or userspace-net fallback. Never bake keys into layers; strip from child env like `ROOT_PASSWORD` handling (`docker/AGENTS.md:31-43`).

Auth model: Tuxee UI login stays local PAM (container `root`); remote hosts are chosen after login. No remote passwords/keys stored in v1; Tailscale identity owns remote auth. Keeps anonymous surface frozen (`web/login/AGENTS.md`, `internal/server/testdata/login-surface.json`).

## Multi-host model (implemented)

- Remotes are user-owned data in the existing per-user `~/.config/tuxee/settings.json` (`remotes` array): `{id, name, host, user, port, mode}`. There is no separate state-dir file and no server-side secret store.
- Manage via authenticated `PUT/GET /api/remotes` with the same CSRF/origin admission as other mutations. A `tuxee remotes` CLI is deferred.
- Scope by `(loginUID, hostID)`: panel `hostId`, terminal creation (`hostId`), process snapshots (`?host=` with per-snapshot binding), and `?host=` monitor/system readings. Remote `$HOME` is resolved with `ssh pwd` when a terminal directory is not supplied.

## Full-parity attempt and v1 cut

Working in v1: terminals, files/editor, processes, metrics, system info.

GUI/audio agentless requires remote X/VNC daemons plus X11 forwarding, VNC bridging (`internal/server/server.go:637`, `internal/desktop/display.go:54-117`, `internal/desktop/desktop.go:39-107`), XFixes clipboard and shared PulseAudio (`internal/audio/audio.go:364`) — disproportionate without an agent and conflicts with `internal/desktop/AGENTS.md` + `internal/audio/AGENTS.md`. V1 shows a catalogued notice (`Remote GUI/audio not supported agentless`) and disables those controls per remote panel. Remote `restart/update/service` stay local-only.

## Frontend

- Host switcher + per-panel `hostID` badge; persist `panel.hostID` in `model.ts/app.ts`; route `addPanel`, `Open terminal here`, `Browse shell folder`, path links to owning host.
- New `remotes.ts` settings UI; per-host SSH health per component (global `connection.ts` overlay unchanged).
- No login-page changes; Tuxee UI login language/session rules unchanged (`web/AGENTS.md`, `internal/server/AGENTS.md:25`).

## Security

- Client-only change: no new inbound listener, preserves "no SSH listener" contract.
- Tailscale authkeys via env/file only, `0400`, stripped before jobs; no remote secrets in `remotes.json`.
- Tailscale-identity host verification for `*.ts.net`; no `StrictHostKeyChecking=no` for other names.
- Bound dials (~4s), per-host concurrency limits mirroring `identity/gate.go:23-55`. Reuse `Origin + X-Tuxee-CSRF` for remote mutations.

## Verification

- `go test ./internal/ssh ./internal/remotes ./internal/server -race`: mocked `sshd`/SFTP fixture for files/process/monitor parsers, router admission (`hostID`/UID mismatch → 404), settings-namespace isolation.
- `TUXEE_SYSTEM_TEST=1` root with disposable `tuxee-poc-*` + isolated `sshd` as fake remote: real `terminal-transport`, `uploads`, `processes`, `monitoring` against remote host; `login-security.mjs` to pin anonymous surface.
- Docker: extend `test:docker-init` flow + two-container tailnet fixture where available; report skip otherwise.

## Phases

1. `BackendRouter` + `remotes.json` + CLI/API + host switcher; local-only regression.
2. SSH terminal (PTY-wrapped `ssh -tt`) + remote `WorkingDirectory` + history/draft namespacing.
3. SFTP files + editor + trash/zip paths.
4. Remote processes/metrics/system parsers.
5. Host + embedded Tailscale modes + guide/i18n (`docs/guide.md`, `npm run check:i18n`).
6. Hardening + system/browser suites + DOX pass (`internal/AGENTS.md`, `internal/server/AGENTS.md`, `web/AGENTS.md`, `docker/AGENTS.md`).
