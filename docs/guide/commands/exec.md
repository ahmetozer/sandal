# Exec

Execute a command under given container.

```bash
export MY_VAR="test_var"
sandal exec -env-pass MY_VAR new-york -- env
    PATH=/sbin:/bin:/sbin:/usr/bin:/usr/sbin:/usr/bin:/usr/sbin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/sbin
    MY_VAR=test_var
```

## Custom Namespaces

Sandal is flexiable to execute your custom process with in different namespace.

### Host

To keep nep process on default namespace while environment is in created container.

```bash
sandal exec --env-all --ns-net host test -- ifconfig
```

### Proccess ID (PID)

System is capable to provisioning new process from other processes namespaces.

```bash
sandal exec --env-all --ns-net pid:5321 test -- ifconfig
```

### File

Jump namespaces which is created from other tools is done by giving path of namespace endpoint.

```bash
ip netns create test
sandal exec --env-all --ns-net file:/var/run/netns/test test -- ifconfig
```
