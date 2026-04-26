# VS Code AI coding agent

Use sandal to give a VS Code–driven AI coding agent — Claude Code, Copilot, Cursor, or any other shell-using assistant — a clean, ephemeral development environment that resets on every session, without giving the agent access to your host filesystem, secrets, or network.

This guide walks through:

- Three topology patterns for wiring VS Code, an agent, and a sandal container together
- Two ways to expose the project — `-v` (writes hit the host directly) vs. `-lw` (writes stay inside the container until you promote them)
- Three ephemerality modes (`-tmp` tmpfs, `--rm` disk overlay, snapshot-per-task) and when to pick each
- A hardening section covering network, mounts, secrets, and unprivileged execution to bound the agent's blast radius

## Quick start

The fastest way to convince yourself isolation works — drop into an Alpine shell with your project bind-mounted, all writes outside the project going to RAM, and disappearing on exit:

``` { .bash title="One-shot ephemeral shell with the project mounted" }
sandal run --rm -t -tmp 1024 \
    -lw alpine:latest \
    -v "$PWD":/workspace \
    -dir /workspace \
    -name agent-shell \
    -- /bin/sh
```

Inside the container, run `apk add curl`, drop a `touch /etc/burned-it-down`, edit `/workspace/README.md`, then exit. Run the same command again: `/etc/burned-it-down` is gone (it lived in the tmpfs change dir), `apk` no longer remembers `curl`, but your `README.md` edit is still there because `/workspace` is a real bind mount on the host.

That's the whole model. The rest of this page is about plugging an editor and an agent into it.

