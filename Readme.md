# Sandal

![icon](./docs/sandal_logo.png)

Sandal is a lightweight portable container environment controller, designed and work with Linux systems.

Sandal creates intermediate layer between host operating system and containers without requiring dedicated memory allocation like as virtual machines.

Sandal enables easy deployment at field side without requering any other application, copy files to sd cards is enough for Raspberry Pi based deployments.

## Install

### macOS (Apple Silicon)

```sh
brew tap ahmetozer/tap
brew install --cask sandal
```

### Linux

```sh
# Pick your arch: amd64, 386, arm64, armv7, armv6
ARCH=amd64
sudo wget https://github.com/ahmetozer/sandal/releases/latest/download/sandal-linux-${ARCH} -O /usr/bin/sandal
sudo chmod +x /usr/bin/sandal
```

### From source

```sh
go install github.com/ahmetozer/sandal@latest
```

See [docs/setup](./docs/setup/index.md) for boot-time daemon registration and other options.
