# Home Assistant

Run [Home Assistant Core](https://www.home-assistant.io/) inside a sandal container straight from the official `ghcr.io/home-assistant/home-assistant` image — no Docker, no Home Assistant OS, just sandal and the static binary.

This guide walks through:

- Pulling and running the Home Assistant image with [`sandal run`](../commands/run.md)
- Persisting the `/config` directory with a volume mount or [`sandal snapshot`](../commands/snapshot.md)
- Auto-starting the container at boot with [`sandal daemon`](../commands/daemon.md)

## Quick start

The fastest way to verify everything works — pull the image and expose Home Assistant on port `8123`:

``` { .bash title="Try Home Assistant in one command" }
sandal run --rm -lw ghcr.io/home-assistant/home-assistant:stable \
    -name homeassistant \
    -p 0.0.0.0:8123:8123
```

Open <http://localhost:8123> in your browser. The image's default `ENTRYPOINT` (`/init`) starts s6-overlay and brings Home Assistant up — sandal reads `ENTRYPOINT`, `CMD`, `ENV`, and `WorkingDir` directly from the OCI manifest, so no extra command is needed after the image reference.

!!! note "Tags"
    `ghcr.io/home-assistant/home-assistant` publishes architecture-specific tags. `:stable` resolves to the latest stable release for your architecture. Use `:beta` for pre-releases, `:dev` for the development branch, or pin a specific version like `:2026.4`.

`--rm` deletes the container's overlay on exit. Anything Home Assistant wrote — your onboarding account, the configuration database, automations — is gone. The next two sections show how to keep it.

## Persisting `/config` with a volume

The simplest persistence pattern is a host-path bind mount. Home Assistant writes everything stateful under `/config`; mount a host directory there and you can stop, restart, or rebuild the container without losing data.

``` { .bash title="Run Home Assistant with a host-mounted config dir" }
mkdir -p /mnt/homeassistant/config

sandal run -lw ghcr.io/home-assistant/home-assistant:stable \
    -name homeassistant \
    -p 0.0.0.0:8123:8123 \
    -v /mnt/homeassistant/config:/config
```

The `-v` flag accepts `host:container` form. Anything Home Assistant writes under `/config` (e.g. `configuration.yaml`, `home-assistant_v2.db`, `.storage/`) lands in `/mnt/homeassistant/config` on the host and is durable across container removals.

??? info "Why a volume instead of the overlay?"
    Sandal containers default to copy-on-write: writes go to the container's *change dir* and merge with the lower layer at read time. That's perfect for ephemeral changes, but the SQLite database and recorder writes Home Assistant performs are heavy and constant — bind-mounting `/config` lets them hit the host filesystem directly with no overlay overhead, and makes the data trivially backup-able with regular host tools.

## Persisting changes with `sandal snapshot`

If you don't want a host bind mount — for example, you want to keep the entire Home Assistant state as a single portable squashfs file you can copy to another machine — use [`sandal snapshot`](../commands/snapshot.md) instead.

`sandal snapshot` captures the container's *change dir* (everything the container wrote on top of its image) into a `.sqfs` image. On the next `sandal run` with the same `-name`, that snapshot is automatically reattached as a lower layer and your previous state reappears inside the container.

### First run — write some state

``` { .bash title="Start Home Assistant; complete onboarding in the browser" }
sandal run -lw ghcr.io/home-assistant/home-assistant:stable \
    -name homeassistant \
    -p 0.0.0.0:8123:8123
```

While the container is running, complete the onboarding wizard at <http://localhost:8123>. Home Assistant writes the new owner account, integrations, and the recorder DB to `/config` inside the container.

### Save a snapshot — config only

In a second terminal, capture **just the `/config` directory** into a portable squashfs. Excluding everything else keeps Python caches, apk indexes, and `/tmp` writes out of the image so you end up with a small, self-contained "Home Assistant configuration" file:

``` { .bash title="Snapshot only /config" }
sandal snapshot -i /config homeassistant
/var/lib/sandal/snapshot/homeassistant.sqfs
```

`-i /config` tells sandal to include only paths under `/config`; the resulting squashfs holds your `configuration.yaml`, `.storage/`, the recorder DB, dashboards, and automations — and nothing else. By default the file lands at `SANDAL_SNAPSHOT_DIR/<name>.sqfs` (`/var/lib/sandal/snapshot/homeassistant.sqfs`). Use `-f` to write it somewhere else — handy for backups onto a USB drive or an SD card:

``` { .bash title="Config snapshot to a custom path" }
sandal snapshot -i /config -f /mnt/usb/ha-config-2026-04-25.sqfs homeassistant
```

### Reuse the config snapshot as a `-lw` layer

Because the snapshot is a regular squashfs image, you can mount it back into a fresh container with `-lw` — the same flag you use for the Home Assistant image. Stack the config squashfs on top of the upstream image and the next `sandal run` boots with your saved configuration in place:

``` { .bash title="Stop the running container" }
sandal stop homeassistant
```

``` { .bash title="Run a fresh container with the saved config layered in" }
sandal run --rm \
    -lw ghcr.io/home-assistant/home-assistant:stable \
    -lw /var/lib/sandal/snapshot/homeassistant.sqfs \
    -name homeassistant \
    -p 0.0.0.0:8123:8123
```

The right-most `-lw` wins for `ENTRYPOINT`/`CMD`/`WorkingDir`, but our config snapshot doesn't define any of those — it's just a `/config` tree — so Home Assistant's own entrypoint still runs, and `/config` is now populated from the snapshot. This is the **recommended** pattern: your configuration is a single portable file, and you can ship it to another machine, version it, or roll back by simply swapping which `.sqfs` file you pass to `-lw`.

``` { .bash title="Run with a snapshot stored on removable media" }
sandal run --rm \
    -lw ghcr.io/home-assistant/home-assistant:stable \
    -lw /mnt/usb/ha-config-2026-04-25.sqfs \
    -name homeassistant \
    -p 0.0.0.0:8123:8123
```

??? info "Snapshot auto-attach vs explicit `-lw`"
    Sandal auto-attaches the default snapshot file at `SANDAL_SNAPSHOT_DIR/<name>.sqfs` when a container runs under the same `-name`. Passing the snapshot explicitly with `-lw` is the more flexible form: you can pin a specific config file, ship it to another host, or stack multiple config snapshots together. The two mechanisms don't conflict, but using `-lw` makes the dependency obvious in the command line.

??? info "Successive snapshots are merged"
    `sandal snapshot -i /config homeassistant` is safe to re-run — sandal mounts the previous `homeassistant.sqfs` as a read-only overlay underneath the current change dir before squashing, so accumulated `/config` changes are preserved across snapshots. See [How It Works](../commands/snapshot.md#how-it-works) for the data flow.

## Combining a volume and a snapshot

The two persistence mechanisms compose. A common production pattern is:

- **Volume** for `/config` — fast direct-to-disk SQLite writes, easy host-side backup
- **Snapshot** for system-level changes — extra apk packages, custom Python deps, tweaks under `/etc`

``` { .bash title="Volume for state, snapshot for system tweaks" }
sandal run -lw ghcr.io/home-assistant/home-assistant:stable \
    -name homeassistant \
    -p 0.0.0.0:8123:8123 \
    -v /mnt/homeassistant/config:/config

# After installing extra packages or editing system files:
sandal snapshot -e /config homeassistant
```

`-e /config` excludes the config dir from the snapshot — it lives on the host volume, so there's no point shoving it into the squashfs too.

If you'd rather keep `/config` inside a squashfs (no host volume) and capture system tweaks separately, take two snapshots with disjoint scopes and stack both with `-lw`:

``` { .bash title="Two scoped snapshots stacked back as -lw layers" }
sandal snapshot -i /config -f /var/lib/sandal/snapshot/ha-config.sqfs homeassistant
sandal snapshot -e /config -f /var/lib/sandal/snapshot/ha-system.sqfs homeassistant

sandal run --rm \
    -lw ghcr.io/home-assistant/home-assistant:stable \
    -lw /var/lib/sandal/snapshot/ha-system.sqfs \
    -lw /var/lib/sandal/snapshot/ha-config.sqfs \
    -name homeassistant \
    -p 0.0.0.0:8123:8123
```

## Auto-starting at boot

For a real home installation you want Home Assistant up after every reboot. The sandal daemon handles this with the `-d` (detached) and `--startup` flags.

### Install the daemon service

``` { .bash title="One-time service install (systemd or OpenRC)" }
sudo sandal daemon -install
sudo service sandal start
```

See [`sandal daemon`](../commands/daemon.md) for service-management details.

### Register Home Assistant as a startup container

``` { .bash title="Register Home Assistant as a daemon-managed container" }
sudo sandal run -d --startup \
    -lw ghcr.io/home-assistant/home-assistant:stable \
    -name homeassistant \
    -p 0.0.0.0:8123:8123 \
    -v /mnt/homeassistant/config:/config
```

- `-d` runs the container in the background.
- `--startup` registers it with the daemon so it's re-provisioned on boot and **automatically restarted if the process exits**.

The daemon proxies `SIGTERM` / `SIGINT` / `SIGQUIT` to the container during shutdown and gives Home Assistant 30 seconds to exit gracefully before sending `SIGKILL` — enough time for the recorder to flush state.

### Check status, restart, stop

``` { .bash title="Day-to-day operations" }
sandal ps                       # see all containers and their status
sandal exec homeassistant -- ha core info   # run a command inside
sandal rerun homeassistant      # restart with the original arguments
sandal stop homeassistant       # graceful stop (SIGTERM, 30s timeout)
sandal kill homeassistant       # SIGKILL if it's wedged
```

See [`sandal ps`](../commands/ps.md), [`sandal exec`](../commands/exec.md), [`sandal stop`](../commands/stop.md), and [`sandal kill`](../commands/kill.md) for the full flag set.

## Updating Home Assistant

Sandal caches each pulled image as a single squashfs in `SANDAL_IMAGE_DIR` keyed by the tag, so `-lw ghcr.io/home-assistant/home-assistant:stable` reuses the cached file on every subsequent run. To pick up a newer build of `:stable`, drop the cached image so the next `sandal run` re-pulls a fresh one:

``` { .bash title="Update to the latest stable release" }
# Stop and remove the running container so the cached image becomes unreferenced
sandal stop homeassistant
sandal rm homeassistant

# Clear images that no container references — the old :stable squashfs is one of them
sandal clear -images

# Start it again with the same arguments — the new manifest is pulled fresh
sandal run -d --startup \
    -lw ghcr.io/home-assistant/home-assistant:stable \
    -name homeassistant \
    -p 0.0.0.0:8123:8123 \
    -v /mnt/homeassistant/config:/config
```

See [`sandal clear`](../commands/clear.md) for the full set of cleanup scopes (`-images`, `-snapshots`, `-orphans`, `-temp`, …) and the `-dry-run` preview. Your `-v /mnt/homeassistant/config:/config` volume and any saved snapshot files are *not* touched by `sandal clear`, so all your Home Assistant state survives the upgrade.

## Backups

With this setup, a complete backup is just two paths on the host:

- `/mnt/homeassistant/config/` — the live config volume (rsync, restic, borg, …)
- `/var/lib/sandal/snapshot/homeassistant.sqfs` — the system-tweaks snapshot, if you use one

Snapshots are squashfs images and are portable — copy `homeassistant.sqfs` to a new machine, drop it in `SANDAL_SNAPSHOT_DIR` (or pass `-snapshot /path/to/file.sqfs` to `sandal run`), and the new host picks up exactly where the old one left off.

## Tear down

``` { .bash title="Remove the container and its overlay" }
sandal stop homeassistant
sandal rm homeassistant
```

The host volume at `/mnt/homeassistant/config` and any saved snapshot files are kept — delete them manually if you want a fully clean slate.

## See also

- [`sandal run`](../commands/run.md) — full flag reference
- [`sandal snapshot`](../commands/snapshot.md) — include/exclude filters and merge behavior
- [`sandal daemon`](../commands/daemon.md) — service install, signal proxy, restart-on-exit
- [Home Assistant Core docker image](https://github.com/home-assistant/core/pkgs/container/home-assistant) — upstream tag list
