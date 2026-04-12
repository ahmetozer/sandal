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
