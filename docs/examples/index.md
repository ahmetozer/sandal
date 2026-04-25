# Examples

End-to-end examples for running real workloads with sandal.

## Available examples

- [Home Assistant](homeasistant.md) — run [Home Assistant Core](https://www.home-assistant.io/) from `ghcr.io/home-assistant/home-assistant`, persist `/config` with [`sandal snapshot -i /config`](../commands/snapshot.md), and reuse the resulting squashfs as a portable `-lw` configuration layer.

## See also

- [Commands](../commands/index.md) — full reference for every `sandal` sub-command.
- [`sandal run`](../commands/run.md) — flag reference used throughout these examples.
- [`sandal snapshot`](../commands/snapshot.md) — capturing container state into a portable squashfs.
- [`sandal daemon`](../commands/daemon.md) — auto-start and restart-on-exit for production containers.
