# Configuration

Each sub command will be configured by, flags and you can find details under [/commands/*](../commands/index.md) pages.  
Project wide configurations are use environment variables for setting.

## Environment Variables

You can list default, current and defined environement variable configuration with `sandal help` command.

```bash
sandal help
System variable information:
  Variable Name             Set by user                         Used as                             Default
  SANDAL_LIB_DIR            /tmp/sandal/lib                     /tmp/sandal/lib                     /var/lib/sandal
  SANDAL_RUN_DIR            /tmp/sandal/run                     /tmp/sandal/run                     /var/run/sandal
  SANDAL_IMAGE_DIR                                              /tmp/sandal/lib/image               /var/lib/sandal/image
  SANDAL_STATE_DIR                                              /tmp/sandal/lib/state               /var/lib/sandal/state
  SANDAL_CHANGE_DIR                                             /tmp/sandal/lib/changedir           /var/lib/sandal/changedir
  SANDAL_SNAPSHOT_DIR                                           /tmp/sandal/lib/snapshot            /var/lib/sandal/snapshot
  SANDAL_KERNEL_DIR                                             /tmp/sandal/lib/kernel              /var/lib/sandal/kernel
  SANDAL_TEMP_DIR                                               /tmp/sandal/lib/tmp                 /var/lib/sandal/tmp
  SANDAL_ROOTFSDIR                                              /tmp/sandal/run/rootfs              /var/run/sandal/rootfs
  SANDAL_IMMUTABLEIMAGEDIR                                      /tmp/sandal/run/immutable           /var/run/sandal/immutable
  SANDAL_HOST_NET           172.19.0.1/24,fd34:0135:0127::1/64  172.19.0.1/24,fd34:0135:0127::1/64  172.16.0.1/24,fd34:0135:0123::1/64
  SANDAL_SOCKET                                                 /tmp/sandal/run/sandal.sock         /var/run/sandal/sandal.sock
  SANDAL_LOG_LEVEL          debug                               debug                               warn
```

!!! note "macOS Defaults"
    On macOS, the default paths differ from Linux:

    - `SANDAL_LIB_DIR`: `~/.sandal/lib`
    - `SANDAL_RUN_DIR`: `~/.sandal/run`

    All derived paths (IMAGE_DIR, STATE_DIR, etc.) cascade from these base directories.

### SANDAL_LIB_DIR

This variable provides path for working directory for project which keeps the files.

### SANDAL_RUN_DIR

Directory allocation for current runtime such as mountings. After system reboot, those directories can be deleted by your operating system
and sandal will be re-allocate those paths.

### SANDAL_IMAGE_DIR

Default path for images such as SquashFS or Disk images.

### SANDAL_STATE_DIR

Your container execution configurations and states will save under given directory.

### SANDAL_CHANGE_DIR

File and folder changes under container will save under this directory.  
This has no effect unless you have been set -chdir argument while starting up the container.

### SANDAL_SNAPSHOT_DIR

Container snapshots created with `sandal snapshot` are stored under this directory.

### SANDAL_KERNEL_DIR

Cached Linux kernels and initramfs images used for VM execution are stored under this directory.

### SANDAL_TEMP_DIR

Temporary files created during image pulls and other operations are stored under this directory.

### SANDAL_ROOTFSDIR

Root file system which is seen by container environment. It is combunation if lowerlayers, mounted valumes and changes which is made in container.

### SANDAL_IMMUTABLEIMAGEDIR

Immutable images are require to be mounted to operating system for using at containers.

### SANDAL_HOST_NET

Default host network configuration.

### SANDAL_UPSTREAM_IF

Interface name (e.g. `eth0`, `enp1s0`) that the daemon watches for upstream IPv6 prefix changes.  
Empty by default — leave unset to disable dynamic IPv6.  
See [Dynamic IPv6](dynamic-ipv6.md).

### SANDAL_IPV6_MODE

Selects how the upstream prefix is learned. One of:

- `ndp-proxy` (default) — same `/64` on upstream and `sandal0`, with kernel NDP proxy entries on the upstream for each container IP.
- `pd` — sandal runs a DHCPv6-PD client on the upstream interface and sub-allocates a `/64` for `sandal0`.
- `off` — feature disabled even when `SANDAL_UPSTREAM_IF` is set; `sandal0` keeps `SANDAL_HOST_NET`'s static IPv6.

Only consulted when `SANDAL_UPSTREAM_IF` is non-empty. See [Dynamic IPv6](dynamic-ipv6.md).

### SANDAL_IPV6_PD_HINT

Optional prefix length hint sent in DHCPv6-PD requests (e.g. `60` to ask the server for a `/60`). Only meaningful when `SANDAL_IPV6_MODE=pd`. Empty (the default) sends no hint.

### SANDAL_SOCKET

Socket location of the sandal state service.

### SANDAL_LOG_LEVEL

Sandal software supports leveled logging and by default runs as `warn` mode.  
Supported levels are:

- debug
- info
- warn
- error
