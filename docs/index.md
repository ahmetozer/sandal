---
hide:
  - navigation
  - toc
---
# Sandal

![icon](./sandal_logo.png){width="300" align=right}

## Welcome

Sandal is a lightweight portable container environment controller, designed and work with Linux systems.

## Goal of This Project

Sandal creates intermediate layer between host operating system and containers without requiring dedicated memory allocation like as virtual machines.  

- This project supports provision container from directories, immutable image files (IMG, Squash FS) so you can execute the container directly from the file, and easy to distribute and configure it with portable media.

- Portable container images gives ability to provision containers from outside the system host storage system such as SD cards, That enables to access and manage from outside (macOS, Windows) the host operating system when the system is offline.  

- Easy deployment, enables remote deployment without requiring any software or deep experience at field side.

- Additionally, these features create easy to manage embedded-Linux work environments or development surface without requiring to build own distribution ([Yocto](https://www.yoctoproject.org/), [Buildroot](https://buildroot.org/)) from scratch for each change.