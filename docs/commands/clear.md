# Clear

`sandal clear` reclaims disk used by stopped containers and by artifacts
that no container references any more. By default it only removes
containers that were started with the `-rm` flag; flags below opt in to
the other cleanup scopes.

```bash
sandal clear
```

## What gets cleaned

| Scope              | What it removes                                                                                                 |
| ------------------ | --------------------------------------------------------------------------------------------------------------- |
| Stopped containers | Rootfs, changedir (folder or `.img`), and state file of non-running containers flagged for removal.             |
| `-images`          | `.sqfs` files in `SANDAL_IMAGE_DIR` that no container references (via `Lower` or mounted `ImmutableImages`).    |
| `-snapshots`       | `.sqfs` files in `SANDAL_SNAPSHOT_DIR` that no container references.                                            |
| `-orphans`         | Changedir entries and `.img` files in `SANDAL_CHANGE_DIR` whose container state file is missing.                |
| `-kernel-cache`    | Stale `initramfs-sandal-*.img` entries in `SANDAL_KERNEL_DIR`; keeps the most recently produced one.            |
| `-temp`            | Leftover temp files in `SANDAL_TEMP_DIR` from interrupted pulls.                                                |
| `-all`             | All of the above, plus every stopped container regardless of the `-rm` flag.                                    |

Alpine-provided kernels (`vmlinuz-virt-*`, `initramfs-virt-*`) are never
touched — they require a network download to restore.

## Safety

`sandal clear` refuses to delete any path that does not resolve inside
`SANDAL_LIB_DIR` or `SANDAL_RUN_DIR`. If a container's `ChangeDir`,
`RootfsDir`, or `Snapshot` was configured to live outside those
directories, the file is preserved and a warning is logged — your data
is safe even if you ran `sandal clear -all`. Symlinks are resolved
before the check, so a symlink planted inside the sandal dirs cannot
trick the cleanup routine into escaping.

## Dry run

Pass `-dry-run` to preview the exact list of files that would be
removed and the bytes that would be reclaimed. No disk changes are
made.

```bash
sandal clear -all -dry-run
```

The dry-run output is a faithful preview: running `sandal clear -all`
immediately afterwards deletes the same set of paths and reports the
same byte total.

## Examples

Remove only the containers started with `-rm`:

```bash
sandal clear
```

Reclaim everything that's safe to reclaim:

```bash
sandal clear -all
```

Clean only unused downloaded images:

```bash
sandal clear -images
```

Preview a full cleanup without touching disk:

```bash
sandal clear -all -dry-run
```

## Flags

```
Usage of clear:
  -all
        reclaim everything: stopped containers, unused images and
        snapshots, orphan changedirs, stale kernel cache
  -dry-run
        print what would be removed without deleting anything
  -help
        show this help message
  -images
        remove downloaded images under SANDAL_IMAGE_DIR that no
        container references
  -kernel-cache
        remove stale initramfs-sandal-*.img entries in
        SANDAL_KERNEL_DIR (keeps the most recent)
  -orphans
        remove changedir files/dirs and ext4 .img files whose container
        state file is missing
  -snapshots
        remove snapshots under SANDAL_SNAPSHOT_DIR that no container
        references
  -temp
        remove leftover temp files under SANDAL_TEMP_DIR from
        interrupted pulls
```
