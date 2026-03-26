//go:build !(linux && arm64)

package kernel

// preinitBinary is nil on non-Linux-ARM64 platforms.
// macOS VZ and other hypervisors handle /dev setup before running init.
var preinitBinary []byte
