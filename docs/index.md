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

Sandal is a single-binary container runtime for systems that aren't always online, aren't always Linux, and don't always have room for a background daemon. It sits between the host OS and its containers as a lightweight isolation layer — without the memory overhead of a virtual machine.

- **One static binary, no required daemon.** A single executable runs containers directly — no background service, no image store on shared disk, nothing to install on the far end. An optional daemon exists only for `-startup` workloads that need to survive reboots.

- **OCI-compliant registry client, built in.** Pulls directly from Docker Hub, `ghcr.io`, and `quay.io`, flattens the layers, and caches them locally — no registry configuration, no side services.

- **Containers as portable files.** Provision from directories, immutable images (IMG, SquashFS), or registry references, and execute directly off the file — so a container can live on an SD card or USB drive and be built, inspected, or managed from macOS or Windows even when the Linux host is offline.

- **Optional microVM, same CLI.** Add `-vm` to run under KVM (Linux) or Apple Virtualization (macOS) without changing any other flag — useful when untrusted work needs a kernel boundary of its own.

- **Embedded Linux without a full build.** Get a manageable embedded-Linux work environment without rebuilding a custom distribution with [Yocto](https://www.yoctoproject.org/) or [Buildroot](https://buildroot.org/) from scratch for every change.

<!--
  Homepage explainer. Loaded only on /. Script order is load-bearing:
  Icons → Sources → LiveDiagram → Explainer provide globals consumed by
  later files. `explainer.js` is a plain script with a retry loop because
  the `type="text/babel"` scripts transpile async via @babel/standalone;
  it also subscribes to mkdocs-material's `document$` observable to
  re-mount on `navigation.instant` page swaps. Don't reorder.
-->
<div id="sandal-explainer"></div>

<script crossorigin src="https://unpkg.com/react@18.3.1/umd/react.production.min.js"></script>
<script crossorigin src="https://unpkg.com/react-dom@18.3.1/umd/react-dom.production.min.js"></script>
<script src="https://unpkg.com/@babel/standalone@7.29.0/babel.min.js"></script>
<script type="text/babel" src="assets/explainer/Icons.jsx"></script>
<script type="text/babel" src="assets/explainer/Sources.jsx"></script>
<script type="text/babel" src="assets/explainer/LiveDiagram.jsx"></script>
<script type="text/babel" src="assets/explainer/Explainer.jsx"></script>
<script src="assets/explainer/explainer.js"></script>