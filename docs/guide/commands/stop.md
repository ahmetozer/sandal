# Stop

Stop or send custom signal to your container, system will not try to restart your container.

```bash
sandal stop klipper
```

## Flags

### `-signal int`

Define custom signal for kill request.

```bash
sandal stop -signal 3 homeasistant
```

### `-timeout string`

Kill commands wait to proccess complate and you can define time out for this command to give up.

```bash
sandal stop -signal 2 -timeout 10 homeasistant
```
