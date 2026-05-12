# Dynamic IPv6

Track an upstream IPv6 prefix and renumber `sandal0` and running containers automatically when it changes. Useful when your ISP rotates prefix delegations or your upstream router renumbers its LAN.

## When to use it

A typical home or small-office topology:

```
ISP  ───►  Upstream router  ───►  Sandal host  ───►  sandal0  ───►  containers
                 (RA / DHCPv6-PD)         (configured here)
```

Without this feature, `sandal0` and every container hold the static IPv6 prefix from `SANDAL_HOST_NET` (default `fd34:0135:0123::1/64`). When the ISP rotates the prefix delegation, containers keep their old, now-unrouted addresses and lose external IPv6 connectivity.

With dynamic IPv6 enabled, the daemon watches an upstream interface, picks up the new prefix, and rewrites the bridge and every running container under the new prefix within a few seconds.

!!! note "Daemon only"
    Dynamic IPv6 runs only when `sandal daemon` is active. Daemonless installs still get the prefix once at container creation but do not renumber automatically.

## Two modes

Pick the one that matches what your upstream advertises.

| Mode | When to use | What sandal does |
| --- | --- | --- |
| **`ndp-proxy`** (default) | Upstream router only advertises a single `/64` via RA. | Same `/64` lives on the upstream interface and on `sandal0`. Per-container NDP proxy entries on the upstream make each container directly reachable as a first-class IPv6 host on the LAN. |
| **`pd`** | Upstream router supports DHCPv6-PD and can delegate a shorter prefix (e.g. `/60`). | Sandal runs a DHCPv6-PD client on the upstream, sub-allocates a `/64` from the delegation for `sandal0`, and lets the upstream router route the delegation. No NDP proxy needed. |
| **`off`** | You set `SANDAL_UPSTREAM_IF` for some other reason but want to keep `SANDAL_HOST_NET`'s static IPv6. | Daemon does not spawn the renumber service; bridge keeps its static IPv6 from `SANDAL_HOST_NET`. |

## Required host setup

1. Install `sandal` and have a daemon running ([`sandal daemon`](../commands/daemon.md)).
2. Make sure IPv6 forwarding is on:

    ```bash
    sysctl -w net.ipv6.conf.all.forwarding=1
    ```

3. Allow your upstream interface to accept RAs while forwarding:

    ```bash
    sysctl -w net.ipv6.conf.eth0.accept_ra=2
    ```

    Sandal sets these at daemon start if they are not already correct, but if NetworkManager or `systemd-networkd` manages the interface it will fight the change — set them in the network manager's own config.

4. (NDP-proxy mode only) Enable `proxy_ndp` on the upstream:

    ```bash
    sysctl -w net.ipv6.conf.eth0.proxy_ndp=1
    ```

    Sandal sets this automatically too.

## Configuration

Three environment variables control the feature, parsed from sandal's environment at daemon start ([configuration.md](configuration.md)):

| Variable | Default | Meaning |
| --- | --- | --- |
| `SANDAL_UPSTREAM_IF` | `""` | Upstream interface name. **Empty disables the feature entirely.** |
| `SANDAL_IPV6_MODE` | `ndp-proxy` | `ndp-proxy` \| `pd` \| `off`. Only consulted when `SANDAL_UPSTREAM_IF` is set. |
| `SANDAL_IPV6_PD_HINT` | `""` | Optional prefix length hint for DHCPv6-PD requests (`pd` mode only). Example: `60`. |

The existing `SANDAL_HOST_NET` keeps working: the IPv4 portion (`172.16.0.1/24` default) is still applied to `sandal0` statically; the IPv6 portion is used only as a bootstrap until the upstream watcher publishes a real prefix.

## Behavior on a prefix change

When the upstream prefix changes, the renumber service performs a **hard cutover**:

1. Acquires a process-wide single-flight lock (so concurrent container start/stop hooks serialize against the renumber).
2. Rewrites `sandal0`: keeps its interface-identifier (default `::1`), removes the old global IPv6, adds the new one.
3. For each running container that did **not** opt out (`dynamic=false`):
    1. Computes a new IPv6 in the new prefix, preserving the container's existing interface-identifier when possible (so allocator order keeps `::2`, `::3`, … stable across renumber).
    2. Enters the container's network namespace, removes the old global IPv6 on its interface, adds the new one.
    3. Updates and persists `Config.Net` so a subsequent `sandal rerun` is consistent.
4. (NDP-proxy mode) Reconciles the kernel's NDP proxy table on the upstream: adds entries for new container IPs, removes stale ones.
5. Releases the lock.

The old IPv6 is dropped immediately — **existing connections break**. The next renumber sees the new prefix as `current` and stays sticky on it until it disappears.

Events from the upstream are debounced (2 s window) and coalesced if they arrive while a renumber is already in flight; only the latest prefix is applied.

## Examples

### Home router with RA-only upstream (NDP-proxy mode)

The host has a single LAN interface `enp1s0` whose IPv6 is set by the upstream router via RA. The user wants containers on the same `/64`, reachable from any LAN device.

```bash title="systemd unit override for sandal daemon"
[Service]
Environment="SANDAL_UPSTREAM_IF=enp1s0"
Environment="SANDAL_IPV6_MODE=ndp-proxy"
```

```bash title="Run a container that gets a dynamic IPv6"
sandal run -d --name web -lw nginx:latest -p 0.0.0.0:8080:80
```

When the ISP rotates the prefix:

- `sandal daemon` logs `renumber: applying prefix=2001:db8:NEW::/64`.
- `ip -6 addr show dev sandal0` reflects the new prefix immediately.
- `sandal exec web -- ip -6 addr show dev eth0` shows the container's new global IPv6.
- `ip -6 neigh show proxy dev enp1s0` shows a proxy entry for the container's new IPv6 so upstream NS replies to it.

