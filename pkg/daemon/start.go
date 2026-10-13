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
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/modprobe"
	"github.com/ahmetozer/sandal/pkg/sandal"
)

type DaemonConfig struct {
	DiskReloadInterval  time.Duration
	HealthCheckInterval time.Duration
}

func (dc DaemonConfig) Start() error {

	var wg sync.WaitGroup

	go func() {
		if dc.DiskReloadInterval == 0 {
			dc.superviseDiskEvents()
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

	renumberCtx, renumberCancel := context.WithCancel(context.Background())
	defer renumberCancel()
	if err := startRenumber(renumberCtx); err != nil {
		slog.Error("renumber", "err", err)
		return err
	}

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

	// Stop launching new recoveries and wait for any in-flight recovery to
	// finish before tearing down. Otherwise a recovery that is mid-placement
	// when the daemon exits leaves a freshly started child untracked and
	// orphaned, and the next daemon start spawns a duplicate (audit L1).
	beginShutdown()

	// Release port-forward listeners before tearing down containers so
	// in-flight relays don't dial into netns that's about to disappear.
	host.Forwards.StopAll()

	DaemonPid := os.Getpid()
	conts, err := controller.Containers()
	if err != nil {
		slog.Error("unable to deprovision container during close process", "error", err.Error())
	} else {
		for _, cont := range conts {
			// VM children are Setsid session leaders detached from the daemon's
			// process group; host.DeRunContainer targets ContPid (the in-VM pid,
			// invisible from the host), so reap daemon-managed (startup) VMs by
			// their KVM HostPid directly (audit L7).
			if cont.VM != "" {
				if cont.Startup {
					pid, wantStart := cont.MonitorPidIdentity()
					if alive, _ := crt.IsPidRunningAs(pid, wantStart); alive {
						sandal.Kill(cont, 9, 10)
					}
				}
				continue
			}
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

// startRenumber launches the dynamic IPv6 service in the background. The
// upstream interface is SANDAL_UPSTREAM_IF, or auto-detected with retries
// so an uplink created by a --startup container after the daemon starts
// is still picked up. Only an invalid SANDAL_IPV6_MODE fails synchronously.
func startRenumber(ctx context.Context) error {
	mode := env.IPv6Mode
	switch mode {
	case "off":
		slog.Info("renumber: disabled", "mode", mode)
		return nil
	case "", "ndp-proxy", "pd":
	default:
		return fmt.Errorf("invalid SANDAL_IPV6_MODE %q; expected 'ndp-proxy', 'pd', or 'off'", mode)
	}
	go func() {
		upstream := env.UpstreamInterface
		if upstream == "" {
			var ok bool
			if upstream, ok = net.WaitForUpstream(ctx, 5*time.Second); !ok {
				return
			}
			slog.Info("renumber: auto-detected upstream interface", "iface", upstream)
		}
		if mode == "" {
			mode = net.DetectIPv6Mode(upstream)
			slog.Info("renumber: auto-detected IPv6 mode", "iface", upstream, "mode", mode)
		}
		renumber.SetActiveHostSysctls(renumber.HostSysctlsConfig{
			Upstream: upstream,
			Bridge:   net.DefaultBridgeInterface,
			Mode:     mode,
		})
		renumber.EnsureHostSysctls()

		applier := &renumber.DefaultApplier{BridgeName: net.DefaultBridgeInterface}
		var src renumber.Source
		if mode == "pd" {
			src = renumber.NewPDSource(upstream, env.IPv6PDHint)
		} else {
			src = renumber.NewRASource(upstream)
			applier.Proxy = renumber.NewNDPProxy(upstream, net.DefaultBridgeInterface)
			applier.HostRoute = true
			renumber.SetActiveNDPProxy(applier.Proxy)
		}
		slog.Info("renumber: service started", "upstream", upstream, "mode", mode)
		renumber.NewService(src, applier, 0).Run(ctx)
	}()
	return nil
}
