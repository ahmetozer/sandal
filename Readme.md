# Sandal

Sandal is a basic, deamonless container system for Linux-embedded systems.

This project aims to have a container system which light weight and respects systems disk usage.  
It utilizes the squashfs filesystem as a container image, so you can execute the container directly from the file, and easy to distribute it with portable media.

## Commands

Multiple subcommands are available on the system for different usage purposes.

### Run

Executing a new container image with given options.

Options

- -devtmpfs string  
    Mount devtmpfs inside the container in the given location  
- -env-all  
    send all current existing environment variables to the container
- -help  
    show this help message
- -host-ips string  
    host interface IP’s (default "172.16.0.1/24;fd34:0135:0123::1/64")
- -host-net string  
    host root interface for bridge or MACVLAN (default "sandal0")  
    If it’s a bridge, sub-veth interfaces are attached, for MACVLAN, sub-interfaces forked from the main.
- -hosts string  
    cp, cp-n, image (default "cp")  
    cp: copy from root  
    cp-n: copy if it does not exist or is empty at the container image  
    image: do nothing, use if it exists on the container image  
- -name string  
    name of the container (default "Random")  
- -net-type string  
    bridge, macvlan, ipvlan (default "bridge")  
    Type of network the interfaces which is used while container hosts network
- -ns-net string  
    Use container net namespace or host  
- -ns-pid string  
    Use container pid namespace or host
- -ns-user string  
    Use container user namespace or host (default "host")
- -ns-uts string  
    Use container uts namespace or host
- -pod-ips string  
    Container network interface IP’s (default "172.16.0.2/24;fd34:0135:0123::2/64")
- -resolv string  
    cp, cp-n, image, {IP1};{IP2} (default "cp-n")  
    cp: Copy host’s resolv.conf file to container.  
    cp-n: Copy host’s resolv.conf file if the container image does not exist or is the empty  
    image: Do nothing, use the image’s resolv.conf if it exists.  
    ipaddress: you can provide multiple nameservers (IPv4 and IPv6) by argument, it is overwrites to container’s resov.conf.  
- -keep  
    do not remove container files on exit
    (for background proccesses, system it will always keep)
- -ro  
    read-only rootfs
- -sq string  
    squashfs image location (default "./rootfs.sqfs")
- -tmp uint  
    allocate changes at memory instead of disk. unit is in MB, disk is used by default
- -v value  
    volume mount point

Examples:

```sh
sandal run -sq=homeas.sq  -v=/run/dbus:/run/dbus -name=homeas -env-all \
-v=/mnt/homeassistant/config:/config  /init

sandal run -sq=/mnt/sandal/images/homeas.sq  -pod-ips="172.16.0.3/24;fd34:0135:0123::3/64" \
 -ns-net=host -v=/run/dbus:/run/dbus -name=homeas -v=/mnt/homeassistant/config:/config  /init

sandal run -sq=/mnt/sandal/images/octo.sq  -env-all --ns-net=host --name=octo \
-pod-ips="172.16.0.4/24;172.16.0.5/24;fd34:0135:0123::4/64"  -v=/mnt/octo:/octoprint/octoprint  -devtmpfs=/mnt/external/ /init
```

### Ps

Listing existing containers

Default ps command is assumes state file but if its deamon or form some reason host proccess killed, it is not updated so `-verify` checks proccess.

Example:

```bash
sandal ps
NAME                   SQUASHFS                       COMMAND   CREATED                   STATUS                                       PID
22IQkaDNhRp9okqsewZL19 /mnt/sandal/images/homeas.sqfs /bin/bash 2024-05-11T22:48:59+01:00 exit 0                                       815519
22J7btLUSru7kuDRbkxLpi alpine.sqfs                    /bin/ash  2024-05-12T15:18:32+01:00 exit 0                                       927465
22J7dGBOWOz5sgrXiwVpRx alpine.sqfs                    /bin/ash  2024-05-12T15:20:38+01:00 exit 0                                       928541
22JbC5X2ks59IViFLkuVfG alpine.sqfs                    /bin/ping 2024-05-12T19:38:31+01:00 running                                      957321 <- Note here
22JbvIh9nbT1CpkNgrB8K1 alpine.sqfs                    /bin/ash  2024-05-12T19:32:28+01:00 exit 130                                     953991
sandal ps -verify
NAME                   SQUASHFS                       COMMAND   CREATED                   STATUS                                       PID
22IQkaDNhRp9okqsewZL19 /mnt/sandal/images/homeas.sqfs /bin/bash 2024-05-11T22:48:59+01:00 exit 0                                       815519
22J7btLUSru7kuDRbkxLpi alpine.sqfs                    /bin/ash  2024-05-12T15:18:32+01:00 exit 0                                       927465
22J7dGBOWOz5sgrXiwVpRx alpine.sqfs                    /bin/ash  2024-05-12T15:20:38+01:00 exit 0                                       928541
22JbC5X2ks59IViFLkuVfG alpine.sqfs                    /bin/ping 2024-05-12T19:38:31+01:00 hang                                         957321 <-
22JbvIh9nbT1CpkNgrB8K1 alpine.sqfs                    /bin/ash  2024-05-12T19:32:28+01:00 exit 130                                     953991
```

Listing namespace id's

```bash
sandal ps -ns
NAME                   PID    CGROUPNS   IPC        MNT        NET        PIDNS      USERNS     UTS
22IQkaDNhRp9okqsewZL19 815519 4026532509 4026532507 4026532505 4026532510 4026532508 4026531837 4026532506
22J7btLUSru7kuDRbkxLpi 927465 4026532603 4026532601 4026532599 4026532604 4026532602 4026531837 4026532600
22J7dGBOWOz5sgrXiwVpRx 928541 4026532603 4026532601 4026532599 4026532604 4026532602 4026531837 4026532600
22JbC5X2ks59IViFLkuVfG 957321 4026532603 4026532601 4026532599 4026532604 4026532602 4026531837 4026532600
22JbvIh9nbT1CpkNgrB8K1 953991 4026532603 4026532601 4026532599 4026532604 4026532602 4026531837 4026532600
```

### Convert

Creating squashfs from existing containers.  
Note: this process requires podman or docker to get details for your container, and `squashfs-tools` to create a squashfs archive image.

Example

```bash
podman run -it --rm --name cont1 alpine
sandal convert cont1
#[==================================================|] 92/92 100%
sandal run  -sq=cont1.sqfs /bin/ping 1.0.0.1
```

### Kill

Kill container process

Example

```bash
sandal run -d -sq=cont1.sqfs --name cont1 /bin/ping 1.0.0.1
sandal kill cont1

sandal kill 22JbC5X2ks59IViFLkuVfG
```
