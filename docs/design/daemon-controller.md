# Daemon and Controller

The daemon provides persistent container management, and the controller implements IPC between CLI commands and the daemon.

## Daemon

**Package**: `pkg/daemon/`

### Purpose

The daemon is optional. Without it, sandal runs containers in the foreground. With it:
- Containers survive CLI disconnection (`-d` flag)
- Background containers can be listed, attached, stopped
- Startup containers auto-restart on daemon boot
- Health checks monitor container processes

### Startup

**File**: `daemon/start.go`

```
cmdDaemon()
  |
  +-- Create directories:
  |     /var/lib/sandal/          (state persistence)
  |     /var/lib/sandal/state/    (container config files)
  |     /var/run/sandal/          (runtime: PIDs, sockets, FIFOs)
  |
  +-- sandalnet.CreateDefaultBridge()
  |     Create sandal0 bridge with default subnet
  |
  +-- controller.StartServer()
  |     Listen on Unix socket: /var/run/sandal/sandal.sock
  |
  +-- Restore startup containers:
  |     For each config in /var/lib/sandal/state/ with Startup=true:
  |       Verify process is running
  |       If dead: restart container
  |
  +-- Start health check loop (periodic)
  |
  +-- Handle signals (SIGINT, SIGTERM):
        Stop all running containers
        Clean up bridge and state
        Exit
```

### Health Check

**File**: `daemon/health-check.go`

```
healthCheckLoop():
  Every 3 seconds:
    For each container in state directory:
      Determine which PID to monitor:
        VM container (VM != ""): check HostPid (KVM child process)
        Direct container:        check ContPid
      Check if process PID is alive (kill -0)
      If zombie: reap with waitpid
      If dead and Startup=true: recover (contRecover)
      If dead and not Startup: skip
```

#### VM-Aware PID Monitoring

For VM containers, the health check monitors `HostPid` instead of `ContPid`. This is because:
- `ContPid` is always 0 for VM containers (the container PID lives inside the VM)
- `HostPid` is the KVM child process PID that was forked by `forkVMProcess()`
- When the VM process dies (crash, kill, or normal exit), the daemon detects it via `HostPid`

#### Container Recovery (`contRecover`)

```
contRecover(cont):
  If status == "stop": return (user explicitly stopped it)

  Clean up stale resources (console sockets, mounts, cgroups)
  Kill old process (SIGTERM with timeout, then SIGKILL)
  Re-read latest config from controller

  If VM container (VM != ""):
    Call sandal.Run(HostArgs[2:])
    This re-runs the full RunInKVM() pipeline:
      - Re-pulls images
      - Re-allocates network
      - Re-builds initrd
      - Boots KVM in a forked child process
  Else (direct container):
    Call host.Run(latest)
```

### Signal Proxy

**File**: `daemon/signal-proxy.go`

The daemon forwards signals to managed containers:
```go
signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
for sig := range sigCh {
    for _, container := range runningContainers {
        syscall.Kill(container.HostPid, sig)
    }
}
```

### Zombie Reaping

**File**: `daemon/zombie.go`

As PID 1 in certain setups (or as a container manager), the daemon reaps zombie processes:
```go
go func() {
    for {
        var status unix.WaitStatus
        pid, err := unix.Wait4(-1, &status, unix.WNOHANG, nil)
        if pid <= 0 { break }
    }
}()
```

### Service Installation

**File**: `daemon/install-services.go`

Can generate and install systemd service unit:
```ini
[Unit]
Description=Sandal Container Manager
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/sandal daemon
Restart=always

[Install]
WantedBy=multi-user.target
```

### VM Container Lifecycle with Daemon

VM containers integrate with the daemon using the same state system as direct containers. The `VM` field in the container config enables VM-specific behavior.

#### Three Execution Paths

When `RunInKVM()` is called, it takes one of three paths depending on context:

```
RunInKVM(config)
  |
  +-- Path 1: Delegation (-d -startup, daemon running)
  |     CLI saves container config with HostPid=self, returns immediately.
  |     Daemon health check detects dead PID, calls sandal.Run(HostArgs)
  |     which re-enters RunInKVM() in the daemon process (Path 2).
  |
  +-- Path 2: Fork (daemon context or -d without -startup)
  |     Forks a child process: "sandal vm start -name <vmName>"
  |     Child process loads saved VM config and calls kvm.Boot().
  |     Parent records child PID as HostPid, starts socket relay,
  |     goroutine waits for child exit and updates status.
  |     Critical: daemon PID != VM PID, so killing VM doesn't kill daemon.
  |
  +-- Path 3: Foreground (no -d, no daemon)
        Boots KVM directly in the current process.
        kvm.Boot() blocks until VM exits.
        Process PID is recorded as HostPid.
```

#### Flag Stripping

Flags that apply to the host VM process are stripped before forwarding args to the guest:
- `-vm`: consumed by RunInKVM, not meaningful inside VM
- `-cpu`, `-memory`: used for VM resources (vCPU count, RAM), not guest cgroups
- `-d`, `-startup`: would cause guest to background/delegate, causing immediate exit
- `--name`: would cause guest container to overwrite host's state file (shared via VirtioFS)

#### Auto-Restart Flow

```
1. User: sandal run -d -startup --name myvm -vm kvm alpine sleep 60
2. CLI: saves config (VM="kvm", Startup=true), delegates to daemon
3. Daemon health check: detects dead PID, calls sandal.Run(HostArgs)
4. RunInKVM: forks child process (sandal vm start), records child PID
5. VM runs sleep 60
6. VM process killed (crash or "sandal kill myvm")
7. forkVMProcess wait goroutine: updates status, cleans up VM config
8. Daemon health check: detects dead PID again, repeats from step 3
```

