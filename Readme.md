# Sandal

Sandal is a basic, lightweight container system for Linux-embedded systems.

This project aims to have a container system which light weight and respects systems disk usage.  
It utilizes the squashfs filesystem as a container image, so you can execute the container directly from the file, and easy to distribute it with portable media.

## Installation

```bash
ARCH=amd64 # arm64, armv7, armv6, 386
wget https://github.com/ahmetozer/sandal/releases/latest/download/sandal-linux-${ARCH} -O /usr/bin/sandal
chmod +x /usr/bin/sandal
```

## Commands

Multiple subcommands are available on the system for different usage purposes.

### Run

Executing a new container image with given options.

| flag  | default  | description  |
|---|---|---|
| `-d` | false | Run your container at background  |
| `-startup` | false | Run your container at startup with sandal daemon |
| `devtmpfs` |   | Mount devtmpfs inside the container in the given location <br/> `-devtmpfs=/mnt/host/dev` |
| `-env-all` | false | Pass hosts enviroment variable to container |
| `-help` | false | print argument helps |
| `-host-ips` | 172.16.0.1/24;fd34:0135:0123::1/64 | host interface ip addresses |
| `-host-net` |  sandal0 |  host interface for bridge or macvlan |
| `-hosts` | cp | Behavior of `/etc/hosts` file. <br/>cp (copy), cp-n (copy if not exist), image(use image) |
| `-keep` | false | Do not remove container files on exit |
| `-name` |   | name of the container |
| `-net-type` | bridge | Type of host net type. bridge, macvlan, ipvlan  |
| `-ns-net` |   | net namespace or host |
| `-ns-pid` |   | pid namespace or host |
| `-ns-user` | host | user namespace or host |
| `-ns-uts` |   | uts namespace or host |
| `-pod-ips` |   | container interface ips |
| `-resolv` | cp-n | Behavior of `/etc/resolv` file. <br/>cp (copy), cp-n (copy if not exist), image (use image), 1.1.1.1;2606:4700:4700::1111 |
| `-ro` | false | read only rootfs |
| `-sq` |  | squashfs image location  |
| `-lw` |  | mount custom paths for lowerdirs (mutliple lower dir supported) |
| `-chd` |  | save change on different path or disk |
| `-rpp` |  | run command on main filesystem before pivoting to containers rootfs  |
| `-rpe` |  | run command after pivoting container's rootfs but before starting init |
| `-tmp` | 0 | allocate changes at memory instead of disk. unit is in MB, disk is used used by default |
| `-v` |   | attach system directory paths to container <br/> `-v=/mnt/homeasistant:/config` |

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

Example:

```bash
sandal ps
NAME                   SQUASHFS                       COMMAND   CREATED                   STATUS     PID
22IQkaDNhRp9okqsewZL19 /mnt/sandal/images/homeas.sqfs /bin/bash 2024-05-11T22:48:59+01:00 exit 0     815519
22J7btLUSru7kuDRbkxLpi alpine.sqfs                    /bin/ash  2024-05-12T15:18:32+01:00 exit 0     927465
22J7dGBOWOz5sgrXiwVpRx alpine.sqfs                    /bin/ash  2024-05-12T15:20:38+01:00 exit 0     928541
22JbC5X2ks59IViFLkuVfG alpine.sqfs                    /bin/ping 2024-05-12T19:38:31+01:00 hang       957321 <-
22JbvIh9nbT1CpkNgrB8K1 alpine.sqfs                    /bin/ash  2024-05-12T19:32:28+01:00 exit 130   953991
sandal ps -dry
NAME                   SQUASHFS                       COMMAND   CREATED                   STATUS     PID
22IQkaDNhRp9okqsewZL19 /mnt/sandal/images/homeas.sqfs /bin/bash 2024-05-11T22:48:59+01:00 exit 0     815519
22J7btLUSru7kuDRbkxLpi alpine.sqfs                    /bin/ash  2024-05-12T15:18:32+01:00 exit 0     927465
22J7dGBOWOz5sgrXiwVpRx alpine.sqfs                    /bin/ash  2024-05-12T15:20:38+01:00 exit 0     928541
22JbC5X2ks59IViFLkuVfG alpine.sqfs                    /bin/ping 2024-05-12T19:38:31+01:00 running    957321 <- Note here
22JbvIh9nbT1CpkNgrB8K1 alpine.sqfs                    /bin/ash  2024-05-12T19:32:28+01:00 exit 130   953991
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

### Rm

Delete container run files.

```bash
sandal rm alpine-1
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

To install `squashfs-tools`

