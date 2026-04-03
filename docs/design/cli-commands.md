# CLI Commands

Sandal is a single-binary CLI. All subcommands are dispatched from `pkg/cmd/main.go`.

## Command Dispatch

**File**: `pkg/cmd/main.go`

```go
func Main() {
    if len(os.Args) < 2 {
        printHelp()
        return
    }
    switch os.Args[1] {
    case "run":        Run(os.Args[2:])
    case "ps":         cmdPs(os.Args[2:])
    case "kill":       cmdKill(os.Args[2:])
    case "stop":       cmdStop(os.Args[2:])
    case "rm":         cmdRm(os.Args[2:])
    case "exec":       cmdExec(os.Args[2:])
    case "inspect":    cmdInspect(os.Args[2:])
    case "attach":     cmdAttach(os.Args[2:])
    case "rerun":      cmdRerun(os.Args[2:])
    case "snapshot":   cmdSnapshot(os.Args[2:])
    case "export":     cmdExport(os.Args[2:])
    case "convert":    cmdConvert(os.Args[2:])
    case "clear":      cmdClear(os.Args[2:])
    case "daemon":     cmdDaemon(os.Args[2:])
    case "cmd":        cmdCmd(os.Args[2:])
    case "vm":         cmdVM(os.Args[2:])
    case "completion": cmdCompletion(os.Args[2:])
    case "help":       printHelp()
    }
}
```

## Command Reference

### `run` - Create and Run a Container

**Files**: `pkg/sandal/run_linux.go`, `pkg/sandal/container.go`, `pkg/sandal/vm_linux.go`

```
sandal run [flags] <image> [command] [args...]
```

**Key flags**:

| Flag | Description | Example |
|------|-------------|---------|
| `--vm` | Run inside KVM virtual machine | `sandal run --vm alpine sh` |
| `-v` | Bind mount volume | `-v /host:/container` |
| `-d` | Run as daemon (background) | `sandal run -d alpine` |
| `-t` | Allocate TTY | `sandal run -t alpine sh` |
| `-n` | Network configuration | `-n ip=dhcp` |
| `--name` | Container name | `--name myapp` |
| `--memory` | Memory limit | `--memory 512M` |
| `--cpu` | CPU limit | `--cpu 2` |
| `--user` | Run as user | `--user nobody:nogroup` |
| `--privileged` | Grant all capabilities | |
| `--ns-pid` | PID namespace (host/new/pid) | `--ns-pid host` |
| `--ns-net` | Network namespace | `--ns-net host` |
| `--startup` | Auto-restart on daemon boot | |
| `-lw` | Additional lower directories | `-lw /extra/layer` |
| `-tmp` | Tmpfs-backed changes (ephemeral) | |
| `-e` | Environment variable | `-e FOO=bar` |
| `-w` | Working directory | `-w /app` |
| `--env-host` | Pass all host env vars | |

**Behavior with `--vm`**: Parses `--vm` into the container config's `VM` field, pre-pulls the image, downloads the kernel, builds an initrd containing the sandal binary, and launches a KVM VM that re-executes the remaining command inside the VM. Host-only flags (`-d`, `-startup`, `--name`, `--cpu`, `--memory`) are stripped from the args forwarded to the VM guest. When combined with `-d -startup`, the VM container is registered in the same state system as direct containers and managed by the daemon for auto-restart.

### `ps` - List Containers

**File**: `pkg/cmd/ps.go`

```
sandal ps [flags]
```

| Flag | Description |
|------|-------------|
| `-a` | Show all containers (including stopped) |
| `-q` | Only show container names |

Output format (tabwriter):
```
NAME          IMAGE          STATUS    PID     CREATED
myapp         alpine:latest  running   12345   2024-01-01 12:00:00
myvm          alpine         running   12346   2024-01-01 12:01:00
```

Both direct containers and VM containers appear in the same listing. VM containers show the KVM child process PID in the PID column.

### `kill` - Send Signal to Container

**File**: `pkg/cmd/kill.go`

```
sandal kill [-s SIGNAL] <name>
```

Sends a signal (default: SIGKILL) to the container's host PID. For VM containers, this kills the KVM child process (HostPid), which terminates the VM. If the container has `-startup`, the daemon will auto-restart it.

### `stop` - Graceful Stop

**File**: `pkg/cmd/stop.go`

```
sandal stop [-t TIMEOUT] <name>
```

1. Send SIGTERM
2. Wait up to TIMEOUT seconds (default: 10)
3. Send SIGKILL if still running

### `exec` - Execute Command in Container

**File**: `pkg/cmd/exec.go`

