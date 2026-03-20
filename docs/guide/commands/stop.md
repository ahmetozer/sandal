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

Define custom signal for kill request.

```bash
sandal stop -signal 3 homeassistant
```

### `-timeout string`

Kill commands wait for the process to complete and you can define a timeout for this command to give up.

```bash
sandal stop -signal 2 -timeout 10 homeassistant
```