```bash
#Debian, Ubuntu
sudo apt install squashfs-tools
#Fedora
sudo yum install squashfs-tools
#Alpine
apk add squashfs-tools
```

### Kill

Kill container process

Example

```bash
sandal run -d -sq=cont1.sqfs --name cont1 /bin/ping 1.0.0.1
sandal kill cont1

sandal kill 22JbC5X2ks59IViFLkuVfG
```

### Rerun

Kill and restart container proccess with same args and current enviroment variable.

```bash
sandal run -d -sq=cont1.sqfs --name cont1 /bin/ping 1.0.0.1
sandal rerun cont1
```

### Cmd

Get last execution command.

```bash
sandal cmd minecraft
# Output
"sandal run -name=minecraft -keep -sq=/mnt/sandal/images/ubuntu.sq -pod-ips=172.16.0.4/24 -startup -d /sbin/runit"
```

### Daemon

Run all startup containers and watch in case of hang errors.

To enable systemd or runit, you can use `sandal daemon -install`.

```bash
sandal daemon
2024/06/27 03:09:37 INFO daemon started
2024/06/27 03:10:01 INFO starting container=minecraft oldpid=102679
2024/06/27 03:10:03 INFO new container started container=minecraft pid=102759
2024/06/27 03:16:01 INFO starting container=homeas-2 oldpid=91491
2024/06/27 03:16:03 INFO new container started container=homeas-2 pid=103751
```

### Inspect

Get configuration file

```bash
sandal inspect minecraft
{
 "Name": "minecraft",
 "Created": 1719447001,
 "HostPid": 102751,
 "ContPid": 102759,
 "LoopDevNo": 128,
 "TmpSize": 0,
    ...}
```

## Use cases

At below, you can see some examples of different scenarios and combinations.

If your compiled application require system lib files, you can use distro squasfile and your second layer to start container.

```bash
mkdir -p myroot/bin/
cp myBinnary myroot/bin/
sandal run -name=mybinnary -keep -sq=/mnt/sandal/images/ubuntu.sq -lw="$HOME/myroot/" /bin/myBinnary
```

In this example, alpine is used and you can get alpine with below command

```bash
wget https://dl-cdn.alpinelinux.org/alpine/v3.20/releases/aarch64/alpine-minirootfs-3.20.1-aarch64.tar.gz
mkdir alpine
tar -xvzf alpine-minirootfs-3.20.1-aarch64.tar.gz -C alpine
rm alpine-minirootfs-3.20.1-aarch64.tar.gz
```

If you want to isolate your changes, you can start your system with lowerdir only.

```bash
sandal run -name=mybinnary -keep -lw="$HOME/alpine/" /bin/ash
```

With ramdisk

```bash
sandal run -name=alpine -keep -lw="$HOME/alpine/" -tmp=100 /bin/ash
```

With your binnary as layer

```bash
ls /myApp/bin/myapp
sandal run -name=mybinnarywithdistro -keep -lw="$HOME/alpine/" -lw="/myApp/" /bin/myapp
# or locate your configs
ls /myconfigs/
    /etc/
        dnsmasq.conf
        hostpad.conf
sandal run -name=mybinnarywithdistro -keep -lw="$HOME/alpine/" -lw="/myApp/" -lw="/myconfig/ /bin/myapp
```

If you don't want to isolate your continer file system with overlay, you can use -v to mount your system

```bash
sandal run -name=alpine -v=/root/alpine:/  /bin/ash
```

You can directly use pysical disc with high performance with this approach as well

```bash
sandal run -name=mySsd -v=/mnt/myssd:/  /bin/bash
```

You can directly attach single binnary to container enviroment as well

```bash
sandal run -name=tunnel -v=/root/testlw/bosphorus:/bosphorus  /bosphorus server
```

Altering routing table to only allow local access.

```bash
sandal run -v="/usr/bin/bosphorus" -rpp="/usr/bin/ip ro re unreachable default" -rpp="/usr/bin/ip ro add 10.0.0.0/8 via 172.16.0.1" --rpp="/usr/bin/ip ro show"  /usr/bin/bosphorus
```

Installing curl before executing as init.

```bash
sandal run -v="/usr/bin/bosphorus" -rpe="/sbin/apk add curl" -lw="/root/alpine"  /usr/bin/curl https://ahmet.network -lI
```

Save changes in different disk

```bash
sandal run  -sq="/mnt/sandal/images/ubuntu.sq" -chd="/mnt/nvme1/develop/" /usr/bin/df -h
Filesystem      Size  Used Avail Use% Mounted on
overlay         916G  3.2G  867G   1% /
```
