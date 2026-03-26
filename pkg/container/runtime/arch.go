//go:build linux

package runtime

import "encoding/binary"

const (
	bits = 32 << (^uint(0) >> 63)
)

var (
	ProcSize = 65535
)

func init() {
	// arange bits based on cpu architecture
	if bits == 64 {
		bin := binary.BigEndian.AppendUint64([]byte{255, 255, 255, 255}, 0)
		ProcSize = int(binary.BigEndian.Uint32(bin))
	}

}