### Routed sub-prefix via DHCPv6-PD

Upstream router delegates a `/60`. The user wants sandal to claim one `/64` from that delegation and route it.

```bash title="Daemon configuration"
SANDAL_UPSTREAM_IF=wan0
SANDAL_IPV6_MODE=pd
SANDAL_IPV6_PD_HINT=60   # ask the server for a /60; omit to accept whatever it offers
```

```bash title="Run a container — same as before"
sandal run -d --name api -lw my-api:1
```

Sandal will Solicit → Advertise → Request → Reply against the upstream DHCPv6-PD server, sub-allocate the first `/64` from the delegation (e.g. `2001:db8:cafe:0::/64`), apply it to `sandal0`, and renumber containers. The upstream router is expected to install a route for the whole delegation pointing at the sandal host.

The lease is renewed at T1, rebound at T2 if renew fails, and re-solicited from scratch if the lease fully expires. On daemon shutdown the lease is Release'd cleanly.

### Pin a single container to a literal IPv6

Sometimes you want a specific service on a specific address that survives unaware of upstream prefix churn (for example, a ULA-only internal-only container). Use the `dynamic=false` token on its `-net` flag.

```bash
sandal run -d --name internal-only \
  -lw alpine:latest \
  -net "ip=fd00:internal::42/64;dynamic=false" \
  -- /run-something.sh
```

That container's `Net` entry is now opted out: future renumber passes skip it entirely. Other containers on the same bridge continue to renumber normally.

### Disable the feature even though an upstream interface is configured

Useful when another tool on the host owns IPv6 management and you want sandal to keep the static `SANDAL_HOST_NET` IPv6 on the bridge.

```bash
SANDAL_UPSTREAM_IF=eth0
SANDAL_IPV6_MODE=off
```

The daemon will not spawn the renumber service and `sandal0` keeps its `SANDAL_HOST_NET` IPv6 (default `fd34:0135:0123::1/64`).

### Inspecting what the daemon is doing

The renumber service logs at `info`. Tail it through your service manager:

```bash
journalctl -u sandal -f | grep -E "renumber|dhcp6"
```

You will see lines like:

```
INFO renumber: service started upstream=enp1s0 mode=ndp-proxy
INFO sysctl set key=net.ipv6.conf.enp1s0.proxy_ndp from=0 to=1
INFO renumber: applying prefix=2001:db8:abcd::/64
```

To list current NDP proxy entries:

```bash
ip -6 neigh show proxy dev <upstream-if>
```

To see the bridge prefix:

```bash
ip -6 addr show dev sandal0
```

## In-container DHCPv6 renewal

Independent of the upstream watcher, sandal now renews container-side DHCPv6 leases that you requested with `-net "ip=dhcp6"` or `-net "ip=dhcp"`. Before this change the lease was a one-shot exchange and the address vanished from the kernel when `valid_lifetime` expired. The renewal loop now:

- Sends Renew at T1 (default `0.5 × preferred_lifetime`).
- Falls back to Rebind at T2 (default `0.8 × preferred_lifetime`) if Renew fails.
- Re-Solicits from scratch with exponential backoff (5 s → 60 s cap) if the lease fully expires.
- Sends Release when the container shuts down so the server frees the binding.

No configuration is required — this is on by default for any container that runs DHCPv6.

## Troubleshooting

**Containers do not renumber when I change the prefix on the upstream router.**

- Verify `SANDAL_UPSTREAM_IF` matches the interface where the new RAs arrive.
- Confirm `net.ipv6.conf.<upstream>.accept_ra` is `2` (forwarding hosts need this).
- Run `ip -6 addr show dev <upstream>` and confirm the kernel actually accepted the new prefix.
- Tail the daemon log for `renumber: applying`.

**`sandal0` has no IPv6.**

- If you set `SANDAL_UPSTREAM_IF` but the watcher has not yet learned a prefix, the bridge stays bare for up to 10 seconds while bootstrap waits. Check the daemon log.
- If `SANDAL_IPV6_MODE=off`, the bridge keeps `SANDAL_HOST_NET`'s static IPv6 as it always did.

**NDP proxy entries are missing for a container.**

- Make sure the container actually has a global IPv6 (not just ULA / link-local).
- Confirm `sysctl net.ipv6.conf.<upstream>.proxy_ndp == 1`.
- Check that the upstream router is sending NS for the container's address; sandal proxies replies but does not initiate.

**The container had a working IPv6 connection, then it dropped.**

- Expected behavior on a hard renumber. Existing connections break by design; new connections work.

**My network manager keeps resetting `accept_ra` to `1`.**

- Sandal does not fight network managers. Configure `accept_ra=2` directly in NetworkManager / systemd-networkd / your manager of choice.

## Limitations

- **IPv4 is unchanged.** The bridge IPv4 still comes from `SANDAL_HOST_NET` and is not renumbered automatically.
- **Daemonless installs do not renumber running containers.** They get the prefix once at container creation, like before.
- **No connection preservation.** Renumber is a hard cutover — open connections drop. RFC 8978-style lifetime-managed renumbering is not implemented.
- **Single bridge only.** Multi-bridge or per-container alternative prefixes are not supported.
- **No multi-prefix.** If the upstream has multiple global prefixes, sandal picks one — the one with the longest remaining valid lifetime — and is sticky on it until it disappears.

## See also

- [`sandal daemon`](../commands/daemon.md) — daemon lifecycle.
- [`sandal run -net`](../commands/run.md#-net-value) — per-container network options including `dynamic=false`.
- [Networking design](../design/networking.md) — bridge, veth, and DHCP fundamentals.
- [Configuration](configuration.md) — full list of `SANDAL_*` environment variables.