## Controller (IPC)

**Package**: `pkg/controller/`

### Architecture

```
CLI command                          Daemon
+-----------+                        +-----------+
| ps/kill/  |  -- Unix Socket -->    | Server    |
| exec/...  |  <-- JSON Response --  | Handler   |
+-----------+                        +-----------+
      |                                    |
      v                                    v
+-------------+                      +-------------+
| Client API  |                      | State Files |
| (fallback:  |                      | /var/lib/   |
|  read files |                      | sandal/     |
|  directly)  |                      | state/      |
+-------------+                      +-------------+
```

### Server

**File**: `controller/server.go`

```go
func StartServer() {
    listener, _ := net.Listen("unix", "/var/run/sandal/sandal.sock")

    http.HandleFunc("/containers", handleContainers)
    http.HandleFunc("/container/", handleContainer)

    http.Serve(listener, nil)
}
```

**Endpoints**:

| Method | Path | Description |
|--------|------|-------------|
| GET | `/containers` | List all containers |
| GET | `/container/<name>` | Get container config |
| POST | `/container/<name>` | Create/update container |
| DELETE | `/container/<name>` | Remove container |

### Client

**File**: `controller/client.go`

```go
func GetContainers() ([]config.Config, error) {
    // Try daemon socket first
    if socketExists("/var/run/sandal/sandal.sock") {
        resp := httpGet("http://unix/containers")
        return parseJSON(resp)
    }
    // Fallback: read state files directly
    return readStateDirectory("/var/lib/sandal/state/")
}
```

The client transparently falls back to direct filesystem access when the daemon isn't running, enabling CLI commands like `ps` and `inspect` to work without the daemon.

### State Persistence

**File**: `controller/set.go`, `controller/get.go`, `controller/del.go`

Container state is stored as JSON files:

```
/var/lib/sandal/state/<container-name>.json
```

```go
func SetContainer(cfg *config.Config) error {
    if DisableStateWrites {
        return nil // VM guest: shared VirtioFS, skip writes
    }
    data, _ := json.Marshal(cfg)
    return os.WriteFile(
        filepath.Join(stateDir, cfg.Name+".json"),
        data, 0644)
}

func GetContainer(name string) (*config.Config, error) {
    data, _ := os.ReadFile(filepath.Join(stateDir, name+".json"))
    var cfg config.Config
    json.Unmarshal(data, &cfg)
    return &cfg, nil
}
```

#### VirtioFS State Isolation

The state directory (`/var/lib/sandal/state/`) is a subdirectory of the sandal library dir, which is shared into VM guests via VirtioFS. Without protection, `SetContainer()` calls from the container runtime inside the VM would write "ghost" state files through VirtioFS, creating duplicate container entries visible from the host.

The `DisableStateWrites` flag (set in `main_linux.go` after `VMInit()`) makes all `SetContainer()` calls inside VM guests a no-op. This is set at the controller level rather than individual call sites because the container runtime (`host.Run()`, `crun()`) calls `SetContainer()` at multiple points during the lifecycle and shares the same code path between host and VM execution.

```
HOST PROCESS                           VM GUEST PROCESS
  SetContainer(c)                        SetContainer(c)
    DisableStateWrites = false             DisableStateWrites = true
    → writes state JSON ✓                 → returns nil (no-op) ✓
```

### Container Listing

**File**: `controller/containers.go`

```go
func ListContainers() ([]config.Config, error) {
    entries, _ := os.ReadDir(stateDir)
    var configs []config.Config
    for _, entry := range entries {
        if strings.HasSuffix(entry.Name(), ".json") {
            cfg := loadConfigFile(entry)
            configs = append(configs, cfg)
        }
    }
    return configs, nil
}
```

## CLI Commands That Use the Controller

| Command | Controller Usage |
|---------|-----------------|
| `ps` | `ListContainers()` -> display table |
| `kill <name>` | `GetContainer(name)` -> `syscall.Kill(HostPid, sig)` |
| `stop <name>` | `GetContainer(name)` -> SIGTERM, wait, SIGKILL |
| `rm <name>` | `DelContainer(name)` -> cleanup files |
| `inspect <name>` | `GetContainer(name)` -> print JSON |
| `exec <name> cmd` | `GetContainer(name)` -> `nsenter` into namespaces |
| `attach <name>` | `GetContainer(name)` -> connect to console FIFO/socket |
| `rerun <name>` | `GetContainer(name)` -> stop + re-run with same config |

## Environment Variables

**File**: `pkg/env/defaults.go`

| Variable | Default | Description |
|----------|---------|-------------|
| `SANDAL_LIB_DIR` | `/var/lib/sandal` | Base directory for persistent state |
| `SANDAL_RUN_DIR` | `/var/run/sandal` | Runtime directory (PIDs, sockets) |
| `SANDAL_STATE_DIR` | `$SANDAL_LIB_DIR/state` | Container config JSON files |
| `SANDAL_IMAGE_DIR` | `$SANDAL_LIB_DIR/images` | Cached OCI images |
| `SANDAL_ROOTFSDIR` | `$SANDAL_LIB_DIR/rootfs` | Container root filesystems |
| `SANDAL_CHANGE_DIR` | `$SANDAL_LIB_DIR/changes` | Overlay upper directories |
| `SANDAL_HOST_NET` | `172.19.0.1/24` | Default bridge subnet |
| `SANDAL_LOG_LEVEL` | `info` | Logging level |
| `SANDAL_DAEMON_SOCKET` | `$SANDAL_RUN_DIR/sandal.sock` | Daemon Unix socket path |

These can be overridden via environment variables or the sandal daemon configuration.
