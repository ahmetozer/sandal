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

Define custom signal for kill request.

```bash
sandal kill -signal 3 homeassistant
```

### `-timeout string`

Kill commands wait for the process to complete and you can define a timeout for this command to give up.

```bash
sandal kill -signal 2 -timeout 10 homeassistant
```
