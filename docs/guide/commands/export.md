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
