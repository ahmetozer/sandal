# Snapshot

Save container changes (upper workdir) as a squashfs image. Only the modifications made inside the container are captured — the base image is not included.

```bash
sandal snapshot test
/var/lib/sandal/snapshot/test.sqfs
```

## Flags

### `-f string`

:   custom output file path (default: `SANDAL_SNAPSHOT_DIR/<container>.sqfs`)

```bash
sandal snapshot -f /backup/test-snapshot.sqfs test
/backup/test-snapshot.sqfs
```

### `-i string`

:   include path — only snapshot content under these paths (can be specified multiple times). If not set, everything is included.

```bash
sandal snapshot -i /folder1/ -i /folder2/folder2-1/ test
```

### `-e string`

:   exclude path — skip content under these paths (can be specified multiple times). Excludes take priority over includes.

```bash
sandal snapshot -e /tmp test
```

### `-help bool`

:   show help message

## Include and Exclude Filters

Use `-i` and `-e` to control which paths end up in the snapshot. This keeps unrelated data (caches, logs, temp files) out of the image.

Include only `/folder1/` and `/folder2/folder2-1/`, but exclude `/folder1/tmp`:

```bash
sandal snapshot -i /folder1/ -i /folder2/folder2-1/ -e /folder1/tmp test
```

Snapshot everything except `/tmp`:

```bash
sandal snapshot -e /tmp test
```

When no `-i` is specified, all paths are included by default. Excludes always take priority over includes.

## How It Works

1. Locates the container's upper directory (change dir), which holds all filesystem modifications.
2. If a previous snapshot already exists, it is merged with the current changes using a read-only overlay so that accumulated changes are preserved across successive snapshots.
3. Creates a squashfs image from the (merged) changes.

``` mermaid
graph LR
    S1([sandal snapshot]) --> C1{Previous snapshot?}
    C1 -- No --> W1[Create sqfs from upper dir]
    C1 -- Yes --> M1[Mount previous sqfs]
    M1 --> O1[Overlay: upper dir + previous sqfs]
    O1 --> W2[Create sqfs from merged view]
```

## Snapshot as Lower Layer

When a container is started with `sandal run`, if a snapshot file exists for that container it is automatically mounted and appended as the lowest-priority lower directory in the overlay filesystem. This means previously saved changes are available inside the container without manual configuration.

```bash
# First run — make changes
sandal run -lw / -name myapp -- bash -c "echo hello > /data.txt"

# Snapshot the changes
sandal snapshot myapp

# Second run — /data.txt is available from the snapshot
sandal run -lw / -name myapp -- cat /data.txt
hello
```

### Custom Snapshot Path

Use the `-snapshot` flag on `sandal run` to set a custom snapshot location:

```bash
sandal run -snapshot /data/myapp.sqfs -lw / -name myapp -- bash
```

### Works with `-tmp` Flag

Snapshots work with containers using tmpfs-backed changes (`-tmp` flag). The snapshot command correctly resolves the upper directory from the tmpfs location.

```bash
sandal run -tmp 100 -lw / -name ephemeral -- bash
# Changes are in memory, but can still be persisted:
sandal snapshot ephemeral
```
