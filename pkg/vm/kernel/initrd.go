package kernel

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type moduleFile struct {
	name string
	data []byte
	mode int64
}

// buildModulesInitrd creates a gzip-compressed CPIO archive containing
// the kernel modules tree (lib/modules/...).
func buildModulesInitrd(destPath string, modules []moduleFile) error {
	var cpio bytes.Buffer

	// Collect unique directories
	dirs := make(map[string]bool)
	for _, m := range modules {
		dir := filepath.Dir(m.name)
		for dir != "." && dir != "" {
			dirs[dir] = true
			dir = filepath.Dir(dir)
		}
	}

	// Write directory entries sorted so parents come before children
	// (the kernel's CPIO extractor uses non-recursive mkdir)
	sortedDirs := make([]string, 0, len(dirs))
	for dir := range dirs {
		sortedDirs = append(sortedDirs, dir)
	}
	sort.Strings(sortedDirs)

	inode := uint32(1)
	for _, dir := range sortedDirs {
		writeCPIODir(&cpio, dir, inode)
		inode++
	}

	// Write file entries
	for _, m := range modules {
		mode := uint32(m.mode) | 0100000 // Ensure S_IFREG file type bit is set
		if mode == 0100000 {
			mode = 0100644
		}
		writeCPIOFile(&cpio, m.name, m.data, mode, inode)
		inode++
	}

	// Trailer
	writeCPIOFile(&cpio, "TRAILER!!!", nil, 0, 0)

	// Gzip compress
	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	if _, err := gz.Write(cpio.Bytes()); err != nil {
		f.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

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

func cpiopad(buf *bytes.Buffer) {
	for buf.Len()%4 != 0 {
		buf.WriteByte(0)
	}
}
