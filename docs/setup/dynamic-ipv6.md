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

…and the container will have a public IPv6 made of the upstream prefix and its ULA's interface identifier, like `2001:db8:aaaa:1:c0a8:10f:0:2` (see below) — reachable from any IPv6 device on your LAN.

## How to verify it's working

After the daemon comes up:

```bash
# Bridge has the public /64 from your upstream
ip -6 addr show dev sandal0
#  2001:db8:aaaa:1:c0a8:10f::1/128  ← public, mirrored from eth0 (/128 in ndp-proxy, /64 in pd)
#  fd34:135:123:0:c0a8:10f::1/120   ← static ULA (kept as fallback)
#  172.16.0.1/24                    ← static IPv4
# (The :c0a8:10f tail is the host's IPv4 (192.168.1.15 here) baked into the IID
#  by the default SANDAL_HOST_NET template — preserved across upstream renumber.)

# Daemon log shows what was picked
journalctl -u sandal -n 20 | grep -i renumber
#  renumber: auto-detected upstream interface  iface=eth0
#  renumber: auto-detected IPv6 mode           iface=eth0  mode=ndp-proxy
#  renumber: service started                   upstream=eth0 mode=ndp-proxy
#  renumber: applying                          prefix=2001:db8:aaaa:1::/64
```

After `sandal run`:

```bash
# Container's IPv6
sandal exec web -- ip -6 addr show dev eth0 scope global
#  2001:db8:aaaa:1:c0a8:10f:0:2/128  ← public (IID mirrors the container's ULA)
#  fd34:135:123:0:c0a8:10f::2/120    ← ULA (host-local)

# Host is announcing it to the LAN
ip -6 neigh show proxy dev eth0
#  2001:db8:aaaa:1:c0a8:10f::2 proxy

# RA prefix route on the uplink (an alternative source when the uplink has no SLAAC address)
ip -6 route show dev eth0
#  2001:db8:aaaa:1::/64 proto kernel metric 256 expires 300sec   ← upstream /64 (also present when the router offers no SLAAC)

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

### Give a container a public IPv6 with a fixed interface ID

Write the address as a dynamic address. `%uv6%` is replaced by the upstream
prefix (high 64 bits), the same token `SANDAL_HOST_NET` uses, and is
re-resolved whenever the prefix rotates:

```bash
sandal run -d --name web \
    -net "ip=192.0.2.23/24=%uv6%:192:0:2:23=fd00:db8::23/64" \
    -lw nginx:latest
```

With prefix `2001:db8:aaaa:1::/64` the container gets
`2001:db8:aaaa:1:192:0:2:23`. After rotation to `2001:db8:77:1::/64` it
becomes `2001:db8:77:1:192:0:2:23`, so firewall rules that match the
interface-ID suffix keep working. Omit the mask (or write `/128`); sandal
installs `/128` in ndp-proxy mode and the prefix length in pd mode.

The addresses above are placeholders. Explicit `ip=` addresses must be inside
`sandal0`'s subnets (from `SANDAL_HOST_NET`, `172.16.0.1/24` and
`fd34:135:123::/120` by default) so the bridge ULA gateway is on-link.

A literal global (e.g. `ip=2001:db8:9::7/64`) is static: sandal never
renumbers or removes it.

### Pin a container to a fixed IPv6 (don't auto-renumber)

For a container whose address should not change — e.g. a stable internal service:

```bash
sandal run -d --name internal \
    -lw alpine:latest \
    -net "ip=fd00:db8::42/64;dynamic=false" \
    -- /run.sh
