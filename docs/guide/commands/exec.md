# Exec

Execute a command under given container.

```bash
export MY_VAR="test_var"
sandal exec -env-pass MY_VAR new-york -- env
    PATH=/sbin:/bin:/sbin:/usr/bin:/usr/sbin:/usr/bin:/usr/sbin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/sbin
    MY_VAR=test_var
```
