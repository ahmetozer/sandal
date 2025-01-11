# Installation

Set your system architecture.
For Raspberry Pi 4 and newer you can set arm64 for others, armv7 and first generation can use armv6.

```bash
ARCH=amd64 # arm64, armv7, armv6, 386
```

Download prebuild binary.

```bash
wget https://github.com/ahmetozer/sandal/releases/latest/download/sandal-linux-${ARCH} -O /usr/bin/sandal
```

Set executable permission

```bash
chmod +x /usr/bin/sandal
```

Test downloaded version.

```bash
sandal help
```
