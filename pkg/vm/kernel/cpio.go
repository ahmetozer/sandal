package kernel

import (
	"bytes"
	"fmt"
)

// writeCPIODir writes a directory entry in newc CPIO format.
func writeCPIODir(buf *bytes.Buffer, name string, ino uint32) {
	nameWithNull := name + "\x00"
	fmt.Fprintf(buf, "070701")
	fmt.Fprintf(buf, "%08X", ino)                // ino
	fmt.Fprintf(buf, "%08X", 040755)             // mode (directory)
	fmt.Fprintf(buf, "%08X", 0)                  // uid
	fmt.Fprintf(buf, "%08X", 0)                  // gid
	fmt.Fprintf(buf, "%08X", 2)                  // nlink
	fmt.Fprintf(buf, "%08X", 0)                  // mtime
	fmt.Fprintf(buf, "%08X", 0)                  // filesize
	fmt.Fprintf(buf, "%08X", 0)                  // devmajor
	fmt.Fprintf(buf, "%08X", 0)                  // devminor
	fmt.Fprintf(buf, "%08X", 0)                  // rdevmajor
	fmt.Fprintf(buf, "%08X", 0)                  // rdevminor
	fmt.Fprintf(buf, "%08X", len(nameWithNull))  // namesize
	fmt.Fprintf(buf, "%08X", 0)                  // check
	buf.WriteString(nameWithNull)
	cpiopad(buf)
}

// writeCPIOFile writes a file entry in newc CPIO format.
func writeCPIOFile(buf *bytes.Buffer, name string, data []byte, mode uint32, ino uint32) {
	nameWithNull := name + "\x00"
	filesize := len(data)
	nlink := uint32(1)
	if data == nil {
		filesize = 0
		nlink = 1
	}
	fmt.Fprintf(buf, "070701")
	fmt.Fprintf(buf, "%08X", ino)                // ino
	fmt.Fprintf(buf, "%08X", mode)               // mode
	fmt.Fprintf(buf, "%08X", 0)                  // uid
	fmt.Fprintf(buf, "%08X", 0)                  // gid
	fmt.Fprintf(buf, "%08X", nlink)              // nlink
	fmt.Fprintf(buf, "%08X", 0)                  // mtime
	fmt.Fprintf(buf, "%08X", filesize)           // filesize
	fmt.Fprintf(buf, "%08X", 0)                  // devmajor
	fmt.Fprintf(buf, "%08X", 0)                  // devminor
	fmt.Fprintf(buf, "%08X", 0)                  // rdevmajor
	fmt.Fprintf(buf, "%08X", 0)                  // rdevminor
	fmt.Fprintf(buf, "%08X", len(nameWithNull))  // namesize
	fmt.Fprintf(buf, "%08X", 0)                  // check
	buf.WriteString(nameWithNull)
	cpiopad(buf)
	if filesize > 0 {
		buf.Write(data)
		cpiopad(buf)
	}
}

// cpiopad pads the buffer to a 4-byte boundary.
func cpiopad(buf *bytes.Buffer) {
	for buf.Len()%4 != 0 {
		buf.WriteByte(0)
	}
}

// writeCPIOCharDev writes a character device node in newc CPIO format.
func writeCPIOCharDev(buf *bytes.Buffer, name string, mode uint32, major, minor uint32, ino uint32) {
	nameWithNull := name + "\x00"
	fmt.Fprintf(buf, "070701")
	fmt.Fprintf(buf, "%08X", ino)               // ino
	fmt.Fprintf(buf, "%08X", mode)               // mode (char device)
	fmt.Fprintf(buf, "%08X", 0)                  // uid
	fmt.Fprintf(buf, "%08X", 0)                  // gid
	fmt.Fprintf(buf, "%08X", 1)                  // nlink
	fmt.Fprintf(buf, "%08X", 0)                  // mtime
	fmt.Fprintf(buf, "%08X", 0)                  // filesize
	fmt.Fprintf(buf, "%08X", 0)                  // devmajor
	fmt.Fprintf(buf, "%08X", 0)                  // devminor
	fmt.Fprintf(buf, "%08X", major)              // rdevmajor
	fmt.Fprintf(buf, "%08X", minor)              // rdevminor
	fmt.Fprintf(buf, "%08X", len(nameWithNull))  // namesize
	fmt.Fprintf(buf, "%08X", 0)                  // check
	buf.WriteString(nameWithNull)
	cpiopad(buf)
}

// newcCPIO builds a minimal newc-format CPIO archive containing a single file
// and a trailer.
func newcCPIO(name string, data []byte, mode uint32) []byte {
	var buf bytes.Buffer
	writeCPIOFile(&buf, name, data, mode, 1)
	writeCPIOFile(&buf, "TRAILER!!!", nil, 0, 0)
	return buf.Bytes()
}

// findInCPIO parses a newc-format CPIO archive and returns the data for the
// named file.
func findInCPIO(data []byte, target string) ([]byte, error) {
	offset := 0
	for offset < len(data) {
		if offset+110 > len(data) {
			break
		}

		magic := string(data[offset : offset+6])
		if magic != "070701" && magic != "070702" {
			return nil, fmt.Errorf("invalid cpio magic at offset %d: %q", offset, magic)
		}

		filesize := parseHex(data[offset+54 : offset+62])
		namesize := parseHex(data[offset+94 : offset+102])

		nameEnd := offset + 110 + int(namesize)
		if nameEnd > len(data) {
			break
		}
		name := string(data[offset+110 : nameEnd-1]) // exclude null terminator

		// Data starts after header+name, aligned to 4 bytes
		dataStart := align4(offset + 110 + int(namesize))
		dataEnd := dataStart + int(filesize)

		if name == "TRAILER!!!" {
			break
		}

		// Strip leading "./" or "/" for comparison
		cleanName := name
		for len(cleanName) > 0 && (cleanName[0] == '.' || cleanName[0] == '/') {
			cleanName = cleanName[1:]
		}
		if cleanName == target && filesize > 0 {
			if dataEnd > len(data) {
				return nil, fmt.Errorf("cpio data overflow for %s", target)
			}
			return data[dataStart:dataEnd], nil
		}

		// Next entry: align data end to 4 bytes
		offset = align4(dataEnd)
	}

	return nil, fmt.Errorf("file %q not found in cpio archive", target)
}

// stripCPIOTrailer removes the TRAILER!!! entry and any trailing padding
// from a raw CPIO archive, allowing more entries to be appended.
func stripCPIOTrailer(data []byte) []byte {
	trailerHeader := []byte("070701")
	trailerName := "TRAILER!!!"

	// Scan from the end looking for the TRAILER entry
	for offset := len(data) - 1; offset >= 110; offset-- {
		if !bytes.Equal(data[offset-5:offset+1], trailerHeader) {
			continue
		}
		pos := offset - 5
		nameSize := parseHex(data[pos+94 : pos+102])
		if nameSize < 11 {
			continue
		}
		nameStart := pos + 110
		nameEnd := nameStart + int(nameSize) - 1 // exclude null
		if nameEnd > len(data) {
			continue
		}
		if string(data[nameStart:nameEnd]) == trailerName {
			return data[:pos]
		}
	}
	return data
}

func parseHex(b []byte) uint64 {
	var val uint64
	for _, c := range b {
		val <<= 4
		switch {
		case c >= '0' && c <= '9':
			val |= uint64(c - '0')
		case c >= 'a' && c <= 'f':
			val |= uint64(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			val |= uint64(c - 'A' + 10)
		}
	}
	return val
}

func align4(n int) int {
	return (n + 3) &^ 3
}
