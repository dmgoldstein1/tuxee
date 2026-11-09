Parent DOX: [internal](../AGENTS.md).

# Purpose

Agentless Tailscale SSH remote targets for terminals, files, processes and metrics.

# Ownership

Own remote definitions, validation, SSH command construction and bounded remote
execution. Delegate local Linux work to identity, files, processes and terminal.

# Local Contracts

- Remotes are user-owned data inside the existing per-user
  `~/.config/tuxee/settings.json` (`remotes` array); no new state file, no
  server-side secrets. Remote auth uses the Tailscale daemon/SSH agent only;
  never accept or store remote passwords, keys or authkeys in settings or APIs.
- Validation is shared by load and save: IDs `[a-z0-9][a-z0-9-_]{0,63}`,
  names 1–64 chars, hosts 1–253 chars without NUL/whitespace/control bytes,
  users 1–32 chars Linux username shape, ports 1–65535, mode
  `tailscale` or `ssh`. IDs are unique per user.
- SSH runs under the signed-in Linux UID via `identity.User.Command` with
  `BatchMode=yes`, connection timeouts and no password prompts, so Linux and
  existing SSH-agent/Tailscale credentials enforce access. Never fall back to
  password authentication or `StrictHostKeyChecking=no` for non-Tailscale names.
- `tailscale` mode shells out to `tailscale ssh`; `ssh` mode uses OpenSSH to a
  Tailscale IP/hostname. Both work with the host-mounted daemon or an embedded
  `tailscaled` sidecar; this package does not start daemons.
- Terminals wrap the SSH child in a local PTY; keeper guardians, local X/VNC
  displays and shared PulseAudio stay local-only. Remote `WorkingDirectory`
  resolves via `pwd` over SSH, never local `/proc/PID/cwd`.

# Work Guidance

Keep remote execution small, bounded and agentless. Reuse existing i18n keys;
external SSH diagnostics remain literal data.

# Verification

Run `go test ./internal/remotes -v`. Root-only SSH fixtures live with their
owners (`terminal`, `server`, `processes`).

# Child DOX Index

No children.
