# Clear

Clear removes stale containers which they have `-rm` flag during execution.

```bash
sandal clear
```

To clear all containers which they are not in running state you can execute clear with `-all` argument

```bash
sandal clear -all
```

```bash
Usage of exec:
  -all
        delete all containers which they are not in running state
  -help
        show this help message
```
