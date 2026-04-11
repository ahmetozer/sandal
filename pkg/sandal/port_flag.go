//go:build linux || darwin

package sandal

import (
	"fmt"
	"strings"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/forward"
)

// portMappingFlag implements flag.Value so multiple -p options accumulate
// into c.Ports. Each call parses exactly one mapping.
type portMappingFlag struct {
	cfg *config.Config
}

func (p *portMappingFlag) String() string {
	if p == nil || p.cfg == nil {
		return ""
	}
	parts := make([]string, 0, len(p.cfg.Ports))
	for _, m := range p.cfg.Ports {
		parts = append(parts, m.Raw)
	}
	return strings.Join(parts, ",")
}

func (p *portMappingFlag) Set(value string) error {
	m, err := forward.ParseFlag(value)
	if err != nil {
		return fmt.Errorf("-p %s: %w", value, err)
	}
	if err := m.Validate(); err != nil {
		return fmt.Errorf("-p %s: %w", value, err)
	}
	m.ID = len(p.cfg.Ports)
	p.cfg.Ports = append(p.cfg.Ports, m)
	return nil
}
