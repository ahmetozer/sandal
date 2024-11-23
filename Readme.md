# Sandal

Sandal is a basic, lightweight container system for Linux-embedded systems.

This project aims to have a container system which light weight and respects systems disk usage.  
It utilizes the SquashFS file system as a container image, so you can execute the container directly from the file, and easy to distribute it with portable media.

## Installation

```bash
ARCH=amd64 # arm64, armv7, armv6, 386
wget https://github.com/ahmetozer/sandal/releases/latest/download/sandal-linux-${ARCH} -O /usr/bin/sandal
chmod +x /usr/bin/sandal
```

## Commands

Multiple sub commands are available on the system for different usage purposes.

### Run

Executing a new container image with given options.

Examples:

```sh
sandal run -lw=homeas.sq  -v=/run/dbus:/run/dbus -name=homeas -env-all \
-v=/mnt/homeassistant/config:/config  /init

sandal run -lw=/mnt/sandal/images/homeas.sq  -pod-ips="172.16.0.3/24;fd34:0135:0123::3/64" \
 -ns-net=host -v=/run/dbus:/run/dbus -name=homeas -v=/mnt/homeassistant/config:/config  /init

sandal run -lw=/mnt/sandal/images/octo.sq  -env-all --ns-net=host --name=octo \
-pod-ips="172.16.0.4/24;172.16.0.5/24;fd34:0135:0123::4/64"  -v=/mnt/octo:/octoprint/octoprint  -devtmpfs=/mnt/external/ /init

export MY_SECRET_1=1234
export MY_SECRET_2=asdf
export MY_SECRET_3=aaaaa
sandal run -lw=/root/alpine -rm -env-pass=MY_SECRET_1 -env-pass=MY_SECRET_3 env
MY_SECRET_1=1234
MY_SECRET_3=aaaaa
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
SANDAL_CHILD=/var/lib/sandal/containers/232gG0pVhDa9u8U3v5e75O/config.json
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

Listing namespace ID's

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

### Clear

Delete container files which they are started with `-rm` flag and not running state

```bash
sandal clear
```

### Convert

Creating SquashFS from existing containers.  
Note: this process requires podman or docker to get details for your container, and `squashfs-tools` to create a SquashFS archive image.

Example

```bash
podman run -it --rm --name cont1 alpine
sandal convert cont1
#[==================================================|] 92/92 100%
sandal run  -lw=cont1.sqfs /bin/ping 1.0.0.1
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
sandal run -d -lw=cont1.sqfs --name cont1 /bin/ping 1.0.0.1
sandal kill cont1

sandal kill 22JbC5X2ks59IViFLkuVfG
```

### Rerun

Kill and restart container process with same args and current environment variable.

```bash
sandal run -d -lw=cont1.sqfs --name cont1 /bin/ping 1.0.0.1
sandal rerun cont1
```

### Exec

Execute command under your container

```bash
sandal exec minecraft /bin/bash
```

### Cmd

Get last execution command.

```bash
sandal cmd minecraft
# Output
"sandal run -name=minecraft -keep -lw=/mnt/sandal/images/ubuntu.sq -pod-ips=172.16.0.4/24 -startup -d /sbin/runit"
```

### Daemon

Run all startup containers and watch in case of hang errors.

To enable SystemD or Runit, you can use `sandal daemon -install`.

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

### Environment Variables

The below variables which utilized by all sub commands

```bash
export SANDAL_LIB_DIR="/var/lib/sandal"
export SANDAL_RUN_DIR="/var/run/sandal"

export SANDAL_IMAGE_DIR="${SANDAL_LIB_DIR}/image"
export SANDAL_STATE_DIR="${SANDAL_LIB_DIR}/state"
export SANDAL_UPPERDIR="${SANDAL_LIB_DIR}/upper"

export SANDAL_WORKDIR="${SANDAL_RUN_DIR}/workdir"
export SANDAL_ROOTFSDIR="${SANDAL_RUN_DIR}/rootfs"
export SANDAL_SQUASHFSMOUNTDIR="${SANDAL_RUN_DIR}/squashfs"

export SANDAL_HOST_NET="172.16.0.1/24;fd34:0135:0123::1/64"
```

## Use cases

At below, you can see some examples of different scenarios and combinations.

If your compiled application require system lib files, you can use distro squash file and your second layer to start container.

```bash
# Custom app as overlay
mkdir -p myroot/bin/
cp myBinnary myroot/bin/
sandal run -name=mybinnary -keep -lw=ubuntu.sq -lw="$HOME/myroot/" /bin/myBinnary
```

In this example, alpine is used, and you can get alpine with below command

```bash
wget https://dl-cdn.alpinelinux.org/alpine/v3.20/releases/aarch64/alpine-minirootfs-3.20.1-aarch64.tar.gz -O alpine.tar.gz
mkdir alpine
tar -xvzf alpine.tar.gz -C alpine
rm alpine-minirootfs-3.20.1-aarch64.tar.gz
```

If you want to isolate your changes, you can start your system with lowerdir only.

```bash
# Start alpine from disk
sandal run -name=mybinnary -keep -lw="$HOME/alpine/" /bin/ash
```

Intermediate RootFS can be provisioned with temporary disk environment. Changes are saved on ram and deleted on exit.

```bash
# With ramdisk
sandal run -name=alpine -keep -lw="$HOME/alpine/" -tmp=100 /bin/ash
```

Attaching single binary with/without configuration files.

```bash
# Path of application on phsical disc /myApp/bin/myapp
sandal run -name=mybinnarywithdistro -keep -lw="$HOME/alpine/" -lw="/myApp/" /bin/myapp
# or locate your configs
ls /myconfigs/
    /etc/
        dnsmasq.conf
        hostpad.conf
sandal run -name=mybinnarywithdistro -keep -lw="$HOME/alpine/" -lw="/myApp/" -lw="/myconfig/" /bin/myapp
```

If you don't want to isolate your container file system with overlay, you can use -v to mount your system

```bash
sandal run -name=alpine -v=/root/alpine:/  /bin/ash
```

You can directly use physical disc with high performance with this approach as well

```bash
sandal run -name=mySsd -v=/mnt/myssd:/  /bin/bash
```

You can directly attach single binary to container environment as well

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
sandal run  -lw="/mnt/sandal/images/ubuntu.sq" -chd="/mnt/nvme1/develop/" /usr/bin/df -h
Filesystem      Size  Used Avail Use% Mounted on
overlay         916G  3.2G  867G   1% /
```
