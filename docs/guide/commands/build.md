# Build

Builds an OCI container image from a Dockerfile, producing a sandal-native `.sqfs` in the local image cache and optionally pushing it to a registry.

``` { .bash title="Basic usage" }
sandal build -t myimage:latest .
```

The result is available immediately for `sandal run`:

``` { .bash }
sandal run -lw myimage:latest
```

## How it works

Every build produces a **single squashed layer**. Sandal's local cache format is a single `.sqfs` per image plus a `.sqfs.json` sidecar with the OCI `RuntimeConfig` (ENV, ENTRYPOINT, CMD, WORKDIR, USER). When pushing to a registry the image is flattened into one layer too — this matches how `sandal pull` already treats external images. `docker history` still shows every instruction because the OCI image config's `History` array is populated per instruction.

For each build stage:

1. The base image (`FROM`) is pulled via the same path `sandal run -lw <ref>` uses — cached `.sqfs` and all.
2. The base is snapshot-copied onto a tmpfs-backed working directory. Tmpfs is used so the stage rootfs is on a real filesystem, not overlayfs, which lets per-step RUN overlays nest without hitting the kernel's max overlay stacking depth.
3. Each instruction is applied in order:
   - `ENV`, `WORKDIR`, `USER`, `CMD`, `ENTRYPOINT`, `LABEL`, `EXPOSE`, `ARG`, `VOLUME`, `STOPSIGNAL` — mutate the in-memory `RuntimeConfig` only.
   - `COPY`, `ADD` — copy files directly into the stage rootfs (no container exec needed).
   - `RUN` — execute the command in an ephemeral sandal container with host network and the stage rootfs as lower. After exit, the overlay's upper dir is merged into the stage rootfs.
4. When all stages are done, the target stage's rootfs is serialised as squashfs and the `RuntimeConfig` is written to the sidecar.

## Flags

### `-t name:tag` (required unless --dry-run)

Image tag. Used as the local cache key (`$SANDAL_IMAGE_DIR/<sanitized-tag>.sqfs`) and as the destination when `--push` is set.

### `-f Dockerfile`

Path to the Dockerfile. Defaults to `<CONTEXT>/Dockerfile`.

### `--push`

After building locally, push the image to the registry implied by `-t`. Localhost registries (e.g. `localhost:5000/...`) use plain HTTP automatically; everything else uses HTTPS.

### `--target <stage>`

In multi-stage builds, stop at the named stage. Without this flag the last stage becomes the output image.

### `--build-arg KEY=VALUE` (repeatable)

Supply values for Dockerfile `ARG` declarations. Overrides any default value in the Dockerfile.

### `--dry-run`

Parse the Dockerfile and print the plan without building. Useful for validating syntax.

### `-vm`

Run the build inside a virtual machine. The VM boots sandal as `/init`, the build context and Dockerfile are shared read-only via VirtioFS, and the image cache directory is shared read-write so the output `.sqfs` + sidecar appear on the host immediately. Required on macOS; optional on Linux.

```bash
# Linux: optional VM isolation for the build
sandal build -vm -t myapp:1.0 .

# macOS: VM is implicit — -vm is accepted but always assumed
sandal build -t myapp:1.0 .
```

### `--cpu` / `--memory`

Tune the build VM's resources. Only effective with `-vm`.

```bash
sandal build -vm --cpu 4 --memory 2G -t myapp:1.0 .
```

`--cpu` accepts a decimal number of CPUs (rounded up). `--memory` accepts human-readable sizes: `K`/`M`/`G`/`T` (1000-based) or `Ki`/`Mi`/`Gi`/`Ti` (1024-based); a bare number is bytes.

## Supported instructions

| Instruction | Notes |
|-------------|-------|
| `FROM image[:tag] [AS name]` | Pulls from any OCI registry. `scratch` creates an empty rootfs. `FROM <previous-stage>` is supported via stage name or index. |
| `RUN <cmd>` or `RUN ["exec","form"]` | Runs inside an ephemeral sandal container with host network. Shell form uses `/bin/sh -c`. |
| `COPY [--from=<stage>] <src>... <dst>` | Copies from the build context or from a previous stage's rootfs. |
| `ADD <src>... <dst>` | Same as COPY (URL fetch + tar auto-extract not yet implemented). |
| `ENV KEY=VAL ...` | Multi-pair form and legacy `ENV KEY VAL` supported. |
| `LABEL KEY=VAL ...` | |
| `ARG NAME[=DEFAULT]` | Scoped to the stage (or global if before the first FROM). |
| `WORKDIR /path` | Creates the directory in the rootfs and becomes the default CWD for subsequent RUN/COPY. |
| `USER user[:group]` | |
| `CMD ["a","b"]` or `CMD a b` | |
| `ENTRYPOINT ["x"]` or `ENTRYPOINT x` | |
| `EXPOSE port[/proto]` | `tcp` is the default protocol. |
| `VOLUME /path ...` | |
| `STOPSIGNAL SIG` | |
| `SHELL ["/bin/bash","-c"]` | Parsed but not yet honoured for shell-form RUN (planned). |

`.dockerignore` is honoured for `COPY`/`ADD` from the build context. Negation with `!pattern` and `**` cross-segment globs are supported.

## Multi-stage example

```dockerfile
FROM golang:1.22 AS build
WORKDIR /src
COPY . .
RUN go build -o /out/app ./cmd/app

FROM alpine:3.19
COPY --from=build /out/app /usr/local/bin/app
ENTRYPOINT ["/usr/local/bin/app"]
```

```bash
sandal build -t myapp:1.0 .
sandal run -lw myapp:1.0
```

## Push round-trip

```bash
# On the build host:
sandal build -t registry.example.com/team/svc:v3 --push .

# On any other sandal host:
sandal run -lw registry.example.com/team/svc:v3
```

## Environment variables

| Variable | Purpose |
|----------|---------|
| `SANDAL_IMAGE_DIR` | Where `.sqfs` + `.sqfs.json` files are cached. Default `/var/lib/sandal/image`. |
| `SANDAL_TEMP_DIR` | Working directory for stage rootfs and intermediate files. Default `/var/lib/sandal/tmp`. |

## Limitations

- **Single layer output.** Per-instruction layers are not preserved. This keeps images simple and matches the sandal cache format.
- **No build-time layer cache.** Re-running `sandal build` re-executes every step from scratch. A content-addressed cache by instruction hash is on the roadmap.
- **No BuildKit features.** `--mount=type=cache`, secrets, SSH forwarding, and parser directives other than `# escape=` are not supported.
- **`ADD` URL fetch and tar auto-extract** not yet implemented; `ADD` currently behaves as `COPY`.