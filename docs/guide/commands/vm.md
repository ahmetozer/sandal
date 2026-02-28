# VM

Manage virtual machines using Apple Virtualization framework on macOS. This command boots Linux kernels in lightweight VMs with VirtioFS directory sharing, serial console access, and NAT networking.

!!! note "macOS Only"
    The `vm` subcommand requires macOS with Apple Virtualization framework. It is not available on Linux. The binary must be code-signed with the `com.apple.security.virtualization` entitlement.

``` { .bash .annotate title="Example usage" }
sandal vm run -kernel /path/to/Image -initrd /path/to/initramfs -mount data:/host/dir -- /bin/sh
```

## Subcommands

| Command       | Description                                              |
| ------------- | -------------------------------------------------------- |
| `run`         | Run an ephemeral VM (created at start, deleted on exit)  |
| `create`      | Create a new VM configuration                            |
| `start`       | Start a VM and attach serial console                     |
| `stop`        | Gracefully stop a running VM                             |
| `kill`        | Force kill a running VM                                  |
| `list`        | List all VMs with status                                 |
| `delete`      | Delete a VM configuration                                |
| `create-disk` | Create a raw disk image                                  |

---

## Run

Run an ephemeral VM that is automatically deleted on exit.

```bash
sandal vm run -kernel /path/to/Image -initrd /path/to/initramfs -- /bin/sh
```

### Flags

#### `-kernel string`

:   Path to the Linux kernel image (required).
  The kernel must be an uncompressed `Image` file compatible with the target architecture.

```bash
sandal vm run -kernel ./alpine/boot/Image -- /bin/sh
```

---

#### `-initrd string`

:   Path to the initial ramdisk (optional).
  When virtiofs mounts are specified, sandal automatically generates an initrd overlay that injects mount commands before `switch_root`.

```bash
sandal vm run -kernel ./boot/Image -initrd ./boot/initramfs-lts -- /bin/sh
```

---

#### `-cmdline string`

:   Kernel command line (default `console=hvc0`).
  The `console=hvc0` argument is required for serial console access through the VM.

```bash
sandal vm run -kernel ./boot/Image -cmdline "console=hvc0 quiet" -- /bin/sh
```

---

#### `-disk string`

:   Path to a writable disk image (optional).
  Use `sandal vm create-disk` to create a raw disk image. The disk is attached as a VirtIO block device.

```bash
sandal vm create-disk -path ./rootfs.img -size 4096
sandal vm run -kernel ./boot/Image -disk ./rootfs.img -- /bin/sh
```

---

#### `-iso string`

:   Path to an ISO image (optional, mounted as read-only disk).
  When virtiofs mounts are specified and no ISO is provided, sandal auto-generates a cloud-init NoCloud ISO to mount shares inside the guest.

```bash
sandal vm run -kernel ./boot/Image -iso ./cloud-init.iso -- /bin/sh
```

---

#### `-mount value`

:   Mount a host directory into the VM using VirtioFS (repeatable).
  Format: `tag:path` or `tag:path:ro` for read-only.
  Inside the guest, shares are mounted at `/mnt/<tag>`.

```bash
# Single mount
sandal vm run -kernel ./boot/Image -mount data:/home/user/project -- /bin/sh
# Multiple mounts
sandal vm run -kernel ./boot/Image \
  -mount src:/home/user/src \
  -mount config:/etc/myapp:ro -- /bin/sh
```

??? info "How VirtioFS Mounts Work"

    Sandal uses two strategies to auto-mount VirtioFS shares inside the guest:

    1. **Cloud-init ISO**: A NoCloud datasource ISO is generated with `runcmd` entries that load `virtiofs` kernel module and mount each share to `/mnt/<tag>`. This works with distributions that support cloud-init or tiny-cloud.

    2. **Initrd overlay**: When an initrd is provided and no ISO is specified, sandal extracts the `/init` script from the initramfs, injects mount commands before `switch_root`, and appends the modified init as a CPIO overlay. The kernel processes concatenated archives in order, so the modified `/init` takes precedence.

---

#### `-env value`

:   Environment variable for the init process (repeatable).
  Format: `KEY=VALUE`. Variables are passed via the kernel command line.

```bash
sandal vm run -kernel ./boot/Image -env "HOME=/root" -env "TERM=xterm" -- /bin/sh
```

---

#### `-cpus uint`

:   Number of virtual CPUs (default `2`).

```bash
sandal vm run -kernel ./boot/Image -cpus 4 -- /bin/sh
```

---

#### `-memory uint`

:   Memory allocation in MB (default `512`).

```bash
sandal vm run -kernel ./boot/Image -memory 1024 -- /bin/sh
```

---

#### `-name string`

:   VM name (auto-generated from PID if empty).
  The name is used for identifying the VM in `list`, `stop`, and `kill` commands.

```bash
sandal vm run -name my-vm -kernel ./boot/Image -- /bin/sh
```

---

## Create

Create a persistent VM configuration without starting it. The configuration is saved to `~/.sandal-vm/machines/<name>/config.json`.

```bash
sandal vm create -name alpine -kernel ./boot/Image -initrd ./boot/initramfs-lts \
  -disk ./rootfs.img -mount workspace:/home/user/code
```

All flags from `run` are available. Both `-name` and `-kernel` are required.

---

## Start

Start a previously created VM and attach the serial console.

```bash
sandal vm start -name alpine
```

### Flags

#### `-name string`

:   VM name (required).

---

## List

List all VMs with their status, resource configuration, and kernel path.

```bash
sandal vm list
```

Example output:

```
  alpine    [running (pid 12345)]  cpus=2  memory=512MB  kernel=/path/to/Image
  test-vm   [stopped]              cpus=4  memory=1024MB kernel=/path/to/Image
```

---

## Stop

Gracefully stop a running VM by sending SIGTERM to the host process.

```bash
sandal vm stop -name alpine
```

### Flags

#### `-name string`

:   VM name (required).

---

## Kill

Force kill a running VM. By default, sends SIGTERM first and waits 3 seconds before sending SIGKILL.

```bash
sandal vm kill -name alpine
```

### Flags

#### `-name string`

:   VM name (required).

#### `-force bool`

:   Skip SIGTERM, send SIGKILL immediately.

```bash
sandal vm kill -force -name alpine
```

---

## Delete

Delete a VM configuration and its associated files.

```bash
sandal vm delete -name alpine
```

### Flags

#### `-name string`

:   VM name (required).

---

## Create Disk

Create a sparse raw disk image for use with `-disk` flag.

```bash
sandal vm create-disk -path ./rootfs.img -size 4096
```

### Flags

#### `-path string`

:   Output disk image path (required).

#### `-size int`

:   Disk size in MB (default `4096`).

---

## Serial Console

The VM provides an interactive serial console through VirtIO. The host terminal is switched to raw mode so keyboard input is forwarded directly to the guest.

- **First Ctrl+C**: Requests a graceful VM shutdown (ACPI power button)
- **Second Ctrl+C**: Forces immediate VM stop

The terminal is automatically restored to normal mode when the VM exits.