```

The `dynamic=false` token tells the renumber service to leave this container alone. Other containers on the same bridge keep renumbering normally.

### Disable dynamic IPv6 entirely

If you want `sandal0` to keep only the static IPv6 from `SANDAL_HOST_NET` (the static ULA portion of the default, `fd34:0135:0123:0:%uv4%::1/120`) and never track an upstream:

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

Which public IPv6 a container gets depends on its `-net` option:

| `-net` | Public IPv6 |
|---|---|
| no `ip=` (sandal allocates) | yes, IID mirrors the container's ULA |
| `ip=` with a `%uv6%:…` dynamic address | yes, that address |
| `ip=` with a literal public IPv6 | static, never renumbered |
| `ip=` with IPv4/ULA only | no |
| `dynamic=false` or `ip=dhcp6` | never touched |

Without `sandal daemon`, or with `SANDAL_IPV6_MODE=off`, containers get only IPv4 and the ULA.

The IPv6 gateway is always the bridge ULA, so prefix rotation never breaks the
default route. Containers get their global at creation when the daemon already
knows the prefix, otherwise within ~3 s.

The static ULA (`fd34:135:123::/120` by default) is always kept as a fallback so containers can talk to each other and to the host even before — or instead of — a public IPv6 is available.

### VMs

`sandal run -vm` links follow the same rules, but the address is resolved once,
when the VM boots. Sandal proxies it while it is inside the current prefix;
after a prefix rotation restart the VM to pick up the new prefix.

This also applies when `-net ip=` lists explicit addresses: the VM gets the host-resolved public address and the sandal0 gateways.

### Upstreams without SLAAC

Some routers announce the prefix with autoconfiguration off and hand out
addresses only via DHCPv6. Sandal learns the prefix from the on-link route the
kernel installs from the router advertisement, so no address on the uplink is
needed. Sandal0's own public address is proxied too, so the host has global
IPv6 even when the uplink has none. Some routers with stateful DHCPv6 filter
addresses they did not lease; if containers can't reach the internet, check
with `tcpdump -i <uplink> icmp6` that the router's neighbour solicitations for
the container address are answered.

## Verifying a prefix change

Sandal stays on the current prefix for as long as it is still present on the uplink (as an address or a router-advertised on-link route). Adding a second prefix changes nothing; sandal switches only after the current one is removed or expires. Run this drill on a lab host — on a LAN with a live router, its next advertisement brings the current prefix back:

```bash
# Before
sandal exec web -- ip -6 addr show dev eth0 scope global
ip -6 neigh show proxy dev eth0

# Add a test prefix; sandal keeps the current one while it is present
sudo ip -6 addr add 2001:db8:aaaa:2::1/64 dev eth0 valid_lft 7200 preferred_lft 7200

# Remove the current prefix (its address and on-link route); sandal switches within ~3 s
sudo ip -6 addr del 2001:db8:aaaa:1::10/64 dev eth0    # eth0's current address, if any
sudo ip -6 route del 2001:db8:aaaa:1::/64 dev eth0

sleep 5
sandal exec web -- ip -6 addr show dev eth0 scope global    # now in 2001:db8:aaaa:2::/64
ip -6 neigh show proxy dev eth0                              # entry flipped

# Clean up: the original prefix returns with the router's next advertisement
sudo ip -6 addr del 2001:db8:aaaa:2::1/64 dev eth0
```

The container keeps its interface identifier across the renumber, so `…:c0a8:10f:0:2` keeps that suffix when the `/64` changes — handy for DNS records and firewall rules that reference the host part.

## In-container DHCPv6 renewal

When you start a container with `-net "ip=dhcp6"`, sandal keeps the DHCPv6 lease alive in the background:

- **Renew** at T1 (default 50% of preferred lifetime).
- **Rebind** at T2 (default 80%) if Renew fails.
- **Re-Solicit** with exponential backoff if the lease fully expires.
- **Release** on container shutdown so the server frees the binding.

No configuration is required — this is automatic for every container that uses DHCPv6.

## Upgrading

Containers created by older sandal versions keep their old addresses and IPv6 gateway: sandal does not migrate them. Recreate them (`sandal rm <name>`, then `sandal run …`) to get dynamic addresses, the ULA gateway and `%uv6%` support.

## Troubleshooting

**Daemon log says `no default route yet; retrying upstream auto-detect`.**

The uplink isn't up yet (common when a `--startup` container creates it). Sandal retries every 5 s and starts as soon as a default route appears. Set `SANDAL_UPSTREAM_IF=<iface>` to skip detection. If you set it for an uplink that appears late, also set `SANDAL_IPV6_MODE` explicitly: with `SANDAL_UPSTREAM_IF` set, the mode is auto-detected only once, when the daemon starts, possibly before the uplink exists.

**Auto-detect picked the wrong interface (e.g. VPN tun0).**

Set `SANDAL_UPSTREAM_IF=<correct-iface>` in your service file or systemd unit.

**Auto-detect picked `pd` but I want `ndp-proxy` (or vice versa).**

Set `SANDAL_IPV6_MODE=ndp-proxy` (or `pd`) explicitly.

**The host itself has no IPv6 but containers do.**

Check that sandal0's public address doesn't equal an address the router owns (often `<prefix>::1`). If you set `%uv6%` in SANDAL_HOST_NET yourself, pick an interface ID the router won't use.

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
- **The upstream interface is chosen once.** Detection retries until it succeeds, but the daemon does not switch interfaces afterwards; pin `SANDAL_UPSTREAM_IF` if the default route can move.

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
