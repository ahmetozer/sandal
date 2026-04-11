//go:build darwin

package vmbin

import (
	_ "embed"
	"bytes"
	"fmt"
)

//go:generate sh -c "rm -f linux-sandal && GOOS=linux GOARCH=$(go env GOARCH) CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o linux-sandal ../../.."

//go:embed linux-sandal
var linuxSandal []byte

// Linux returns the bytes of the Linux sandal binary embedded into this
// darwin build. The embedded binary targets the same GOARCH as the darwin
// host. It is produced by `go generate ./pkg/sandal/vmbin` prior to
// `go build` — the repo ships a text sentinel in its place so the tree
// always compiles, and this function rejects that sentinel at runtime.
func Linux() ([]byte, error) {
	if bytes.HasPrefix(linuxSandal, []byte("SANDAL_VMBIN_PLACEHOLDER")) {
		return nil, fmt.Errorf("embedded linux sandal binary is a placeholder; run `go generate ./pkg/sandal/vmbin` before building on darwin")
	}
	if len(linuxSandal) < 4 || !bytes.Equal(linuxSandal[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		return nil, fmt.Errorf("embedded linux sandal binary is not a valid ELF; re-run `go generate ./pkg/sandal/vmbin`")
	}
	return linuxSandal, nil
}
