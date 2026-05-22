# Dynamic IPv6

Give your containers public IPv6 addresses from your ISP's prefix, and keep them current when the prefix rotates.

```
ISP  ───►  Upstream router  ───►  Sandal host  ───►  sandal0  ───►  containers
              (your eth0)                            (bridge)        (alpine, nginx, …)
```

In short: a container started with `sandal run` gets an address inside whatever IPv6 prefix your ISP gave you, and stays reachable from the wider internet — without you wiring anything in by hand.

!!! note "Daemon only"
    Dynamic IPv6 runs only when `sandal daemon` is active. `sandal run` from the CLI by itself only gets the static fallback prefix at container creation; it does not track upstream changes.

## Quick start

For most users this is the whole setup:

```bash
sandal daemon
```

That's it. No environment variables. The daemon will:

- **Auto-detect your upstream interface** from the host's default route (typically `eth0`).
- **Auto-detect the right mode** by inspecting that interface (`ndp-proxy` for a typical home/SMB router with one `/64`, `pd` if it sees a delegated prefix).
- **Configure the kernel** sysctls needed for forwarding and proxying.
- **Mirror the upstream prefix** onto `sandal0` and renumber any running container.

Then start a container as usual:

```bash
sandal run -d --name web -lw nginx:latest
```

…and the container will have a public IPv6 like `2001:db8:3b0a:4f00::2` — reachable from any IPv6 device on your LAN.

## How to verify it's working

After the daemon comes up:

```bash
# Bridge has the public /64 from your upstream
ip -6 addr show dev sandal0
#  2001:db8:3b0a:4f00::1/64                        ← public, mirrored from eth0
#  fd34:135:123::1/120                               ← static ULA (kept as fallback)
#  172.16.0.1/24                                     ← static IPv4

# Daemon log shows what was picked
journalctl -u sandal -n 20 | grep -i renumber
#  renumber: auto-detected upstream interface  iface=eth0
#  renumber: auto-detected IPv6 mode           iface=eth0  mode=ndp-proxy
#  renumber: service started                   upstream=eth0 mode=ndp-proxy
#  renumber: applying                          prefix=2001:db8:3b0a:4f00::/64
```

After `sandal run`:

```bash
# Container's IPv6
sandal exec web -- ip -6 addr show dev eth0 scope global
#  2001:db8:3b0a:4f00::2/64                        ← public
#  fd34:135:123::2/120                               ← ULA (host-local)

# Host is announcing it to the LAN
ip -6 neigh show proxy dev eth0
#  2001:db8:3b0a:4f00::2 proxy

# Quick reachability test from the container
sandal exec web -- ping -6 -c 2 2606:4700:4700::1111
```

## Common scenarios

### Home router that hands out a `/64` via RA

Most consumer ISPs work this way. Nothing to configure — the auto-detect picks `ndp-proxy` and everything just works:

```bash
sandal daemon
sandal run -d --name web -lw nginx:latest
```

When your ISP rotates the prefix, containers renumber automatically within a few seconds. You'll see `renumber: applying prefix=…` in the daemon log.

### ISP that delegates a routed `/56` or `/60` (DHCPv6-PD)

If your router supports prefix delegation and you want sandal to claim its own `/64` from the delegated block:

```bash
# Optional — only needed if you want to ask for a specific delegation length
export SANDAL_IPV6_PD_HINT=60
sandal daemon
```

Auto-detect will pick `pd` mode if a delegated route is visible. If you want to be explicit:

```bash
export SANDAL_IPV6_MODE=pd
sandal daemon
```

### Pin a container to a fixed IPv6 (don't auto-renumber)

For a container whose address should not change — e.g. a stable internal service:

```bash
sandal run -d --name internal \
    -lw alpine:latest \
    -net "ip=fd00:internal::42/64;dynamic=false" \
    -- /run.sh
```

The `dynamic=false` token tells the renumber service to leave this container alone. Other containers on the same bridge keep renumbering normally.

### Disable dynamic IPv6 entirely

If you want `sandal0` to keep the static IPv6 from `SANDAL_HOST_NET` (default `fd34:0135:0123::1/120`) and never track an upstream:

```bash
export SANDAL_IPV6_MODE=off
sandal daemon
```

Bridge and containers stay on the ULA. No NDP-proxy entries, no renumbering.

### Override the auto-detected interface

If your default route doesn't go via the interface you want sandal to track (e.g. you have a VPN tun0 that captures the default but you want sandal to use eth0):

```bash
export SANDAL_UPSTREAM_IF=eth0
sandal daemon
```

### Run a container that already has its own DHCPv6 client

If the container does `ip=dhcp6` for its network config, sandal recognises that the container is managing its own IPv6 lease and skips renumbering for that link:

```bash
sandal run -d --name dhcp-managed \
    -lw alpine:latest \
    -net "ip=dhcp6" \
    -- /run.sh
```

The container keeps whatever address its DHCPv6 client got. The host-side renumber service won't touch it.

## What containers actually get

Bridge state determines what containers are allocated:

| Bridge state | New container's `eth0` |
|---|---|
| Daemon running, upstream has a public IPv6 | IPv4 + public IPv6 + ULA |
| Daemon running, upstream has no global yet | IPv4 + ULA only — public IPv6 added later when upstream's prefix arrives |
| Daemon not running (CLI-only `sandal run`) | IPv4 + static ULA from `SANDAL_HOST_NET` |
| `SANDAL_IPV6_MODE=off` | IPv4 + static ULA |

