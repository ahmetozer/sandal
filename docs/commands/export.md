# Export

Export a container filesystem or a custom directory as a squashfs image.

```bash
sandal export mycontainer output.sqfs
output.sqfs
```

## Flags

### `-from string`

:   create squashfs from a custom directory instead of a container

```bash
sandal export -from /my/rootfs output.sqfs
output.sqfs
```

### `-i string`

:   include path — only export content under these paths (can be specified multiple times). If not set, everything is included.

```bash
sandal export -from /srv/rootfs -i /etc -i /bin output.sqfs
```

### `-e string`

:   exclude path — skip content under these paths (can be specified multiple times). Excludes take priority over includes.

```bash
sandal export mycontainer -e /tmp -e /var/cache output.sqfs
```

### `-help bool`

:   show help message

## Usage

### From a Container

Export the full filesystem of a running container:

```bash
sandal export mycontainer /backup/mycontainer.sqfs
/backup/mycontainer.sqfs
```

The container must be running so that its rootfs directory is available.

### From a Custom Directory

Use `-from` to create a squashfs image from any directory on the host, without needing a running container:

```bash
sandal export -from /srv/alpine-rootfs alpine.sqfs
alpine.sqfs
```

This is useful for packaging a pre-built root filesystem or any arbitrary directory tree into a squashfs image that can later be used as a container lower layer.

### From a Container Image

Use `-image` to pull a container image from a registry, flatten its layers, and export it as a squashfs image:

```bash
sandal export -image public.ecr.aws/docker/library/busybox:latest -o busybox.sqfs
busybox.sqfs
```

Use `-targz` to export as a gzip-compressed tar instead of squashfs:

```bash
sandal export -image public.ecr.aws/docker/library/busybox:latest -targz -o busybox.tar.gz
busybox.tar.gz
```

## Flags (Image Export)

### `-image string`

Container image reference to pull from a registry.

### `-targz`

Export as tar.gz instead of squashfs (only with `-image`).

### `-o string`

Output file path (required with `-image`, optional otherwise).
