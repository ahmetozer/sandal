# Daemon

You can start your containers with run command but for following requirements suggested to use daemon service instead of standalone.
  
- Automatically starting containers at the boot
- Rerun container when process is exits
- Work with read-only disk system and keep current state at memory
- Reduce read and write calls to system disk for updating or getting state information

## Registering the Service

Installation of daemon supports Systemd and Open RC init systems.

```sh
sandal daemon -install
```

You can start service with your system init and service controller.

```{ .sh .annotate title="managing" }
sh title="service control" annote
# service sandal [start|stop|restart]
service sandal start # (1)!
```

1. Output of service command varies between Systemd and OpenRC but it does not have impact.

## Behaviors of Daemon Service

### Service Start

Provisiones containers which they have a `-d` and `--startup` arguments placed while creating with `sandal run` command.

### Service Stop

Daemon has signal proxy to transfer received following signals to containers.

- SIGINT (2)
- SIGQUIT (3)
- SIGTERM (15)

If container does not gracefully **shut down in 30 second** when the signal is recived by contianer, daemon itself will send `SIGKILL (9)` to the container for ending the process.

### Container Death

Health check function checks existence of container Process ID (PID), if not, it re-provisions container with same run arguments.

### File Events at Sandal State Directory

In case of manual actions under Sandal state directly, system reads those actions and reloads files into daemon.  
**Note:** File Events only supported physically attached storage systems. Network attached storage (SMB, NFS) does capable to send those events.
