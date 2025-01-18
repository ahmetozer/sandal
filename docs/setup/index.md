# Installation

This software is independent to work with variety Linux distribution, it is only requiring some Linux kernel capabilities which is supported by most of the ready-made distribution.

## From GitHub

System is single binary and for installation, you can directly download from GitHub releases.

Set your system architecture.
For Raspberry Pi 4 and newer you can set arm64 for others, armv7 and first generation can use armv6.

???+ abstract "Available Prebuild Binary Architectures"
    ```sh
    ARCH=amd64  # 64 Bit regular system
    ARCH=386    # Old x86 machines
    ARCH=arm64  # 64 Bit Arm, Raspberry Pi 4, Raspberry PI 5
    ARCH=armv7  # 32 Bit Arm, Raspberry Pi 3-4-5
    ARCH=armv6  # Raspberry Pi 1 and other old SBCs
    ```

Download prebuild binary.

```sh
sudo wget https://github.com/ahmetozer/sandal/releases/latest/download/sandal-linux-${ARCH} -O /usr/bin/sandal
```

Set executable permission

```sh
sudo chmod +x /usr/bin/sandal
```

Test downloaded version.

```sh
sandal help
```

## From Source

If Golang is already installed, you can get and build at locally.

```sh
go install github.com/ahmetozer/sandal@latest
sandal help
```

## Starting at boot

System has own daemon to run your containers at startup. To achieve this, you need to register sandal daemon service to your init component.

Registration information are available at [Daemon](../guide/commands/daemon/#registering-the-service)
