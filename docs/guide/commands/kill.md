# Kill

Stop or send custom signal to your container

```bash
sandal kill klipper
```

## Flags

### `-signal int`

Define custom signal for kill request.

```bash
sandal kill -signal 3 homeasistant
```

### `-timeout string`

Kill commands wait to proccess complate and you can define time out for this command to give up.

```bash
sandal kill -signal 2 -timeout 10 homeasistant
```
