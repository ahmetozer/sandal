# Networking

Sandal uses a shared bridge network model where both containers and VMs connect to the same L2 bridge (`sandal0`). Containers use veth pairs; VMs use TAP devices.

## Network Topology

```
+------------------+     +------------------+     +------------------+
| Container A      |     | Container B      |     | KVM VM           |
| (veth: eth0)     |     | (veth: eth0)     |     | (virtio-net)     |
+--------+---------+     +--------+---------+     +--------+---------+
         |                         |                        |
   [veth peer]              [veth peer]              [TAP device]
         |                         |                        |
+--------+-------------------------+------------------------+---------+
|                          sandal0 bridge                              |
+---------------------------------------------------------------------+
         |
   [host uplink or NAT]
```

## Bridge Setup

**File**: `pkg/container/net/bridge.go`

### Default Bridge Creation

```go
func CreateDefaultBridge() error {
    bridge := &netlink.Bridge{
        LinkAttrs: netlink.LinkAttrs{Name: "sandal0"},
    }
    netlink.LinkAdd(bridge)
    netlink.LinkSetUp(bridge)

    // Disable STP for immediate port activation
    // Set forward delay to 0
}
```

### Bridge Modes

**Bare metal Linux**:
- Bridge gets a static IP from `SANDAL_HOST_NET` (default: `172.16.0.1/24,fd34:0135:0123::1/64`)
- Containers get IPs from the same subnet
- Host acts as gateway, uses iptables NAT for internet access

**Inside KVM VM** (when sandal itself runs in a VM):
- No bridge is created
- Existing ethN interfaces are adopted directly in passthru mode
- Containers use the VM's network interface without L2 bridging
- Containers get IPs via DHCP from the external network

```go
func CreateDefaultBridge() {
    if env.IsVM() {
        // Passthru mode: no bridge, use ethN directly
        return nil, nil
    } else {
        // Standard mode: assign IP to bridge
        addr := parseAddr(os.Getenv("SANDAL_HOST_NET"))
        netlink.AddrAdd(bridge, addr)
    }
}
```

## Container Networking

**File**: `pkg/container/net/`

### Veth Pair Creation

**File**: `net/veth.go`

```go
func createVethPair(hostName, contName string, bridge *netlink.Bridge) {
    veth := &netlink.Veth{
        LinkAttrs: netlink.LinkAttrs{Name: hostName, MasterIndex: bridge.Index},
        PeerName:  contName,
    }
    netlink.LinkAdd(veth)
    netlink.LinkSetUp(hostVeth)
}
```

