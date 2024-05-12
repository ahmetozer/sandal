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
- -rm  
    remove container files on the exit
- -ro  
    read-only rootfs
- -sq string  
    squashfs image location (default "./rootfs.sqfs")
- -tmp uint  
    allocate changes at memory instead of disk. unit is in MB, disk is used by default
- -v value  
    volume mount point

Example executions

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

Options

- -help  
    Show help message.
- -ns  
    List with namespaces.
- -verify
    Verify the container process is running with sending signal 0.