The static ULA (`fd34:135:123::/120` by default) is always kept as a fallback so containers can talk to each other and to the host even before — or instead of — a public IPv6 is available.

## Verifying a prefix change

Easiest way to confirm renumber works end-to-end is to inject a test prefix on eth0 and watch the chain react:

```bash
# Before
sandal exec web -- ip -6 addr show dev eth0 scope global
ip -6 neigh show proxy dev eth0

# Inject a test prefix; sandal sees it within ~2 s
sudo ip -6 addr add 2001:db8:test::1/64 dev eth0 valid_lft 7200 preferred_lft 7200

# Wait a moment, then check again
sleep 5
sandal exec web -- ip -6 addr show dev eth0 scope global    # new prefix
ip -6 neigh show proxy dev eth0                              # entry flipped

# Clean up
sudo ip -6 addr del 2001:db8:test::1/64 dev eth0
```

The container retains its IPv6 interface-identifier across the renumber, so `::2` stays `::2` even when the `/64` changes — handy for DNS records that reference the host part.

## In-container DHCPv6 renewal

When you start a container with `-net "ip=dhcp6"`, sandal keeps the DHCPv6 lease alive in the background:

- **Renew** at T1 (default 50% of preferred lifetime).
- **Rebind** at T2 (default 80%) if Renew fails.
- **Re-Solicit** with exponential backoff if the lease fully expires.
- **Release** on container shutdown so the server frees the binding.

No configuration is required — this is automatic for every container that uses DHCPv6.

## Troubleshooting

**Daemon log says `auto-detect failed; service disabled`.**

The host has no default IPv6 or IPv4 route at daemon start. Fix the host's networking first, then restart sandal. Or set `SANDAL_UPSTREAM_IF=<iface>` explicitly to skip the auto-detect.

**Auto-detect picked the wrong interface (e.g. VPN tun0).**

Set `SANDAL_UPSTREAM_IF=<correct-iface>` in your service file or systemd unit.

**Auto-detect picked `pd` but I want `ndp-proxy` (or vice versa).**

Set `SANDAL_IPV6_MODE=ndp-proxy` (or `pd`) explicitly.

**`sandal0` shows only the ULA, no public IPv6.**

The upstream interface doesn't currently have a public IPv6 address itself. Check:

```bash
ip -6 addr show dev eth0 scope global
```

If empty, your host has no public IPv6 to mirror — sandal stays dormant until something puts a global address on eth0 (SLAAC from a router RA, DHCPv6 NA from `dhclient -6`, manual `ip -6 addr add`, etc.). Once a global appears, the renumber fires automatically within ~2 s.

**Container ping6 to the internet times out.**

Check the host can reach the internet first:

```bash
ping -6 -c 2 2606:4700:4700::1111
```

If the host can't, this isn't a sandal problem — your upstream IPv6 transit is broken. If the host can but the container can't, check `ip6tables -L FORWARD` (should be `ACCEPT` or have explicit allow rules for sandal0 ⇄ upstream).

**Daemon log: `renumber: container failed err='Link not found'`.**

A container's interface name in its persisted config doesn't match what's inside its netns. Usually fixes itself on next renumber once any IPv6 address is on the container. For pre-existing containers, `sandal kill <name>; sandal run …` to recreate.

**My network manager keeps resetting `accept_ra` to `1`.**

Sandal sets `accept_ra=2` at daemon start because forwarding hosts need it to keep receiving RAs from upstream. If NetworkManager / systemd-networkd / netplan keeps changing it back, set `accept_ra=2` directly in their config (they own the interface).

**Existing connections drop when the prefix rotates.**

Expected behavior. Renumber is a hard cutover — the old global is dropped and the new one is added immediately. New connections work, but anything in-flight on the old address breaks. This matches how SLAAC renumbering behaves on regular hosts.

## Limitations

- **Single bridge only.** Multi-bridge or per-container alternative prefixes aren't supported.
- **One global prefix at a time.** If the upstream has multiple, sandal picks one (longest remaining valid lifetime) and stays sticky on it until it disappears.
- **No connection preservation across renumber.** Open connections drop by design.
- **IPv4 is unchanged.** The IPv4 portion of `SANDAL_HOST_NET` is still applied statically.
- **Auto-detect happens once at daemon start.** If your default route flaps to a different interface later, restart the daemon to re-detect, or pin `SANDAL_UPSTREAM_IF` explicitly.

## Configuration reference

All three are optional. Defaults are designed to work without setting anything.

| Variable | Default | Effect |
|---|---|---|
| `SANDAL_UPSTREAM_IF` | empty → auto-detect | Interface to watch for upstream prefix changes. Set to override auto-detect, or to skip it on hosts where the default-route interface isn't your WAN. |
| `SANDAL_IPV6_MODE` | empty → auto-detect | `ndp-proxy`, `pd`, `off`, or empty. Auto-detect picks between `ndp-proxy` and `pd` from the upstream's route state. Use `off` to disable entirely. |
| `SANDAL_IPV6_PD_HINT` | empty | Optional `pd`-only prefix-length hint (e.g. `60`). |

Set in your systemd unit:

```ini title="/etc/systemd/system/sandal.service.d/override.conf"
[Service]
Environment="SANDAL_UPSTREAM_IF=eth0"
Environment="SANDAL_IPV6_MODE=ndp-proxy"
```

## See also

- [`sandal daemon`](../commands/daemon.md) — daemon lifecycle.
- [`sandal run -net`](../commands/run.md#-net-value) — per-container network options including `dynamic=false` and `ip=dhcp6`.
- [Configuration](configuration.md) — full list of `SANDAL_*` environment variables.
