# Attach

Attach to a running background container's console.

```bash
sandal attach my-container
```

## Console Modes

Sandal uses two console modes depending on how the container was started:

### Socket Mode (PTY)

Used when the container is started with `-t -d` and the daemon is running. Provides a full PTY with terminal resize, mouse, and arrow key support. Terminal programs like `htop`, `vim`, and `iptraf-ng` work in this mode.

```bash
sandal daemon &
sandal run -lw / -tmp 10 --rm -t -d --startup -name my-container -- htop
sandal attach my-container
    Attached to my-container (Ctrl+P, Ctrl+Q to detach)
```

- **Ctrl+C** sends SIGINT to the container process
- **Ctrl+P, Ctrl+Q** detaches without killing the container
- Terminal resizing is automatically forwarded
- Mouse input is supported (click, scroll)
- Arrow keys, function keys, and other special keys work correctly

!!! note
    The `-t` flag requires the daemon to be running. Without the daemon, background containers fall back to FIFO mode and terminal programs will not display correctly.

### FIFO Mode

Used when the container is started without the daemon or without `-t`. Provides basic stdin/stdout/stderr relay via files. Suitable for programs with simple text output like `ping` or log tailing.

```bash
sandal run -lw / -tmp 10 --rm -d -name my-container -- ping 10.0.0.1
sandal attach my-container
    Attached to my-container (Ctrl+C to detach)
```

- **Ctrl+C** detaches from the container (container keeps running)
- No PTY — terminal programs (`htop`, `vim`, etc.) will not work

## Detach and Re-attach

After detaching, the container continues running in the background. You can re-attach at any time:

```bash
sandal attach my-container
# Ctrl+P, Ctrl+Q to detach
sandal attach my-container
# re-attached to the same session
```

When re-attaching in socket mode, the terminal size, mouse tracking, and application cursor mode are automatically restored.

## Flags

### `-help`

:   show the help message

```bash
sandal attach -help
```
