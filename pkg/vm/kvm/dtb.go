//go:build linux && arm64

package kvm

import (
	"encoding/binary"
)

// FDT (Flattened Device Tree) builder following the Devicetree Specification.
// Builds a minimal DTB in-memory using the standard binary format.

const (
	fdtMagic    = 0xd00dfeed
	fdtVersion  = 17
	fdtLastComp = 16

	fdtBeginNode = 0x00000001
	fdtEndNode   = 0x00000002
	fdtProp      = 0x00000003
	fdtEnd       = 0x00000009
)

type fdtBuilder struct {
	structs  []byte
	strings  []byte
	strIndex map[string]uint32
}

func newFDTBuilder() *fdtBuilder {
	return &fdtBuilder{
		strIndex: make(map[string]uint32),
	}
}

func (f *fdtBuilder) addString(s string) uint32 {
	if off, ok := f.strIndex[s]; ok {
		return off
	}
	off := uint32(len(f.strings))
	f.strIndex[s] = off
	f.strings = append(f.strings, []byte(s)...)
	f.strings = append(f.strings, 0)
	return off
}

func (f *fdtBuilder) appendU32(val uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], val)
	f.structs = append(f.structs, buf[:]...)
}

func (f *fdtBuilder) padTo4(data []byte) []byte {
	if pad := len(data) % 4; pad != 0 {
		data = append(data, make([]byte, 4-pad)...)
	}
	return data
}

func (f *fdtBuilder) beginNode(name string) {
	f.appendU32(fdtBeginNode)
	nameBytes := append([]byte(name), 0)
	nameBytes = f.padTo4(nameBytes)
	f.structs = append(f.structs, nameBytes...)
}

func (f *fdtBuilder) endNode() {
	f.appendU32(fdtEndNode)
}

func (f *fdtBuilder) propRaw(name string, data []byte) {
	f.appendU32(fdtProp)
	f.appendU32(uint32(len(data)))
	f.appendU32(f.addString(name))
	padded := make([]byte, len(data))
	copy(padded, data)
	padded = f.padTo4(padded)
	f.structs = append(f.structs, padded...)
}

func (f *fdtBuilder) propU32(name string, val uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], val)
	f.propRaw(name, buf[:])
}

func (f *fdtBuilder) propU64(name string, val uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], val)
	f.propRaw(name, buf[:])
}

func (f *fdtBuilder) propString(name, val string) {
	data := append([]byte(val), 0)
	f.propRaw(name, data)
}

func (f *fdtBuilder) propEmpty(name string) {
	f.propRaw(name, nil)
}

func (f *fdtBuilder) propU32Array(name string, vals []uint32) {
	data := make([]byte, len(vals)*4)
	for i, v := range vals {
		binary.BigEndian.PutUint32(data[i*4:], v)
	}
	f.propRaw(name, data)
}

// propRegU64 writes a reg property with one <addr, size> pair using 64-bit values.
func (f *fdtBuilder) propRegU64(addr, size uint64) {
	var buf [16]byte
	binary.BigEndian.PutUint64(buf[0:], addr)
	binary.BigEndian.PutUint64(buf[8:], size)
	f.propRaw("reg", buf[:])
}

// propRegPair writes a reg property with two <addr, size> pairs using 64-bit values.
func (f *fdtBuilder) propRegPair(addr1, size1, addr2, size2 uint64) {
	var buf [32]byte
	binary.BigEndian.PutUint64(buf[0:], addr1)
	binary.BigEndian.PutUint64(buf[8:], size1)
	binary.BigEndian.PutUint64(buf[16:], addr2)
	binary.BigEndian.PutUint64(buf[24:], size2)
	f.propRaw("reg", buf[:])
}

func (f *fdtBuilder) finish() []byte {
	// Add FDT_END token to structs
	f.appendU32(fdtEnd)

	// Build header
	headerSize := uint32(40) // FDT header is 40 bytes
	structsOffset := headerSize
	// Align structs block
	structsSize := uint32(len(f.structs))
	stringsOffset := structsOffset + structsSize
	stringsSize := uint32(len(f.strings))
	totalSize := stringsOffset + stringsSize

	header := make([]byte, headerSize)
	binary.BigEndian.PutUint32(header[0:], fdtMagic)
	binary.BigEndian.PutUint32(header[4:], totalSize)
	binary.BigEndian.PutUint32(header[8:], structsOffset)   // off_dt_struct
	binary.BigEndian.PutUint32(header[12:], stringsOffset)  // off_dt_strings
	binary.BigEndian.PutUint32(header[16:], headerSize)     // off_mem_rsvmap (points to empty reservation block)
	binary.BigEndian.PutUint32(header[20:], fdtVersion)     // version
	binary.BigEndian.PutUint32(header[24:], fdtLastComp)    // last_comp_version
	binary.BigEndian.PutUint32(header[28:], 0)              // boot_cpuid_phys
	binary.BigEndian.PutUint32(header[32:], stringsSize)    // size_dt_strings
	binary.BigEndian.PutUint32(header[36:], structsSize)    // size_dt_struct

	// The memory reservation block must be present (even if empty).
	// It consists of pairs of (address, size) terminated by (0, 0).
	// Since we put off_mem_rsvmap at headerSize, we need to insert it
	// between header and structs.
	memRsvMap := make([]byte, 16) // one terminating entry: 16 bytes of zeros

	// Recalculate offsets with memRsvMap
	memRsvMapOffset := headerSize
	structsOffset = memRsvMapOffset + uint32(len(memRsvMap))
	stringsOffset = structsOffset + structsSize
	totalSize = stringsOffset + stringsSize

	binary.BigEndian.PutUint32(header[4:], totalSize)
	binary.BigEndian.PutUint32(header[8:], structsOffset)
	binary.BigEndian.PutUint32(header[12:], stringsOffset)
	binary.BigEndian.PutUint32(header[16:], memRsvMapOffset)

	result := make([]byte, 0, totalSize)
	result = append(result, header...)
	result = append(result, memRsvMap...)
	result = append(result, f.structs...)
	result = append(result, f.strings...)

	return result
}
