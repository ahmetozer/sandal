package cruntime

import "encoding/binary"

const (
	bits = 32 << (^uint(0) >> 63)
)

var (
	procSize = 65535
)

func init() {
	// arange bits based on cpu architecture
	if bits == 64 {
		bin := binary.BigEndian.AppendUint64([]byte{255, 255, 255, 255}, 0)
		procSize = int(binary.BigEndian.Uint32(bin))
	}

}
