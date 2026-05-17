//go:build linux

package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/host"
	"github.com/ahmetozer/sandal/pkg/container/net"
	"github.com/ahmetozer/sandal/pkg/container/net/renumber"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/modprobe"
)

type DaemonConfig struct {
	DiskReloadInterval  time.Duration
	HealthCheckInterval time.Duration
}

func (dc DaemonConfig) Start() error {

	var wg sync.WaitGroup

	go func() {
		if dc.DiskReloadInterval == 0 {
			dc.loadByEvent()
		}
	}()

	slog.Info("daemon", "service", "started")

	os.MkdirAll(env.LibDir, 0o0660)
	os.MkdirAll(env.RunDir, 0o0660)

	os.MkdirAll(env.BaseImageDir, 0o0660)
	os.MkdirAll(env.BaseStateDir, 0o0660)

	os.MkdirAll(env.BaseChangeDir, 0o0660)
	os.MkdirAll(env.BaseImmutableImageDir, 0o0660)
	os.MkdirAll(env.BaseRootfsDir, 0o0660)

	net.CreateDefaultBridge()

	// Dynamic IPv6 service. Spawned only when an upstream interface is
	// known — either configured via SANDAL_UPSTREAM_IF or auto-detected
	// from the host's default route.
	if env.UpstreamInterface == "" {
		if iface, err := net.DetectUpstream(); err != nil {
			slog.Warn("renumber: SANDAL_UPSTREAM_IF empty and auto-detect failed; service disabled", "err", err)
		} else {
			slog.Info("renumber: auto-detected upstream interface", "iface", iface)
			env.UpstreamInterface = iface
		}
	}
	renumberCtx, renumberCancel := context.WithCancel(context.Background())
	// Auto-detect IPv6 mode when the operator left it unset. The detector
	// reads the upstream interface's address+route state; on ambiguous
	// hosts it falls back to "ndp-proxy". Set SANDAL_IPV6_MODE explicitly
	// to override.
	if env.UpstreamInterface != "" && env.IPv6Mode == "" {
		env.IPv6Mode = net.DetectIPv6Mode(env.UpstreamInterface)
		slog.Info("renumber: auto-detected IPv6 mode", "iface", env.UpstreamInterface, "mode", env.IPv6Mode)
	}
	if env.UpstreamInterface != "" && env.IPv6Mode != "off" {
		// Validate mode explicitly to surface typos like "ndp_proxy".
		switch env.IPv6Mode {
		case "ndp-proxy", "pd":
			// ok
		default:
			slog.Error("renumber: invalid SANDAL_IPV6_MODE; expected 'ndp-proxy', 'pd', or 'off'",
				"got", env.IPv6Mode)
			renumberCancel()
			return fmt.Errorf("invalid SANDAL_IPV6_MODE %q", env.IPv6Mode)
		}
		// Configure host sysctls up-front so the renumber service and any
		// containers that come up afterwards see correct forwarding /
		// accept_ra / proxy_ndp state from the start.
		renumber.ApplyHostSysctls(renumber.HostSysctlsConfig{
			Upstream: env.UpstreamInterface,
			Bridge:   net.DefaultBridgeInterface,
			Mode:     env.IPv6Mode,
		})
		var src renumber.Source
		switch env.IPv6Mode {
		case "pd":
			src = renumber.NewPDSource(env.UpstreamInterface, env.IPv6PDHint)
		default: // "ndp-proxy"
			src = renumber.NewRASource(env.UpstreamInterface)
		}
		applier := &renumber.DefaultApplier{BridgeName: net.DefaultBridgeInterface}
		if env.IPv6Mode != "pd" {
			proxy, err := renumber.NewNDPProxy(env.UpstreamInterface, net.DefaultBridgeInterface)
			if err != nil {
				slog.Error("renumber: NDP proxy init failed; service disabled", "err", err)
			} else {
				applier.Proxy = proxy
				renumber.SetActiveNDPProxy(proxy)
			}
		}
		svc := renumber.NewService(src, applier, 0)
		go svc.Run(renumberCtx)
		slog.Info("renumber: service started", "upstream", env.UpstreamInterface, "mode", env.IPv6Mode)
	}
	defer renumberCancel()

	for _, mod := range []string{"vhost_net", "vhost_vsock"} {
		if err := modprobe.Load(mod); err != nil {
			slog.Warn("modprobe", slog.String("module", mod), slog.Any("err", err))
		}
	}

	// Rehydrate port-forward listeners for already-running containers
	// before starting the supervisor loops. After a daemon crash the
	// container PIDs are still alive but their listeners died with the
	// previous daemon process; this loop reattaches without touching
	// the containers themselves. Health-check-driven recovery (which
	// goes through crun and registers via Forwards.Add) will replace
	// any sessions installed here for containers it later restarts.
	host.RehydrateAllForwards()

	wg.Add(2)
	daemonKillRequested := make(chan bool)
	go signalProxy(daemonKillRequested, &wg)
	go daemonControlHealthCheck(daemonKillRequested, &wg)
	wg.Wait()

	// Release port-forward listeners before tearing down containers so
	// in-flight relays don't dial into netns that's about to disappear.
	host.Forwards.StopAll()

	DaemonPid := os.Getpid()
	conts, err := controller.Containers()
	if err != nil {
		slog.Error("unable to deprovision container during close process", "error", err.Error())
	} else {
		for _, cont := range conts {
			// Do not kill containers, which is executed before daemon
			if cont.HostPid == DaemonPid {
				host.DeRunContainer(cont)
			}
		}
	}

	checkZombie()
	slog.Info("daemon", slog.String("service", "stopped"))
	return nil
}
