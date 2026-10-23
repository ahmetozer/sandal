//go:build linux && (amd64 || arm64)

package kvm

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unsafe"
)

// renameBody encodes a FUSE_RENAME (rename2=false) or FUSE_RENAME2 request
// body that moves oldName to newName within the same directory node.
func renameBody(rename2 bool, dir uint64, oldName, newName string) []byte {
	var body []byte
	if rename2 {
		body = make([]byte, unsafe.Sizeof(fuseRename2In{}))
	} else {
		body = make([]byte, 8)
	}
	binary.LittleEndian.PutUint64(body, dir)
	body = append(body, oldName...)
	body = append(body, 0)
	body = append(body, newName...)
	return append(body, 0)
}

func replyErrno(outBuf []byte) int32 {
	return (*fuseOutHeader)(unsafe.Pointer(&outBuf[0])).Error
}

// TestVirtioFSRenameKeepsNodes checks that a node the guest looked up
// before a rename still resolves after it. The guest moves its dentry and
// keeps using the same node ID, so the server must follow the rename;
// otherwise the renamed file (or anything below a renamed directory) is
// ENOENT until the guest's entry timeout expires and it looks it up again.
func TestVirtioFSRenameKeepsNodes(t *testing.T) {
	for _, rename2 := range []bool{false, true} {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".out.tmp"), []byte("img"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "dir"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "dir", "f"), []byte("f"), 0o644); err != nil {
			t.Fatal(err)
		}

		d := NewVirtioFSDevice("test", root, false)
		fileNode := d.allocNode(filepath.Join(root, ".out.tmp"))
		dirNode := d.allocNode(filepath.Join(root, "dir"))
		childNode := d.allocNode(filepath.Join(root, "dir", "f"))

		rename := d.handleRename
		if rename2 {
			rename = d.handleRename2
		}
		for _, mv := range [][2]string{{".out.tmp", "out.sqfs"}, {"dir", "dir2"}} {
			out := make([]byte, 4096)
			rename(&fuseInHeader{NodeID: fuseRootID}, renameBody(rename2, fuseRootID, mv[0], mv[1]), out)
			if e := replyErrno(out); e != 0 {
				t.Fatalf("rename2=%v %s -> %s: errno %d", rename2, mv[0], mv[1], e)
			}
		}

		for node, want := range map[uint64]string{
			fileNode:  filepath.Join(root, "out.sqfs"),
			dirNode:   filepath.Join(root, "dir2"),
			childNode: filepath.Join(root, "dir2", "f"),
		} {
			if got, _ := d.nodePath(node); got != want {
				t.Errorf("rename2=%v: node %d path = %q, want %q", rename2, node, got, want)
			}
			out := make([]byte, 4096)
			d.handleGetattr(&fuseInHeader{NodeID: node}, out)
			if e := replyErrno(out); e != 0 {
				t.Errorf("rename2=%v: getattr on node %d (%s) after rename: errno %d", rename2, node, want, e)
			}
		}
	}
}
