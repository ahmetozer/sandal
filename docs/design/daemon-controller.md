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
  Every interval:
    For each container in state directory:
      Check if process PID is alive (kill -0)
      If zombie: reap with waitpid
      If dead and Startup=true: restart
      If dead and not Startup: mark as stopped
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
