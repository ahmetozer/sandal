# Configuration

Each sub command will be configured by, flags and you can find details under [/guide/commands/*](../../guide/commands) pages.  
Project wide configurations are use environment variables for setting.

## Environment Variables

You can list default, current and defined environement variable configuration with `sandal help` command.

```bash
sandal help
System variable information:
  Variable Name             Set by user                         Used as                             Default
  SANDAL_LIB_DIR            /tmp/sandal/lib                     /tmp/sandal/lib                     /var/lib/sandal
  SANDAL_RUN_DIR            /tmp/sandal/run                     /tmp/sandal/run                     /var/run/sandal
  SANDAL_IMAGE_DIR                                              /tmp/sandal/lib/image               /var/lib/sandal/image
  SANDAL_STATE_DIR                                              /tmp/sandal/lib/state               /var/lib/sandal/state
  SANDAL_CHANGE_DIR                                             /tmp/sandal/lib/changedir           /var/lib/sandal/changedir
  SANDAL_ROOTFSDIR                                              /tmp/sandal/run/rootfs              /var/run/sandal/rootfs
  SANDAL_IMMUTABLEIMAGEDIR                                      /tmp/sandal/run/immutable           /var/run/sandal/immutable
  SANDAL_HOST_NET           172.19.0.1/24;fd34:0135:0127::1/64  172.19.0.1/24;fd34:0135:0127::1/64  172.16.0.1/24;fd34:0135:0123::1/64
  SANDAL_SOCKET                                                 /tmp/sandal/run/sandal.sock         /var/run/sandal/sandal.sock
  SANDAL_LOG_LEVEL          debug                               debug                               info
```

### SANDAL_LIB_DIR

This variable provides path for working directory for project which keeps the files.

### SANDAL_RUN_DIR

Directory allocation for current runtime such as mountings. After system reboot, those directories can be deleted by your operating system
and sandal will be re-allocate those paths.

### SANDAL_IMAGE_DIR

Default path for images such as SquashFS or Disk images.

### SANDAL_STATE_DIR

Your container execution configurations and states will save under given directory.

### SANDAL_CHANGE_DIR

File and folder changes under container will save under this directory.  
This has no effect unless you have been set -chdir argument while starting up the container.

### SANDAL_ROOTFSDIR

Root file system which is seen by container environment. It is combunation if lowerlayers, mounted valumes and changes which is made in container.

### SANDAL_IMMUTABLEIMAGEDIR

Immutable images are require to be mounted to operating system for using at containers.

### SANDAL_HOST_NET

Default host network configuration.

### SANDAL_SOCKET

Socket location of the sandal state service.

### SANDAL_LOG_LEVEL

Sandal software supports leveled logging and by default runs as `info` mode.  
Supported levels are:

- debug
- info
- warn
- error
