# Commands

This section covers the `sandal` sub-commands. The examples below focus on [`sandal run`](run.md) usage patterns that work on both **Linux** and **macOS**.

On Linux, containers run natively using kernel namespaces. On macOS, sandal transparently provisions an Apple Virtualization framework VM and runs the container inside it, so the same `sandal run` invocations work on both platforms.

!!! note "About `-tmp` on macOS"
    `-tmp` backs the overlay with a host tmpfs and is **Linux-only**. On macOS the container runs inside a VM, so you don't need it — use `-chdir-type image` (or leave it as the default `auto`, which resolves to `image` inside the VM) instead. The examples below omit `-tmp` so they work on both platforms.

## Quick start

Pull an image from a registry and run a one-off command:

``` { .bash title="Run a command in alpine" }
sandal run -lw alpine:latest --rm -- echo "hello from sandal"
```

`-lw` accepts any OCI image reference (Docker Hub short names, `ghcr.io/...`, `public.ecr.aws/...`, etc.). The image is pulled once, flattened to squashfs, and cached for subsequent runs.

## Run the image's default command

When the image has an `ENTRYPOINT` or `CMD`, you can omit the trailing `-- command`:

``` { .bash title="Run nginx using its default CMD" }
sandal run -lw nginx:latest --rm -p 0.0.0.0:8080:80
```

Sandal reads `ENTRYPOINT`, `CMD`, `WorkingDir`, `User`, and `ENV` from the image manifest automatically.

## Publish ports

The `-p` flag publishes a host port into the container and works identically in native and VM mode:

``` { .bash title="Expose an HTTP server on :8080" }
sandal run -lw nginx:latest --rm -p 0.0.0.0:8080:80
```

``` { .bash title="Remap host 8443 -> container 3000" }
sandal run -lw caddy:latest --rm -p tls://0.0.0.0:8443:tcp://127.0.0.1:3000
```

## Resource limits

Limit CPU and memory with `--cpu` and `--memory`. On Linux, limits are enforced via cgroup v2. On macOS, they are applied to the host-side VM configuration:

``` { .bash title="Half a CPU, 256MB of memory" }
sandal run -lw alpine:latest --rm --cpu 0.5 --memory 256M -- sh
```

``` { .bash title="Two CPUs, 1GiB of memory" }
sandal run -lw python:3-slim --rm --cpu 2 --memory 1Gi -- python -c "print('ok')"
```

## Passing environment variables

Pass individual variables from the host with `-env-pass` (no value injection — the name is read from your shell):

``` { .bash title="Forward a single variable" }
export MY_TOKEN="s3cret"
sandal run -lw alpine:latest --rm -env-pass MY_TOKEN -- sh -c 'echo "$MY_TOKEN"'
```

Or send every host variable with `-env-all`:

``` { .bash title="Forward all host environment variables" }
sandal run -lw alpine:latest --rm -env-all -- env
```

## Named containers and snapshots

Give a container a name and persist its filesystem changes between runs:

``` { .bash title="Run a named container" }
sandal run -lw alpine:latest -name dev -t -- sh
```

``` { .bash title="Save changes to a snapshot after exit" }
sandal snapshot dev
```

On the next run, sandal automatically layers the snapshot back on top of the image. See [`sandal snapshot`](snapshot.md) for snapshot paths, custom output locations, and how snapshots are reattached.

## Layer multiple images

Stack images by repeating `-lw`. The right-most image wins for `ENTRYPOINT`, `CMD`, `WorkingDir`, and `User`; `ENV` accumulates with later images overriding earlier ones.

A common pattern is combining a **single-binary Go image** (typically built `FROM scratch` — no shell, no package manager, just the binary) with **alpine** to get shell and debugging tools alongside the binary:

``` { .bash title="Traefik (single-binary Go image) + alpine shell/tools" }
sandal run -lw traefik:latest -lw alpine:latest --rm -t -- sh
# inside the container: /traefik is the binary, alpine's busybox utilities are also available
```

## Ephemeral in-memory rootfs (Linux only)

On Linux, add `-tmp <MB>` to back the overlay with a host tmpfs instead of disk — useful for throwaway environments, nested sandal/docker runs, or filesystems that don't support overlayfs upper (fat32, exfat). This flag has no effect on macOS, where the overlay already lives inside the VM:

``` { .bash title="Ephemeral alpine with 128MB tmpfs (Linux)" }
sandal run -lw alpine:latest -tmp 128 --rm -t -- sh
```

## Background daemons (Linux only)

On Linux, run detached with `-d` (requires the sandal daemon). Use `sandal attach` to reconnect to a TTY, or `sandal exec` to run additional commands inside the container. The daemon is not available on macOS:

``` { .bash title="Background container with PTY (Linux)" }
sandal daemon &
sandal run -lw alpine:latest -d -t -name bg -- sh
sandal exec bg -- ps aux
sandal attach bg
```

See [`sandal daemon`](daemon.md) for daemon setup and service registration, [`sandal attach`](attach.md) for connecting to a background container's TTY, and [`sandal exec`](exec.md) for running additional commands inside a running container.

## VM mode

On Linux, you can opt in to KVM isolation with `-vm`. On macOS, sandal already uses Apple Virtualization, so `-vm` is a no-op and can be omitted:

``` { .bash title="Force VM isolation on Linux" }
sandal run -lw alpine:latest -vm --rm -- sh
```

See [`sandal vm`](vm.md) for managing persistent VMs on macOS.

---

For the full list of flags, defaults, and advanced behaviors (overlay internals, custom mount targets, networking, TLS termination, etc.), see [`sandal run`](run.md).