Naming convention:
- Host side: `s-<random_10char_id>`
- Container side: `eth0` (moved into container's network namespace)

### Network Interface Configuration

**File**: `net/link.go`

Each container network interface spec supports:

```
-net "ip=172.16.0.2/24;route=172.16.0.1;type=veth;master=sandal0"
-net "ip=dhcp;type=veth;master=sandal0"
```

Note: sharing the host network namespace is done via `-ns-net host`, not through the network interface flag.

Configuration flow:
```
parseNetworkFlags(cfg.Net)
  For each interface:
    1. Create veth pair
    2. Move peer into container network namespace
    3. Set MAC address (if specified)
    4. Set MTU
    5. Assign IP:
       - Static: netlink.AddrAdd()
       - DHCP:   dhcp.Request() -> obtain lease -> apply
    6. Add routes: netlink.RouteAdd()
    7. Bring interface up: netlink.LinkSetUp()
```

### IP Address Allocation

**File**: `net/ip-allocation.go`

When using static IPs, sandal tracks allocated addresses to prevent conflicts:

```go
func IPRequest(bridge *netlink.Bridge) (net.IPNet, error) {
    bridgeAddrs := netlink.AddrList(bridge)
    baseIP := bridgeAddrs[0].IP

    // Scan existing containers for used IPs
    usedIPs := scanUsedIPs()

    // Increment from bridge IP until finding unused one
    candidate := incrementIP(baseIP)
    for usedIPs[candidate.String()] {
        candidate = incrementIP(candidate)
    }
    return net.IPNet{IP: candidate, Mask: bridgeAddrs[0].Mask}, nil
}
```

### DHCP Client

**Package**: `pkg/lib/dhcp/`

Built-in DHCP client for both IPv4 and IPv6:

**DHCPv4** (`client.go`):
```
DISCOVER -> OFFER -> REQUEST -> ACK
  |
  +-- Bind raw socket to interface
  +-- Send DHCPDISCOVER broadcast
  +-- Receive DHCPOFFER with offered IP
  +-- Send DHCPREQUEST for offered IP
  +-- Receive DHCPACK
  +-- Apply: ip addr add <IP> dev <iface>
  +-- Apply: ip route add default via <gateway>
```

**DHCPv6** (`client6.go`):
```
SOLICIT -> ADVERTISE -> REQUEST -> REPLY
```

**Platform abstraction**: `conn_linux.go` uses raw sockets; `conn_darwin.go` uses BPF.

### Route Configuration

**File**: `net/route.go`

```go
func addDefaultRoute(gateway net.IP, iface *netlink.Link) {
    route := &netlink.Route{
        LinkIndex: iface.Attrs().Index,
        Gw:        gateway,
        Dst:       nil,  // default route
    }
    netlink.RouteAdd(route)
}
```

## VM Networking

### TAP Device

**File**: `pkg/vm/kvm/tap.go`

```go
func createTAP(name string) (*tapDevice, error) {
    fd, _ := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)

    ifr := ifreq{
        Flags: IFF_TAP | IFF_NO_PI | IFF_VNET_HDR,
    }
    copy(ifr.Name[:], name)  // e.g., "sandal1234"

    ioctl(fd, TUNSETIFF, uintptr(unsafe.Pointer(&ifr)))
    ioctl(fd, TUNSETOFFLOAD, TUN_F_CSUM|TUN_F_TSO4|TUN_F_TSO6)
    ioctl(fd, TUNSETVNETHDRSZ, 12)  // virtio_net_hdr size

    return &tapDevice{fd: fd, name: name}, nil
}
```

**Flags explained**:
- `IFF_TAP`: Ethernet-level (L2) device (vs IFF_TUN for IP-level)
- `IFF_NO_PI`: No packet info header (raw ethernet frames)
- `IFF_VNET_HDR`: Prepend/expect 12-byte virtio_net_hdr on each frame

### TAP-Bridge Attachment

```go
func (t *tapDevice) attachToBridge() {
    bridge := sandalnet.CreateDefaultBridge()
    tapLink, _ := netlink.LinkByName(t.name)
    netlink.LinkSetMaster(tapLink, bridge)
    netlink.LinkSetUp(tapLink)
}
```

### VM-side Network Configuration

**File**: `pkg/vm/guest/init.go`

Network config is passed via kernel command line as base64-encoded JSON:

```
SANDAL_VM_NET=<base64({"addr":"172.16.0.5/24","gateway":"172.16.0.1","mac":"52:54:00:xx:xx:01","mtu":1500})>
```

Guest init applies this:
```go
func vmConfigureNetwork(cfg vmNetConfig) {
    eth0, _ := netlink.LinkByName("eth0")
    netlink.LinkSetHardwareAddr(eth0, cfg.MAC)
    netlink.AddrAdd(eth0, cfg.Addr)
    netlink.LinkSetUp(eth0)
    netlink.RouteAdd(&netlink.Route{Gw: cfg.Gateway})
}
```

### MAC Address Generation

```go
// virtio_net.go
mac := [6]byte{0x52, 0x54, 0x00, byte(pid>>8), byte(pid), 0x01}
```

- Prefix `52:54:00` is the QEMU/KVM OUI range
- Middle bytes derived from VM PID for uniqueness
- Last byte `0x01` distinguishes from container MACs

## Network Flow Summary

### Container on Bare Metal

```
Container eth0 (172.16.0.2/24)
  -> veth peer on host
  -> sandal0 bridge (172.16.0.1/24)
  -> iptables MASQUERADE
  -> host eth0 -> internet
```

### VM Container

```
Container eth0 inside VM
  -> veth peer inside VM
  -> sandal0 bridge inside VM
  -> VM eth0 (virtio-net)
  -> [virtqueue]
  -> Host TAP device
  -> Host sandal0 bridge
  -> Host network
```

### VM Passthru Mode

When sandal runs inside an existing VM (detected by `env.IsVM()`):

```
Container eth0 inside VM
  -> ethN passthru (no bridge, no veth)
  -> VM's virtio-net interface
  -> Host TAP device
  -> Host sandal0 bridge
  -> DHCP from host network
```
