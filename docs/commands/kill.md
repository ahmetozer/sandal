# Kill

Send a signal to your container. Default signal is SIGKILL (9).

```bash
sandal kill klipper
```

You can kill multiple containers at once:

```bash
sandal kill klipper homeassistant zigbee2mqtt
```

## Flags

### `-all`

Kill all running containers.

```bash
sandal kill -all
```

### `-signal int`

Define custom signal for kill request (default: 9 / SIGKILL).

```bash
sandal kill -signal 3 homeassistant
```

### `-timeout int`

Timeout in seconds to wait for the process to complete (default: 5).

```bash
sandal kill -signal 2 -timeout 10 homeassistant
```
