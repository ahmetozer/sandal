//go:build linux || darwin

package sandal

import (
	"strings"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/env"
)

func TestAttachVMWithoutConsole(t *testing.T) {
	old := env.RunDir
	env.RunDir = t.TempDir()
	t.Cleanup(func() { env.RunDir = old })

	c := &config.Config{Name: "fv", VM: "kvm"}
	err := Attach(c, strings.NewReader(""), nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no attachable console") {
		t.Errorf("Attach = %v, want no attachable console error", err)
	}
}
