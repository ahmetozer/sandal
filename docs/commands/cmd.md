# Cmd

Print `sandal run ...` command of particular container or all containers.

```bash
sandal cmd new-york
    sandal run -name new-york -tmp 10 -lw /nvme1/sandal/images/debian.sq -- bash
```

```bash
sandal cmd -all
    sandal run -name new-york -tmp 10 -lw /nvme1/sandal/images/debian.sq -- bash
    sandal run -name istanbul -ns-net host -lw / -- nginx
    sandal run -lw / -- ping localhost
    ...
```
