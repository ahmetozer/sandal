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

## Flags

### `-install bool`

:   Install systemd or OpenRC service files for the sandal daemon.

### `-read-interval duration`

:   Use polling at the given interval instead of file system events for reloading state. Useful for network-attached storage (NFS, SMB) where file events are not supported.

```bash
sandal daemon -read-interval 5s
```

## Behaviors of Daemon Service

### Service Start

Provisiones containers which they have a `-d` and `--startup` arguments placed while creating with `sandal run` command.

### Service Stop

Stopping or restarting the daemon (SIGINT, SIGQUIT or SIGTERM, which is what `service sandal stop` and `restart` send) **leaves running `-d` containers running**. The next daemon start adopts them: it finds their processes alive, re-opens their `-p` port forwards, and goes on watching them. A daemon upgrade therefore does not restart your containers. To stop a container, use [`sandal stop`](stop.md) or [`sandal kill`](kill.md).

The systemd unit installed by `-install` uses `KillMode=process` and `RuntimeDirectoryPreserve=yes` for this, so systemd does not kill the containers either. A unit installed by an older version kills them on every stop: run `sandal daemon -install` again.

`-vm` containers are the exception: the daemon passes the signal on to them and, when one does not **shut down in 30 seconds**, sends `SIGKILL (9)`.

On a host shutdown, the init system ends the containers like any other process that is left: they get `SIGTERM`, then `SIGKILL`.

!!! note "Upgrading from an older daemon"
    Daemons before this change stop every `-startup` container when they receive SIGTERM, so a `service sandal restart` from such a version restarts them once. To upgrade without that, end the old daemon with SIGKILL, which skips its shutdown, then start the new one (OpenRC: `kill -KILL $(cat /run/sandal.pid); rc-service sandal zap; rc-service sandal start`).

### Container Death

Health check function checks existence of container Process ID (PID), if not, it re-provisions container with same run arguments.

A container that exits again within 30 seconds of being re-provisioned is restarted with a growing delay: 3 seconds, then twice as long each time, up to one minute. One that ran longer is re-provisioned at once.

### File Events at Sandal State Directory

In case of manual actions under Sandal state directly, system reads those actions and reloads files into daemon.  
**Note:** File Events only supported physically attached storage systems. Network attached storage (SMB, NFS) does capable to send those events.
