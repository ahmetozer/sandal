#!/usr/bin/env bash
# Dynamic IPv6 (NDP-proxy mode) integration test.
#
# Setup:
#   ns_router : sends RAs with prefix P1, then swaps to P2
#   ns_sandal : runs sandald with SANDAL_UPSTREAM_IF=veth_s
#
# Acceptance:
#   container has an address in P1 and an NDP proxy entry on veth_s.
#   After the RA swap, both flip to P2 within 5 seconds.
#
# Usage: SANDAL_INTEGRATION=1 sudo bash test/integration/dynamic_ipv6_ra.sh

set -euo pipefail

[[ "${SANDAL_INTEGRATION:-0}" == "1" ]] || { echo "skip: set SANDAL_INTEGRATION=1"; exit 0; }
[[ $EUID -eq 0 ]] || { echo "fatal: must run as root"; exit 1; }

cleanup() {
  ip netns del ns_router 2>/dev/null || true
  ip netns del ns_sandal 2>/dev/null || true
  pkill -f 'ra-emitter' 2>/dev/null || true
}
trap cleanup EXIT

ip netns add ns_router
ip netns add ns_sandal
ip link add veth_r type veth peer name veth_s
ip link set veth_r netns ns_router
ip link set veth_s netns ns_sandal

ip -n ns_router addr add 2001:db8:1::1/64 dev veth_r
ip -n ns_router link set veth_r up
ip -n ns_sandal link set veth_s up

# Build helper and emitter once.
make build
(cd test/integration/ra-emitter && go build -o /tmp/ra-emitter .)

ip netns exec ns_router /tmp/ra-emitter -dev veth_r -prefix 2001:db8:1::/64 -valid 60 &
RA_PID=$!
sleep 2

ip netns exec ns_sandal env SANDAL_UPSTREAM_IF=veth_s SANDAL_IPV6_MODE=ndp-proxy ./sandal daemon &
DAEMON_PID=$!
sleep 3

ip netns exec ns_sandal ./sandal run -d --name testc -net 'type=veth' -- /bin/sleep 600

sleep 5

CONT_IPS=$(ip netns exec ns_sandal ./sandal exec testc -- ip -6 addr show dev eth0 | grep -oE '2001:db8:1:[0-9a-f:]+' || true)
[[ -n "$CONT_IPS" ]] || { echo "FAIL: container has no IP in P1"; exit 1; }
echo "OK: container in P1: $CONT_IPS"

ip netns exec ns_sandal ip -6 neigh show proxy dev veth_s | grep -q "$CONT_IPS" \
  || { echo "FAIL: no NDP proxy entry for $CONT_IPS"; exit 1; }
echo "OK: NDP proxy entry present"

kill $RA_PID
ip netns exec ns_router /tmp/ra-emitter -dev veth_r -prefix 2001:db8:2::/64 -valid 60 &
RA_PID=$!
sleep 6

CONT_IPS2=$(ip netns exec ns_sandal ./sandal exec testc -- ip -6 addr show dev eth0 | grep -oE '2001:db8:2:[0-9a-f:]+' || true)
[[ -n "$CONT_IPS2" ]] || { echo "FAIL: container did not renumber to P2"; exit 1; }
echo "OK: container in P2: $CONT_IPS2"

ip netns exec ns_sandal ip -6 neigh show proxy dev veth_s | grep -q "$CONT_IPS2" \
  || { echo "FAIL: NDP proxy entry not updated"; exit 1; }
echo "OK: NDP proxy entry updated"

kill $DAEMON_PID 2>/dev/null || true
echo PASS
