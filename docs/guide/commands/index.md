# Run

This sub command provisiones a new container to the system.

``` { .bash .annotate title="Example usage" }
sandal run -lw / -tmp 10 --rm --  bash
```

When `-lw` provides a container image with an `ENTRYPOINT` or `CMD`, the trailing `-- command` is **optional** — the image's default command is used:

``` { .bash .annotate title="Run image default command" }
sandal run -lw alpine:latest -tmp 10 --rm
```

## Flags

| Flag Type   | Description                          |
| ----------- | ------------------------------------ |
| `bool`      | by default it is set to false, in case of presence, it will be true.  |
| `string`    | only accepts single string value. |
| `value`     | similar to string but multiple presences are accepted |

---

### `-cap-add value`

:   add capabilities to the container (multiple values allowed)  
  See [Linux Capabilities](https://man7.org/linux/man-pages/man7/capabilities.7.html) for available capabilities.  
  Example: `-cap-add NET_BIND_SERVICE -cap-add SYS_ADMIN`  
  By default, containers start with a restricted set of capabilities. This flag adds additional capabilities when needed.

---

### `-cap-drop value`

:   drop capabilities from the container (multiple values allowed)
  Remove specific capabilities even in privileged mode.
  Example: `-cap-drop SYS_ADMIN -cap-drop NET_ADMIN`
  This provides an additional layer of security by explicitly removing potentially dangerous capabilities.

---

### `--cpu string`

:   limit the number of CPUs available to the container
  Specify the maximum number of CPUs the container can use as a decimal value.
  Uses cgroup v2 cpu controller to enforce CPU throttling.
  Example: `--cpu 0.5` (half a CPU), `--cpu 2` (two CPUs)
  The value is rounded up to the nearest integer for display in `/proc/cpuinfo`.
  **Note:** Requires cgroup v2 with cpu controller available. If unavailable, a warning is logged but the container continues to run.

---

### `-chdir string`

:   container changes will saved this directory (default "/var/lib/sandal/changedir/new-york")

---

### `-d bool`

:   run container in background

---

### `-devtmpfs string`

:   mount point of devtmpfs  
example: -devtmpfs /mnt/devtmpfs  
[more info unix.stackexchange.com](https://unix.stackexchange.com/questions/77933/using-devtmpfs-for-dev)

---

### `-dir string`

:   working directory  
Default it is set to root folder `/`. When `-lw` provides an OCI image, the image's `WorkingDir` is used as the default if `-dir` is not set. When multiple `-lw` images are provided, the last image that defines `WorkingDir` wins.

---

### `-env-all bool`

:   send all enviroment variables to container  
Environment variables which currently you are seing at `env` command.

---

### `-env-pass value`

:   pass only requested enviroment variables to container  
For example you are set variable with `export FOO=BAR`, and `-env-pass FOO` will read variable from existing environment and passes to container.  
***It does not accepts `-env-pass FOO=BAR` for security purposes***

---

### `-entrypoint string`

:   override the OCI image `ENTRYPOINT`.
  When set, replaces the image's ENTRYPOINT with the given executable. Equivalent to Docker's `--entrypoint` flag.
  Combine with `--` arguments to compose the full command.

  ```bash
  # Run /bin/echo instead of the image's ENTRYPOINT
  sandal run -lw alpine:latest -tmp 64 -entrypoint /bin/echo -- "hello world"
  # Skip an image's ENTRYPOINT (e.g. to inspect a container that normally runs an init)
  sandal run -lw ghcr.io/home-assistant/home-assistant:latest -tmp 200 \
      -entrypoint /bin/sh -- -c "ls /usr/local/lib/python3.14/site-packages | head"
  ```

---

### `-help bool`

:   show this help message

---

### `-hosts string`

:   cp (copy), cp-n (copy if not exist), image(use image) (default "cp")  
Allocation configuration of /etc/hosts file.

---

### `-lw value`

: Lower directory of the root file system (default destination: `/`)
  Lower directories are attach folders or images to container to access but changes are saved under `-chdir`.
  This flag can usable multiple times to attach multiple images and directories to container.
  By default, lower directories are mounted at the root (`/`) of the container. Use `source:/container/path` syntax to mount at a custom path.

  In addition to local paths and image files, `-lw` accepts **container image references** from OCI registries. When the value is not a local path, sandal automatically pulls the image, flattens its layers into a squashfs image, and caches it under `SANDAL_IMAGE_DIR` for future use.

  **Multiple images.** When multiple `-lw` images are provided, the **last** image that defines a value wins for `ENTRYPOINT`, `CMD`, `WorkingDir`, and `User`. `ENV` vars accumulate from all images in `-lw` order, with later images overriding earlier ones on duplicate keys. This matches the intuition that the right-most `-lw` is the "outer" layer:

    ```bash
    # python:3-slim's PATH and PYTHON_VERSION win, alpine's unique ENV vars are kept
    sandal run -lw alpine:latest -lw python:3-slim -tmp 200 -- env

    # Run image's default CMD without providing `--`
    sandal run -lw alpine:latest -tmp 64
    ```

??? info "More"

    Example Commands

    ```bash
    # Single Lower Directory
    sandal run -lw /my/dir/lw1 -- bash
    # Multiple Lower Directories
    sandal run -lw /my/dir/lw1 -lw /my/dir/lw2 -lw /my/dir/lw3 -- bash
    # SquashFS # (1)
    sandal run -lw /my/img/debian.sqfs -lw /my/image/config.sqfs -- bash
    # Mounting .img file # (2)
    sandal run -tmp 1000 -lw /my/img/2024-11-19-raspios-bookworm-arm64-lite.img:part=2 \
    -lw /my/image/config.sqfs --rm -- bash
    # Container image from registry # (3)
    sandal run -lw public.ecr.aws/docker/library/busybox:latest -tmp 10 --rm -- sh
    # Docker Hub short name
    sandal run -lw alpine:latest -tmp 10 --rm -- sh
    # Multiple sources: registry image + local config overlay
    sandal run -lw ghcr.io/home-assistant/home-assistant:latest \
    -lw /my/image/config.sqfs -tmp 100 --rm -name homeassistant -- bash
    # Mount a host directory at a custom container path # (4)
    sandal run -lw / -lw /root:/mnt/myroot --rm -- ls /mnt/myroot/
    # Mount an OCI image at a custom container path
    sandal run -lw alpine:latest -lw nginx:latest:/opt/nginx --rm -- ls /opt/nginx/
    # Enable sub-mount discovery with :=sub # (5)
    sandal run -lw /:=sub --ns-net host --env-all --rm -- ls /root/
    ```

    1. You can create SquashFS files with `sandal export`.
    2. Image files consist of multiple partition, you have to specificly define partition information in commandline.
      You can find image details with `sandal image info file.img`
    3. Container images are pulled from the registry, layers are flattened and converted to squashfs. The result is cached so subsequent runs use the cached image without re-downloading.
    4. Use `source:/container/path` to mount a lower directory at a specific path inside the container as a mini-overlay with full COW behavior. The separator is `:/` (colon followed by slash).
    5. Append `:=sub` to opt-in to automatic sub-mount discovery. Without `:=sub`, sub-mounts are not included.

    :   #### Custom Mount Targets

    :     By default, lower directories are merged at the root (`/`) of the container overlay. You can mount a lower directory at a **custom container path** using `source:/target` syntax:

          ```bash
          -lw /host/path:/container/path
          -lw myimage.sqfs:/opt/data
          -lw nginx:latest:/opt/nginx
          ```

          Each targeted lower is mounted as a mini-overlay with its own upper/work directories, providing full copy-on-write behavior.

    :   #### Sub-Mount Discovery (`:=sub`)

    :     When a host directory contains sub-mounts on separate filesystems (e.g. `/root` on a separate ext4 partition under `/`), these are **not** included by default — overlayfs does not cross mount boundaries.

          Append `:=sub` to opt-in to automatic sub-mount discovery:

          ```bash
          sandal run -lw /:=sub -- bash     # / with all sub-mounts
          sandal run -lw /:/custom:=sub -- bash  # / at /custom with sub-mounts
          ```

          Paths already covered by `-v` (volume) are skipped to avoid conflicts.

    :   #### How Lower Directories Works ?

    :     Read file operation

    ``` mermaid
    graph LR
    M1([MyApp]) --Access--> O1[OverlayFs]
    O1 -- Return File --> M1

    O1[OverlayFs] --> E4
    E4{Exist at chdir ?} -- Yes | Read from --> C1[(ChangeDir)]
    E4 -- No | TRY --> E3

    E3{Exist at lw3 ?} -- Yes | Read from --> LW3[(lower3)]
    E3 -- No | TRY --> E2

    E2{Exist at lw2 ?} -- Yes | Read from --> LW2[(lower2)]
    E2 -- No | TRY --> E1

    E1{Exist at lw3 ?} -- Yes | Read from --> LW1[(lower1)]
    E1 -- No | File Not Found --> O1
    ```

    :   Write file operation

    ``` mermaid
    graph LR
    M1([MyApp]) --Write--> O1[OverlayFs]

    O1[OverlayFs] --> C1[(ChangeDir)]

    ```

---

### `--memory string`

:   limit the memory available to the container
  Specify the maximum amount of memory the container can use.
  Supports human-readable units: K, M, G, T (1000-based) or Ki, Mi, Gi, Ti (1024-based).
  Uses cgroup v2 memory controller to enforce memory limits (triggers OOM killer if exceeded).
  Custom `/proc/meminfo` is generated to show the limited memory inside the container.

  Example usage:
  ```bash
  sandal run --memory 512M -lw / -- bash    # 512 megabytes
  sandal run --memory 1G -lw / -- bash      # 1 gigabyte
  sandal run --memory 1Gi -lw / -- bash     # 1 gibibyte (1024-based)
  sandal run --memory 134217728 -lw / -- bash  # 128MB in bytes
  ```

  **Note:** Requires cgroup v2 with memory controller available. If unavailable, a warning is logged and only `/proc/meminfo` is customized (without kernel enforcement).

---

### `-name string`

:   name of the container (default "new-york")

---

### `-net value`

:   container network interface configuration
>
  ```bash
  # Allocate custom interface only
  sandal run -lw / -net "ip=172.19.0.3/24=fd34:0135:0127::9/64" -- bash
  # Allocate default and custom interface with different bridge
  sandal run -lw / -net "" -net "ip=172.19.0.3/24=fd34:0135:0127::9/64;master=br0" -- bash
  # Custom interface naming
  sandal run -lw / -net "" -net "name=pppoe;master=layer2" -- bash
  # Custom mtu or ethernet set
  sandal run -lw / -net "" -net "ether="aa:ee:81:f4:c0:d3";mtu=1480" -- bash
  # DHCP: obtain IP from an upstream DHCP server
  sandal run -lw / -net "ip=dhcp" -- bash          # dual-stack (DHCPv4 + DHCPv6)
  sandal run -lw / -net "ip=dhcp4" -- bash         # IPv4 only
  sandal run -lw / -net "ip=dhcp6" -- bash         # IPv6 only
  ```

??? info "DHCP"

    When `ip=dhcp`, `ip=dhcp4`, or `ip=dhcp6` is specified, the container runs a
    built-in DHCP client on its interface instead of receiving a statically
    allocated address.

    - **`ip=dhcp`** — runs both DHCPv4 and DHCPv6 (dual-stack).
    - **`ip=dhcp4`** — runs DHCPv4 only.
    - **`ip=dhcp6`** — runs DHCPv6 only. Failure is non-fatal (logged as a warning).

    The DHCP client retransmits every 2 seconds for up to 30 seconds. The
    obtained IP, default gateway, and DNS servers are applied automatically.

    **macOS (VM mode):** When running on macOS, containers default to DHCPv4
    automatically. The container's interface receives the VM's original MAC
    address so that the VZ NAT network accepts its frames. No manual `-net`
    flag is needed — DHCP is used by default.

---

### `-ns-cgroup string`

:   cgroup namespace or host

---

### `-ns-ipc string`

:   ipc namespace or host

---

### `-ns-mnt string`

:   mnt namespace or host

---

### `-ns-net string`

:   net namespace or host

---

### `-ns-pid string`

:   pid namespace or host

---

### `-ns-user string`

:   user namespace or host

---

### `-ns-uts string`

:   uts namespace or host

---

### `-p value`

:   publish a container port to the host (repeatable).
  Binds a listener on the host and forwards traffic into the container. Works in both native and VM (`-vm`) mode. The general form is:

    ```
    [scheme://]<host-endpoint>[:<container-endpoint>]
    ```

    **Scheme** (optional, default `tcp`): `tcp://`, `udp://`, `tls://`.
    **Host endpoint**: `<port>`, `<ip>:<port>`, or `unix://<path>`.
    **Container endpoint**: `<port>`, `unix://<path>`, or `tcp://<port>` / `udp://<port>` for cross-protocol forwarding. Defaults to the same port as the host when omitted.

    **Grammar reference:**

    ```
    -p 443                                       tcp 127.0.0.1:443  -> cont 127.0.0.1:443
    -p 0.0.0.0:443                               tcp 0.0.0.0:443    -> cont 127.0.0.1:443
    -p 0.0.0.0:443:8443                          tcp 0.0.0.0:443    -> cont 127.0.0.1:8443
    -p 0.0.0.0:443:unix:///tmp/l.sock            tcp 0.0.0.0:443    -> cont unix socket
    -p udp://0.0.0.0:443:unix:///tmp/l.sock      udp 0.0.0.0:443    -> cont unix dgram
    -p tls://0.0.0.0:443:8080                    TLS 0.0.0.0:443    -> cont 127.0.0.1:8080
    -p tls://0.0.0.0:443:unix:///tmp/l.sock      TLS 0.0.0.0:443    -> cont unix socket
    -p unix:///run/host.sock:8080                host unix stream   -> cont 127.0.0.1:8080
    -p unix:///run/host.sock:unix:///run/c.sock  host unix stream   -> cont unix stream
    -p udp://unix:///run/host.sock:53            host unix dgram    -> cont 127.0.0.1:53 udp
    ```

    **Examples:**

    ```bash
    # TCP: expose container port 8080 on host 0.0.0.0:8080
    sandal run -lw alpine -p 0.0.0.0:8080 -- nc -lp 8080

    # TCP with port remapping: host 443 -> container 8443
    sandal run -lw alpine -p 0.0.0.0:443:8443 -- my-server

    # TLS termination: host terminates TLS, container receives plaintext
    sandal run -lw alpine -p tls://0.0.0.0:443:8080 -- my-server

    # UDP on host -> TCP inside container (cross-protocol)
    sandal run -lw alpine -p udp://0.0.0.0:9000:tcp://9000 -- nc -lp 9000

    # Forward to a unix socket inside the container
    sandal run -lw alpine -p 0.0.0.0:8080:unix:///tmp/app.sock -- my-app

    # Host-side unix socket listener
    sandal run -lw alpine -p unix:///run/my.sock:8080 -- my-app

    # VM mode: same flags, traffic tunnels over vsock
    sandal run -lw alpine -vm -p 0.0.0.0:8080 -- nc -lp 8080
    ```

    **TLS**: a self-signed certificate is generated per container. The fingerprint is printed to stderr for pinning.

    **VM mode**: the host listener tunnels traffic through AF_VSOCK to a relay inside the VM guest that dials the container target. No TAP/bridge routing is used.

---

### `-rci value`

:   run command before init
>
  ```bash
  sandal run -rm -lw / -rci="ifconfig eth0" -- echo hello
  ```

---

### `-rcp value`

:   run command before pivoting.  
>
  ```bash
  sandal run -rm -lw / -rcp="ifconfig eth0" -- echo hello
  ```

---

### `-privileged bool`

:   give extended privileges to the container  
  When enabled, the container runs with all Linux capabilities (except those explicitly dropped with `-cap-drop`).  
  **⚠️ Warning:** This significantly increases security risks. Use only when absolutely necessary.  
  Equivalent to Docker's `--privileged` flag. The container will have access to host devices and can perform system-level operations.

---

### `-rdir string`

:   root directory of operating system for container init process (default "/")

---

### `-resolv string`

:   cp (copy), cp-n (copy if not exist), image (use image), 1.1.1.1;2606:4700:4700::1111 (provide nameservers) (default "cp")

---

### `-rm bool`

:   remove container files on exit

---

### `-snapshot string`

:   snapshot output path for container changes (squashfs image).
  When set, `sandal snapshot` will write the image to this path instead of the default location.
  On subsequent runs, if the snapshot file exists it is automatically mounted as the lowest-priority lower layer in the overlay, so previously saved changes are available inside the container.

  Example usage:
  ```bash
  # Run with a named snapshot location
  sandal run -lw / -name test --rm -- bash
  # Save changes while running
  sandal snapshot test
  # Run stateless container with snapshot
  sandal run -lw / -name test --rm -- bash
  # Re-run — previous snapshot
  # if snapshot is presented for this container at default path,
  # and -snapshot file is not presented container will be use defualt path snapshot 
  # with sandal snapshot command new snapshot is presented at new path
  sandal run -snapshot /data/mycontainer.sqfs -lw / -name test -- bash
  ```

  Without `-snapshot`, the default path is `SANDAL_SNAPSHOT_DIR/<name>.sqfs` (`/var/lib/sandal/snapshot/<name>.sqfs`).

---

### `-ro bool`

:   read only rootfs

---

### `-startup`

:   run container at startup by sandal daemon

---

### `-t bool`

:   allocate a pseudo-TTY for the container.
  Required for terminal programs like `htop`, `vim`, `iptraf-ng`, or any program that needs `isatty()` to return true.

  In **foreground** mode (`-t` without `-d`), the host terminal is set to raw mode and connected directly to the container's PTY. Arrow keys, Ctrl+C, and terminal resize work automatically.

  In **background** mode (`-t -d` with daemon running), the PTY is served over a Unix socket. Use `sandal attach` to connect. Supports mouse, arrow keys, function keys, and terminal resize.

  ```bash
  # Interactive foreground with PTY
  sandal run -lw / -tmp 10 --rm -t -- bash
  # Background with PTY (requires daemon)
  sandal daemon &
  sandal run -lw / -tmp 10 --rm -t -d --startup -name my-htop -- htop
  sandal attach my-htop
  ```

  !!! warning
      Using `-t -d` without the daemon results in FIFO mode (no PTY). Terminal programs will not display correctly. A warning is logged in this case.

---

### `-chdir-type string`

:   Change dir backing type (default `auto`).
  Controls how the overlayfs upper/work directory is backed:

    - **`auto`** — Automatically selects the best option. Uses `folder` on native Linux with a supported filesystem (ext4, xfs, btrfs). Falls back to `image` when running inside a VM (VirtioFS), on nested overlayfs, or on unsupported filesystems.
    - **`folder`** — Uses the change dir directly on disk. Requires the underlying filesystem to support overlayfs upper (ext4, xfs, btrfs with d_type).
    - **`image`** — Creates a sparse ext4 disk image, loop-mounts it, and uses it as the change dir. Works everywhere, including VirtioFS, nested overlayfs, and unsupported filesystems.

```bash
# Force image mode on native Linux
sandal run -lw alpine -chdir-type image -- ash
# Force folder mode (only on supported filesystems)
sandal run -lw alpine -chdir-type folder -- ash
```

---

### `-csize string`

:   Change dir disk image size (default `128m`).
  Accepts human-readable sizes: `128m`, `128mb`, `1g`, `1gb`, `512k`, `512kb` (case-insensitive, binary units).
  Raw bytes are also accepted as plain numbers.
  Only applies when `-chdir-type` is `image` (or `auto` resolves to `image`). The image is created as a sparse file, so it only consumes actual disk space as data is written.
  A warning is logged if `-csize` is set while the change dir type resolves to `folder`.

```bash
# 512MB change dir image
sandal run -lw alpine -csize 512m -- ash
# 2GB change dir image
sandal run -lw alpine -chdir-type image -csize 2g -- ash
```

---

### `-tmp uint`

:   allocate changes at memory instead of disk. Unit is in MB, when set to 0 (default) which means it's disabled.
  Overrides `-chdir-type` — when set, a tmpfs is always used regardless of the change dir type.
Benefical for:
:   - Provisioning ephemeral environments
    - Able to execute sandal under sandal or docker with tmpfs to prevent overlayFs in overlayFs limitations
    - Reduce disk calls for writing
    - Work with not supported file systems such as fat32, exfat

---

### `-user string`

:   Start container as custom user or user:group configuration.
  When `-lw` provides an OCI image, the image's `User` is used as the default if `-user` is not set. When multiple `-lw` images are provided, the last image that defines `User` wins.
>
  ```bash
  # chroot to given path
  sandal run -rm --lw / -user dnsmasq -- id
  uid=100(dnsmasq) gid=101(dnsmasq)
  sandal run -rm --lw / -user dnsmasq:wheel -- id
  uid=100(dnsmasq) gid=10(wheel)
  sandal run -rm --lw / -user 10:nogroup -- id
  uid=10(uucp) gid=65533(nogroup)
  sandal run -rm --lw / -user 10 -- id
  uid=10(uucp) gid=10(wheel)
  sandal run -rm --lw / -user adm:10 -- id
  uid=3(adm) gid=10(wheel)
  ```

### `-vm bool`

:   run the container inside a virtual machine.
  On Linux, uses KVM. On macOS, uses Apple Virtualization framework.
  Host-only flags (`-d`, `-startup`, `--name`, `--cpu`, `--memory`) are applied on the host side; the remaining flags are forwarded to the VM guest.

---

### `-v value`

:   volume mount point
>
  ```bash
  # chroot to given path
  sandal run -rm -v /mnt/disk1:/ -- bash
  #  attach file,attach path to custom path, attach path, and  to the container.
  sandal run -rm -v /etc/nftables.conf \
    -v /run/dbus \
    -v /etc/homeas/config:/config  -- bash
  ```
