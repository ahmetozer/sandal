# Examples

End-to-end examples for running real workloads with sandal.

## Available examples

- [Home Assistant](homeasistant.md) — run [Home Assistant Core](https://www.home-assistant.io/) from `ghcr.io/home-assistant/home-assistant`, persist `/config` with [`sandal snapshot -i /config`](../commands/snapshot.md), and reuse the resulting squashfs as a portable `-lw` configuration layer.
- [VS Code AI coding agent](vscode-agent.md) — give a VS Code–driven AI coding agent (Claude Code, Copilot, Cursor) an ephemeral sandal environment with [`-tmp`](../commands/run.md), a COW-protected workspace (`-lw "$PWD":/workspace` + `-v "$PWD":/real`), VS Code Remote-SSH or `sandal exec` integration, a baked toolchain snapshot, and a hardened blast-radius profile.

## See also

- [Commands](../commands/index.md) — full reference for every `sandal` sub-command.
- [`sandal run`](../commands/run.md) — flag reference used throughout these examples.
- [`sandal snapshot`](../commands/snapshot.md) — capturing container state into a portable squashfs.
- [`sandal daemon`](../commands/daemon.md) — auto-start and restart-on-exit for production containers.
