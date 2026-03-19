# Attach

Attach to a running background container's console.

```bash
sandal attach my-container
```

## Console Modes

Sandal uses two console modes depending on how the container was started:

### Socket Mode

Used when the container is started with `-t -d` and the daemon is running. Provides a full PTY experience with terminal resize support.

```bash
sandal daemon &
sandal run -lw / -tmp 10 --rm -t -d --startup -name my-container -- bash
sandal attach my-container
    Attached to my-container (Ctrl+P, Ctrl+Q to detach)
```

- **Ctrl+C** sends SIGINT to the container process
- **Ctrl+P, Ctrl+Q** detaches without killing the container
- Terminal resizing is forwarded to the container

### FIFO Mode

Used when the container is started without the daemon or without `-t`. Provides basic stdin/stdout/stderr relay via files.

```bash
sandal run -lw / -tmp 10 --rm -d -name my-container -- ping 10.0.0.1
sandal attach my-container
    Attached to my-container (Ctrl+C to detach)
```

- **Ctrl+C** detaches from the container (container keeps running)

## Detach and Re-attach

After detaching, the container continues running in the background. You can re-attach at any time:

```bash
sandal attach my-container
# Ctrl+P, Ctrl+Q to detach
sandal attach my-container
# re-attached to the same session
```

## Flags

### `-help`

:   show the help message

```bash
sandal attach -help
```
