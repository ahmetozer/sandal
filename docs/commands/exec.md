# Exec

Execute a command under given container.

```bash
export MY_VAR="test_var"
sandal exec -env-pass MY_VAR new-york -- env
    PATH=/sbin:/bin:/sbin:/usr/bin:/usr/sbin:/usr/bin:/usr/sbin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/sbin
    MY_VAR=test_var
```

## Exit status

`sandal exec` exits with the status of the command, with or without `-t`, for
containers on the host and in a VM: the command's exit code, or 128 + the
signal number when a signal ended it (137 for `SIGKILL`). Errors of sandal
itself, such as a container or a command that is not found, exit 1.

```bash
sandal exec -t new-york -- sh -c 'exit 3'; echo $?
    3
```

A VM started by a sandal version without this support cannot report the
status: exec into it exits 0 once the command ends.

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
