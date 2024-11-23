package net

import (
	"log/slog"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/vishvananda/netlink"
)

// Remove interface which is created for container but allocated under host
func Clear(c *config.Config) {
	for _, iface := range c.Ifaces {
		if iface.ALocFor == config.ALocForHostPod {
			link, err := netlink.LinkByName(iface.Name)
			slog.Debug("Clear", slog.String("action", "LinkByName"), slog.String("iface", iface.Name), slog.Any("err", err))
			if err == nil && link != nil {
				if err := netlink.LinkDel(link); err != nil {
					slog.Info("Clear", slog.String("action", "linkdel"), slog.String("iface", iface.Name), slog.Any("err", err))
				} else {
					slog.Debug("Clear", slog.String("action", "linkdel"), slog.String("iface", iface.Name), slog.Any("err", err))
				}
			}
		}
	}
}
