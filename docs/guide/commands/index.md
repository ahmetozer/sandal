# Run

This sub command provisiones a new container to the system.

``` { .bash .annotate title="Example usage" }
sandal run -lw / -tmp 10 --rm --  bash
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
Default it is set to root folder `/`

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

### `-help bool`

:   show this help message

---

### `-hosts string`

:   cp (copy), cp-n (copy if not exist), image(use image) (default "cp")  
Allocation configuration of /etc/hosts file.

---

### `-lw value`

: Lower directory of the root file system  
  Lower directories are attach folders or images to container to access but changes are saved under `-chdir`.  
  This flag can usable multiple times to attach multiple images and directories to container.

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
    ```

    1. You can create SquashFS files with `sandal convert`.
    2. Image files consist of multiple partition, you have to specificly define partition information in commandline.
      You can find image details with `sandal image info file.img`

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
  ```

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

### `-ns-ns string`

:   ns namespace or host

---

### `-ns-pid string`

:   pid namespace or host

---

### `-ns-time string`

:   time namespace or host

---

### `-ns-user string`

:   user namespace or host

---

### `-ns-uts string`

:   uts namespace or host

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
  sandal run -rm -lw / -rci="ifconfig eth0" -- echo hello
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

### `-ro bool`

:   read only rootfs

---

### `-startup`

:   run container at startup by sandal daemon

---

### `-tmp uint`

:   allocate changes at memory instead of disk. Unit is in MB, when set to 0 (default) which means it's disabled.  
Benefical for:
:   - Provisioning ephemeral environments
    - Able to execute sandal under sandal or docker with tmpfs to prevent overlayFs in overlayFs limitations
    - Reduce disk calls for writing
    - Work with not supported file systems such as fat32, exfat

---

### `-user string`

:   Start container as custom user or user:group configuration.  
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
