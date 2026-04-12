# Stop

Stop or send custom signal to your container, system will not try to restart your container.

```bash
sandal stop klipper
```

You can stop multiple containers at once:

```bash
sandal stop klipper homeassistant zigbee2mqtt
```

## Flags

### `-all`

Stop all running containers.

```bash
sandal stop -all
```

### `-signal int`

Define custom signal for stop request (default: 15 / SIGTERM).

```bash
sandal stop -signal 3 homeassistant
```

### `-timeout int`

Timeout in seconds to wait for the process to complete before sending SIGKILL (default: 30).

```bash
sandal stop -timeout 10 homeassistant
```