```
sandal exec <name> <command> [args...]
```

Enters the container's namespaces and executes the command:
1. Look up container config
2. Open `/proc/<pid>/ns/*` for each namespace
3. `setns()` into each namespace
4. `chroot()` into container rootfs
5. Exec the command

### `attach` - Attach to Container Console

**File**: `pkg/cmd/attach.go`

```
sandal attach <name>
```

Connects stdin/stdout to the container's console FIFO or socket:
- FIFO mode: Opens `$SANDAL_RUN_DIR/<name>/stdin` and `stdout`
- Socket mode: Connects to `$SANDAL_RUN_DIR/<name>/console.sock`

### `inspect` - Show Container Config

**File**: `pkg/cmd/inspect.go`

```
sandal inspect <name>
```

Outputs the container's full configuration as JSON.

### `snapshot` - Snapshot Container Changes

**File**: `pkg/cmd/snapshot.go`

```
sandal snapshot <name> <output.sqsh>
```

Creates a squashfs image from the container's overlay upper directory (changed files only).

### `export` - Export Full Filesystem

**File**: `pkg/cmd/export.go`

```
sandal export <name> <output.sqsh>
```

Creates a squashfs image from the container's merged filesystem (all layers + changes).

### `convert` - Convert to SquashFS

**File**: `pkg/cmd/convert.go`

```
sandal convert <input> <output.sqsh>
```

Converts a directory or OCI image to squashfs format.

### `rm` - Remove Container

**File**: `pkg/cmd/rm.go`

```
sandal rm <name>
```

Removes container state, rootfs, and change directories. Fails if container is running.

### `rerun` - Restart Container

**File**: `pkg/cmd/rerun.go`

```
sandal rerun <name>
```

Stops the container (if running) and re-runs it with the saved original arguments.

### `clear` - Clean Up Unused Containers

**File**: `pkg/cmd/clear.go`

```
sandal clear
```

Removes state and files for containers whose processes are no longer running.

### `daemon` - Start Daemon

**File**: `pkg/cmd/daemon.go`

```
sandal daemon
```

Starts the background daemon for persistent container management. See [daemon-controller.md](daemon-controller.md).

### `vm` - VM Management

**File**: `pkg/cmd/vm.go`

```
sandal vm list
sandal vm start -name <name>
sandal vm stop -name <name>
```

Manages VMs directly. On macOS, uses Apple Virtualization Framework. On Linux, uses KVM. The `vm start` subcommand is also used internally by the daemon's `forkVMProcess()` to boot a VM in a child process — this ensures the daemon PID is not the VM PID, so killing the VM doesn't kill the daemon.

### `completion` - Shell Completions

**File**: `pkg/cmd/completion.go`

```
sandal completion bash
sandal completion zsh
sandal completion fish
```

Outputs shell completion scripts.

### `cmd` - Show Container Command

**File**: `pkg/cmd/cmd.go`

```
sandal cmd <name>
```

Prints the original command used to create the container, useful for recreation.

## Flag Parsing

**Package**: `pkg/sandal/`

**File**: `pkg/sandal/run_linux.go`, `pkg/sandal/args.go`

Flags are parsed using Go's `flag` package with custom types:

```go
type StringFlags []string

func (s *StringFlags) String() string { return strings.Join(*s, ",") }
func (s *StringFlags) Set(value string) error {
    *s = append(*s, value)
    return nil
}
```

This allows repeated flags: `-v /a:/b -v /c:/d -e FOO=1 -e BAR=2`

### Arg Manipulation Helpers (`pkg/sandal/args.go`)

When forwarding CLI args to the VM guest, certain flags must be stripped or rewritten. Helper functions in `args.go`:

| Function | Purpose |
|----------|---------|
| `ExtractFlag(args, name, default)` | Remove a flag and its value (`-flag val` or `--flag=val`), return value and cleaned args |
| `RemoveBoolFlag(args, name)` | Remove a boolean flag (`-flag` or `--flag`) from args |
| `HasFlag(args, name)` | Check if a flag is present in args |
| `SplitFlagsArgs(args)` | Split at `--` separator into flag args and command args |

## Platform-Specific Dispatch

**File**: `pkg/sandal/run_linux.go` vs `pkg/sandal/run_darwin.go`

| Feature | Linux | macOS |
|---------|-------|-------|
| Direct containers | Yes | No (containers run in VM) |
| `--vm` flag | KVM hypervisor | Virtualization.framework |
| Container runtime | Native namespaces | Via VM only |
| Network | veth + bridge | VM NAT |

On macOS, all container execution happens inside a VM managed by the Virtualization.framework.
