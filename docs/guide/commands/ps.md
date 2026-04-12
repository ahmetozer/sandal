# Ps

List your, containers with status informations.

```bash
sandal ps
NAME              LOWER                              COMMAND CREATED              STATUS PID
new-york ["/nvme1/sandal/images/debian.sq"] bash    2025-01-18T16:56:13Z          exit 0 280573
istanbul ["/"]                              bash    2024-09-21T14:29:51Z          exit 0 549032
```

## Flags

### `-dry bool`

:   do not verify running state of containers. Lists containers from state files without checking if their processes are still alive.

### `-ns bool`

:   show namespace information for each container.
