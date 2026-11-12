# Exec

Execute a command under given container.

```bash
export MY_VAR="test_var"
sandal exec -env-pass MY_VAR new-york -- env
    PATH=/sbin:/bin:/sbin:/usr/bin:/usr/sbin:/usr/bin:/usr/sbin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/sbin
    MY_VAR=test_var
```

## Flags

### `-dir string`

:   working directory for the executed command.

### `-user string`

:   run the command as a specific user.

### `-t bool`

:   allocate a pseudo-TTY (for interactive shells).

### `-env-all bool`

:   pass all environment variables from the caller to the container.

### `-env-pass value`

:   pass a specific environment variable by name from the caller (repeatable).

```bash
sandal exec -env-pass MY_VAR -env-pass OTHER_VAR new-york -- env
```

### `-ns-<kind> string`

:   for `cgroup`, `ipc`, `mnt`, `net`, `pid` and `uts`: use another namespace
    of that kind instead of the container's. See [Namespaces](#namespaces).

## Namespaces

By default `sandal exec` joins every namespace of the container: cgroup, ipc,
mnt, net, pid and uts (a kind the container shares with the host, such as
`-ns-net host` at run time, stays on the host). Each `-ns-<kind>` flag changes
that for one kind. The values are the same as on
[`sandal run`](run.md#namespace-flags-ns-kind), with one difference: exec
never creates a namespace, so *unset* means the container's own.

| Value | Effect on exec |
| --- | --- |
| *(unset)* | The container's own namespace (joined through its init process). |
| `host` | Stay in the caller's namespace for this kind. |
| `<pid>` | Join the namespace of process `<pid>`, via `/proc/<pid>/ns/<kind>`. |
| `pid:<pid>` | Same as bare `<pid>`. |
| `file:<path>` | Join the namespace pinned at `<path>` (e.g. `/var/run/netns/blue` from `ip netns add blue`). Not accepted for `-ns-mnt`. |
| `cont:<name>` | Join the namespace of another running sandal container. |

A bad value (unknown syntax, a missing or stopped `cont:` container, a
`file:` mount target) fails before any namespace is joined.

```bash
# host tools against the container's network and hostname (like nsenter -n -u)
sandal exec -ns-mnt host -ns-pid host web -- tcpdump -ni eth0

# web's files, db's network
sandal exec -ns-net cont:db web -- ss -ltn

# a network namespace pinned by `ip netns add blue`
sandal exec -ns-net file:/var/run/netns/blue web -- ip addr
```

Things to know:

- **User namespace.** exec never joins the container's user namespace, and
  `-ns-user` is not offered: the kernel does not let a multi-threaded process
  such as sandal enter one. For a container run with its own user namespace
  (`-ns-user ""`), the command still joins the other namespaces but keeps the
  caller's credentials and capabilities. Files it creates are owned by the
  host uids, not the container's mapped ones.
- **`-ns-mnt`.** The mount namespace decides where the binary, `/etc/passwd`
  (for `-user`), the `-dir` path and `/dev/ptmx` (for `-t`) come from. With
  `-ns-mnt host` they are the host's, and the container's files are at
  `/proc/<container pid>/root`. With `-ns-mnt cont:<name>` or a pid, the
  command runs at the root of that process.
- **`-ns-pid`.** The command is started as a new process in the joined pid
  namespace. Tools like `ps` read `/proc` from the joined mount namespace, so
  with `-ns-pid` but `-ns-mnt host` they still list the host's processes.
- **`-ns-cgroup`** only changes the view of `/sys/fs/cgroup`. The command is
  not placed in the container's cgroup, so `-memory` and `-cpu` limits do not
  apply to it.
- **VM containers** (`-vm`, and every container on macOS). The flags are sent
  to the VM, and the targets refer to the VM's kernel: `host` is the VM's own
  namespace, pids and `file:` paths are the guest's, and `cont:` only finds
  the VM's own container. Namespaces cannot be shared across VMs. A VM booted
  with an older sandal ignores the flags and joins the container's
  namespaces; restart the VM after upgrading.
