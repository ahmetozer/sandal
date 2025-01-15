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

### `-host-ips string`

:   host interface ips (default "172.16.0.1/24;fd34:0135:0123::1/64")

---

### `-host-net string`

:   host interface for bridge or macvlan (default "sandal0")

---

### `-hosts string`

:   cp (copy), cp-n (copy if not exist), image(use image) (default "cp")  
Allocation configuration of /etc/hosts file.

---

### `-lw value`

: Lower directory of the root file system  
  Lower directories are attach folders or images to container to access but changes are saved under `-chdir`.  
  This flag can usable multiple times to attach multiple images and directories to container.

!!! note
Lorem ipsum dolor sit amet, consectetur adipiscing elit. Nulla et euismod
nulla. Curabitur feugiat, tortor non consequat finibus, justo purus auctor
massa, nec semper lorem quam in massa.
!!!

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

### `-name string`

:   name of the container (default "new-york")

---

### `-net-type string`

:   bridge, macvlan, ipvlan (default "bridge")

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

:   user namespace or host (default "host")

---

### `-ns-uts string`

:   uts namespace or host

---

### `-pod-ips string`

:   container interface ips

---

### `-rdir string`

:   root directory of operating system (default "/")

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