!!! note "Image choice"
    Alpine is a tiny base for verifying the mechanics. For real agent work, stack a richer toolchain image with `-lw`. Microsoft publishes language-specific devcontainer bases that work as drop-in `-lw` values — `mcr.microsoft.com/devcontainers/base:ubuntu`, `…/python:3`, `…/javascript-node:20`, `…/go:1.22`, `…/typescript-node:20`, `…/cpp:debian`, `…/rust:1`, and more. See [Pattern C](#pattern-c-devcontainer-style-pre-baked-toolchain) for stacking your own toolchain on top. Multiple `-lw` flags are allowed; later images win on `ENTRYPOINT`/`CMD`/`User`/`WorkingDir`.

---

## Workspace mount mode — `-v` direct vs. `-lw` copy-on-write

Every pattern below has to make one decision: **how should the project tree appear inside the container?** There are two options, and they differ on whether the agent can clobber your host source.

### `-v "$PWD":/workspace` — direct bind mount

Every write the agent makes lands on host disk **immediately**. Convenient (you see edits in your host editor in real time), but the agent can also `rm -rf` your repo, leave half-applied refactors, or commit garbage to `.git/`. Suitable when you trust the agent and rely on `git` to recover from mistakes.

### `-lw "$PWD":/workspace -v "$PWD":/real` — copy-on-write workspace + explicit-promotion path *(recommended)*

`-lw "$PWD":/workspace` mounts the project as a **lower layer**. Reads come from your host repo, but every agent write goes to the container's change dir overlay (RAM tmpfs, disk overlay, or snapshot — whichever ephemerality mode you picked). Your host source is **not** modified.

`-v "$PWD":/real` exposes the same path as a real bind mount at `/real`. When the agent has finished a task and you've reviewed its changes, you (or the agent under your direction) explicitly copy from `/workspace` to `/real` — typically with `rsync -a /workspace/ /real/` or by `git apply`-ing a diff — to promote them to the host.

``` { .bash title="COW workspace with an explicit promotion path" }
sandal run --rm -t -tmp 2048 \
    -lw mcr.microsoft.com/devcontainers/base:ubuntu \
    -lw "$PWD":/workspace \
    -v "$PWD":/real \
    -dir /workspace \
    -name agent-cow \
    -user vscode \
    -- bash
```

Inside the container:

```
# Agent does whatever it wants under /workspace — all writes are ephemeral.
$ rm -rf /workspace/src     # this is fine, it's COW
$ # ... agent does its work ...
# When you've reviewed and want to keep the changes:
$ rsync -a --delete /workspace/ /real/
```

??? info "How sandal layers `-lw` with custom targets"
    `-lw <source>:/<target>` mounts `<source>` as a lower layer at `/target` inside the container, with its own dedicated upper/work directories — i.e. a mini-overlay scoped to that path. Reads transparently fall through to `<source>` on the host, writes go to the container's change dir, and on container exit the change dir is deleted (with `--rm` or `-tmp`) or kept for snapshotting. See [the `-lw` reference](../commands/run.md#-lw-value) for the full semantics, including `:=sub` for sub-mount discovery.

??? info "Picking between the two"
    Use `-v` when the agent is well-trusted and you want zero promotion friction (e.g. you're driving the agent yourself, line by line, and you `git diff` after each step). Use `-lw + -v` when the agent runs longer-horizon tasks, when you want a clean "abort the whole session" off-switch, or when you want to compare the agent's proposed state vs. the host state side-by-side before promoting. The hardened recommended profile at the end of this page uses `-lw + -v`.

---

## Pattern A: VS Code Remote-SSH into the container *(recommended)*

VS Code on the host opens an SSH connection into a sandal container. VS Code Server, the Claude Code (or other agent) extension, the agent's shell tools, and the project all live **inside** the container. Your host VS Code instance is just the front-end; nothing the agent does touches the host.

### Build the base image

You need an image with `openssh-server`, your language toolchain, `git`, and a non-root user. Build it once with `sandal export`, or pull a community devcontainer image, or just use a registry image such as `mcr.microsoft.com/devcontainers/base:ubuntu`. For brevity here we use the Microsoft devcontainer base, which already has a `vscode` user and a sane PATH.

### Run the container with SSH exposed

``` { .bash title="Detached container with SSH on a host-only port" }
sandal run -d -t \
    -lw mcr.microsoft.com/devcontainers/base:ubuntu \
    -name agent-box \
    -tmp 4096 \
    -v "$PWD":/workspaces/project \
    -dir /workspaces/project \
    -p 127.0.0.1:2222:22 \
    -user vscode \
    -- /usr/sbin/sshd -D
```

- `-d -t` runs detached with a PTY — VS Code's terminal panes work correctly inside.
- `-tmp 4096` puts overlay writes in 4 GB of RAM tmpfs. VS Code Server, npm caches, language-server indexes, and `apt install`s land here and vanish on exit.
- `-v "$PWD":/workspaces/project` is the only durable surface — every other write is ephemeral.
- `-p 127.0.0.1:2222:22` binds **only** to localhost; the SSH endpoint is unreachable from other machines on your network. See [`sandal run -p`](../commands/run.md#-p-value) for the full grammar.
- `-user vscode` runs the container's init as the unprivileged `vscode` user instead of root.

### Connect VS Code

In VS Code, install the **Remote - SSH** extension, then open the command palette → *Remote-SSH: Connect to Host* → enter `vscode@127.0.0.1` and pass `-p 2222` in your `~/.ssh/config`:

``` { .bash title="~/.ssh/config snippet" }
Host agent-box
    HostName 127.0.0.1
    Port 2222
    User vscode
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
```

Connect to `agent-box`, open `/workspaces/project`, install the Claude Code extension **in the remote workspace** (VS Code prompts for this), and start the agent. Every shell command Claude Code issues runs inside the container.

??? info "Why VS Code Server first-launch is slow on tmpfs"
    The first connection downloads VS Code Server (~80 MB) and unpacks it under `~/.vscode-server` inside the container. With `-tmp` that lives in RAM and is **lost on container restart**, so a re-run pays the download again. If that bothers you, snapshot it once and stack it as a `-lw` layer — see [Pattern C](#pattern-c-devcontainer-style-pre-baked-toolchain).

### Restart, stop, tear down

``` { .bash title="Day-to-day operations" }
sandal ps                             # see container status
sandal exec agent-box -- bash         # drop a shell in next to the agent
sandal stop agent-box                 # graceful SIGTERM, 30s timeout
sandal rerun agent-box                # restart with the original arguments
sandal rm agent-box                   # delete the overlay (project on host is untouched)
```

See [`sandal exec`](../commands/exec.md), [`sandal ps`](../commands/ps.md), and [`sandal rerun`](../commands/rerun.md) for the flag reference.

---

## Pattern B: Local VS Code + `sandal exec` terminal profile

Keep VS Code running on the host with all your usual settings, themes, and extensions. Replace only the **integrated terminal** with one that drops you inside a sandal container — every shell command Claude Code's extension runs through that terminal then executes inside the sandbox, while file editing stays in your normal host VS Code.

### Start a long-lived container in the background

``` { .bash title="Background sandbox container" }
sandal run -d -t \
    -lw mcr.microsoft.com/devcontainers/base:ubuntu \
    -name agent-shell \
    -tmp 2048 \
    -v "$PWD":/workspaces/project \
    -dir /workspaces/project \
    -user vscode \
    -- sleep infinity
```

The container's only job is to stay alive (`sleep infinity`) so we can `exec` into it. Writes go to tmpfs, edits to `/workspaces/project` go straight to your host repo.

### Add a VS Code terminal profile

Open VS Code's `settings.json` (`Cmd/Ctrl+Shift+P` → *Preferences: Open User Settings (JSON)*) and add:

``` { .json title="settings.json" }
{
  "terminal.integrated.profiles.linux": {
    "sandal-agent": {
      "path": "sandal",
      "args": ["exec", "-t", "agent-shell", "--", "bash", "-l"]
    }
  },
  "terminal.integrated.defaultProfile.linux": "sandal-agent"
}
```

Now any new VS Code terminal — including the ones Claude Code spawns for tool calls — opens inside `agent-shell`. The host editor sees file edits instantly through the bind mount; everything else (package installs, build artifacts, language servers the agent runs from the terminal) lives in the container.

??? info "Why this is weaker isolation than Pattern A"
    The Claude Code extension itself still runs in your host VS Code, so it has access to your VS Code settings, workspace state, and any environment variables VS Code inherited from your shell. Only commands the extension executes via the terminal are sandboxed. If the threat model is "I trust the extension but not the code/tools the agent installs and runs", Pattern B is plenty. If you don't fully trust the extension itself, use Pattern A.

---

## Pattern C: Devcontainer-style pre-baked toolchain

For repeatable agent sessions across a team — same Node version, same `git`, same linters, same agent CLI binaries — bake the toolchain into a portable squashfs once and stack it as a `-lw` layer on every run. This is sandal's equivalent of a devcontainer image, but the artifact is a single `.sqfs` file you can copy to a USB drive or attach to a CI job.

### Build the toolchain layer once

``` { .bash title="Provision the toolchain inside a one-off container" }
sandal run -t \
    -lw mcr.microsoft.com/devcontainers/base:ubuntu \
    -name agent-toolchain-build \
    -- bash -c '
        apt-get update && \
        apt-get install -y --no-install-recommends \
            nodejs npm python3 python3-pip git openssh-server && \
        npm install -g @anthropic-ai/claude-code && \
        rm -rf /var/lib/apt/lists/* /workspaces
    '
```

Then capture **just the system changes** into a squashfs, excluding the workspace and any per-container state:

``` { .bash title="Snapshot the toolchain into a portable .sqfs" }
sandal snapshot -e /workspaces -e /tmp \
    -f /var/lib/sandal/snapshot/agent-toolchain.sqfs \
    agent-toolchain-build

sandal rm agent-toolchain-build
```

`-e` excludes a path tree; the resulting `.sqfs` holds your installed packages, the `claude-code` binary, and the SSH server config — and nothing else. See [`sandal snapshot`](../commands/snapshot.md) for include/exclude semantics.

### Use the toolchain on every subsequent run

``` { .bash title="Fresh ephemeral session that reuses the toolchain" }
sandal run --rm -t -tmp 2048 \
    -lw mcr.microsoft.com/devcontainers/base:ubuntu \
    -lw /var/lib/sandal/snapshot/agent-toolchain.sqfs \
    -v "$PWD":/workspaces/project \
    -dir /workspaces/project \
    -user vscode \
    -name agent-session \
    -- bash
```

The right-most `-lw` wins for `ENTRYPOINT`/`CMD`/`User`, but our toolchain snapshot doesn't define those — it's only system files — so the base image's defaults still apply. Subsequent sessions skip the package install entirely; the layer is a read-only squashfs on disk.

To share with a teammate: copy `agent-toolchain.sqfs` to their machine, drop it under `/var/lib/sandal/snapshot/`, and they run the same command. No registry, no Dockerfile.

---

## Ephemerality modes — which one to pick

| Mode | Flag | Where writes go | Cost | Lost on exit? | Best for |
|---|---|---|---|---|---|
| **tmpfs** | `-tmp <MB>` | RAM | Capped at the MB you allocate | Yes (always) | Short interactive agent loops, fastest iteration, no disk I/O |
| **disk overlay** | `--rm` (no `-tmp`) | `/var/lib/sandal/changedir/<name>` | Disk space until exit | Yes (deleted by `--rm`) | Long-running agent jobs that exceed RAM (large builds, big test suites) |
| **snapshot per task** | `-snapshot <path>` + `sandal snapshot` | Disk overlay → captured into a `.sqfs` | Disk; one `.sqfs` per session you keep | No — you choose what to keep | Reviewable / shareable / replayable agent runs |

`-tmp` is the right default for interactive agent sessions. The agent is going to install things, generate cache files, and create scratch dirs you don't care about; routing them to RAM means they never touch your disk and disappear automatically. Reach for `--rm` (disk overlay) only if a build is too big to fit in tmpfs. Reach for snapshot-per-task when you want to **save** the result of an agent session — for code review, for replay on another machine, or to fork two parallel agent runs from a common baseline.

??? info "Stacking ephemerality modes"
    These compose. A common pattern is: **base image** + **toolchain snapshot** (Pattern C) as `-lw` layers, **`-tmp`** for the per-session change dir, **`-v`** for the project. The base and toolchain are immutable and shared; per-session writes are RAM-only; project edits go straight to git.

---

## Hardening: limiting the agent's blast radius

Agents can read every file they can `cat` and run every binary on `PATH`. The defaults above already block most of the host, but you should know the dials.

### Narrow bind-mounts

Mount only the project. **Do not** bind-mount `$HOME`, `~/.ssh`, `~/.aws`, `~/.config`, the Docker socket, or `/`:

``` { .bash title="Good: just the project" }
-v "$PWD":/workspaces/project
```

``` { .bash title="Bad: don't do these" }
-v "$HOME":/home/vscode             # exposes your SSH/AWS/GPG keys
-v /var/run/docker.sock:/var/run/docker.sock  # container can launch host containers
-v /:/host                          # full host filesystem
```

Each `-v` is a bind mount with no copy-on-write — what the agent writes lands directly on the host path. Pick the smallest possible target.

### Don't inherit host env vars

`-env-all` ships your entire host environment (including any `AWS_*`, `GITHUB_TOKEN`, agent API keys, shell history vars) into the container. Use [`-env-pass`](../commands/run.md#-env-pass-value) with an explicit allowlist instead:

``` { .bash title="Pass exactly the API key the agent needs, nothing else" }
sandal run --rm -t -tmp 1024 \
    -lw mcr.microsoft.com/devcontainers/base:ubuntu \
    -v "$PWD":/workspaces/project \
    -dir /workspaces/project \
    -user vscode \
    -env-pass ANTHROPIC_API_KEY \
    -env-pass TZ \
    -- bash
```

`-env-pass` does **not** accept `KEY=VALUE` form — it reads the named variable from your current environment and forwards just that name. That makes it harder to accidentally bake credentials into a script you check in.

### Run as an unprivileged user

`-user vscode` (or any non-root UID) prevents the agent from running `apt install` system-wide, modifying `/etc`, or tampering with the base image. Combine with [`-ns-user`](../commands/run.md#-ns-user-string) for user-namespace mapping when you want the container's UID space remapped against the host:

``` { .bash title="Unprivileged user inside, mapped through a user namespace" }
sandal run --rm -t -tmp 1024 \
    -lw mcr.microsoft.com/devcontainers/base:ubuntu \
    -user vscode \
    -ns-user "" \
    -v "$PWD":/workspaces/project \
    -dir /workspaces/project \
    -- bash
```

### Read-only base layers

The `-lw` layers are already immutable — the agent's writes go to the change dir overlay. For belt-and-braces, [`-ro`](../commands/run.md#-ro-bool) makes the merged rootfs read-only too, forcing the agent to write only to bind-mounted paths and tmpfs. Useful for very locked-down sessions where the base toolchain should never be modified, even ephemerally.

!!! note "The agent can still trash `/workspace`"
    None of these flags protect the bind-mounted project directory — that's host disk by definition, and any agent write lands there. **Commit before each agent session**, treat the bind-mount as the only durable surface, and rely on `git reset --hard` if the agent makes a mess.

---

## Putting it together — a hardened recommended profile

This is the configuration to start from for a typical "Claude Code in VS Code, ephemeral, hardened" session. Combine Pattern A (Remote-SSH), Pattern C (toolchain snapshot), the COW workspace, and the hardening flags:

``` { .bash title="Production-ish profile" }
sandal run -d -t \
    -lw mcr.microsoft.com/devcontainers/base:ubuntu \
    -lw /var/lib/sandal/snapshot/agent-toolchain.sqfs \
    -lw "$PWD":/workspace \
    -v "$PWD":/real \
    -name agent-box \
    -tmp 4096 \
    -dir /workspace \
    -user vscode \
    -p 127.0.0.1:2222:22 \
    -env-pass ANTHROPIC_API_KEY \
    -- /usr/sbin/sshd -D
```

- Base image + cached toolchain snapshot
- COW workspace at `/workspace` — agent edits stay in the overlay; promote with `rsync /workspace/ /real/`
- 4 GB tmpfs for ephemeral writes (overlay + agent caches)
- Unprivileged user
- SSH on host-loopback only
- One environment variable forwarded explicitly
- No host secrets, no Docker socket

Connect VS Code Remote-SSH to `vscode@127.0.0.1:2222`, open `/workspace`, and start working. When you're happy with what the agent did, promote with `rsync -a --delete /workspace/ /real/` from inside the container — or do the diff-and-apply review on the host, comparing `/real` against the live overlay via `sandal exec agent-box -- diff -ru /real /workspace`.

---

## Tear down

``` { .bash title="Stop and remove" }
sandal stop agent-box
sandal rm agent-box
```

The host project at `$PWD` and any toolchain `.sqfs` files are kept. To wipe sandal's image cache and orphaned change dirs as well, see [`sandal clear`](../commands/clear.md) (`-images`, `-orphans`, `-temp`, `-dry-run`).

## See also

- [`sandal run`](../commands/run.md) — full flag reference, including `-tmp`, `-lw`, `-v`, `-p`, `-ns-*`, `-env-pass`, `-user`, `-ro`
- [`sandal exec`](../commands/exec.md) — run commands inside an existing container; the basis for the Pattern B terminal profile
- [`sandal snapshot`](../commands/snapshot.md) — capture toolchain or session state into a portable squashfs
- [`sandal daemon`](../commands/daemon.md) — keep an agent box alive across reboots with `--startup`
- [`sandal clear`](../commands/clear.md) — clean up unreferenced images and orphan change dirs
- [VS Code Remote-SSH documentation](https://code.visualstudio.com/docs/remote/ssh) — upstream reference for the editor side of Pattern A
