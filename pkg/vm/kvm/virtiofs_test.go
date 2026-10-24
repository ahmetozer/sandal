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

// withRename2Flags sets the flags field of a FUSE_RENAME2 body.
func withRename2Flags(body []byte, flags uint32) []byte {
	binary.LittleEndian.PutUint32(body[8:12], flags)
	return body
}

// nodesAt returns the node IDs that map to path or to anything below it.
func nodesAt(d *VirtioFSDevice, path string) []uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	var ids []uint64
	for id, p := range d.nodes {
		if p == path || len(p) > len(path) && p[:len(path)+1] == path+"/" {
			ids = append(ids, id)
		}
	}
	return ids
}

// TestVirtioFSRenameOverExisting renames a file over an existing file and a
// directory over an existing empty directory. The target's old node IDs
// must go away: otherwise two IDs map to the same path and a lookup of the
// target name can hand the guest the replaced inode's ID.
func TestVirtioFSRenameOverExisting(t *testing.T) {
	for _, rename2 := range []bool{false, true} {
		root := t.TempDir()
		for name, data := range map[string]string{"src": "new", "dst": "old"} {
			if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for _, dir := range []string{"srcdir", "dstdir"} {
			if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(root, "srcdir", "f"), []byte("f"), 0o644); err != nil {
			t.Fatal(err)
		}

		d := NewVirtioFSDevice("test", root, false)
		srcNode := d.allocNode(filepath.Join(root, "src"))
		d.allocNode(filepath.Join(root, "dst"))
		srcDirNode := d.allocNode(filepath.Join(root, "srcdir"))
		srcChildNode := d.allocNode(filepath.Join(root, "srcdir", "f"))
		d.allocNode(filepath.Join(root, "dstdir"))
		d.allocNode(filepath.Join(root, "dstdir", "gone")) // stale entry below the target

		rename := d.handleRename
		if rename2 {
			rename = d.handleRename2
		}
		for _, mv := range [][2]string{{"src", "dst"}, {"srcdir", "dstdir"}} {
			out := make([]byte, 4096)
			rename(&fuseInHeader{NodeID: fuseRootID}, renameBody(rename2, fuseRootID, mv[0], mv[1]), out)
			if e := replyErrno(out); e != 0 {
				t.Fatalf("rename2=%v %s -> %s: errno %d", rename2, mv[0], mv[1], e)
			}
		}

		for path, want := range map[string][]uint64{
			filepath.Join(root, "dst"):    {srcNode},
			filepath.Join(root, "dstdir"): {srcDirNode, srcChildNode},
		} {
			got := nodesAt(d, path)
			if len(got) != len(want) {
				t.Errorf("rename2=%v: nodes at %s = %v, want %v", rename2, path, got, want)
				continue
			}
			for _, id := range want {
				if p, ok := d.nodePath(id); !ok || (p != path && p != filepath.Join(path, "f")) {
					t.Errorf("rename2=%v: node %d = %q, want under %s", rename2, id, p, path)
				}
			}
		}
		if id := d.allocNode(filepath.Join(root, "dst")); id != srcNode {
			t.Errorf("rename2=%v: lookup of dst = node %d, want the renamed node %d", rename2, id, srcNode)
		}
	}
}

// TestVirtioFSRename2Flags covers the RENAME2 flags: NOREPLACE must not
// replace, EXCHANGE swaps both the files and their node IDs, and anything
// else is rejected rather than treated as a plain rename.
func TestVirtioFSRename2Flags(t *testing.T) {
	const (
		renameNoReplace = 1 << 0
		renameExchange  = 1 << 1
		renameWhiteout  = 1 << 2
	)
	setup := func(t *testing.T) (string, *VirtioFSDevice, uint64, uint64) {
		root := t.TempDir()
		for name, data := range map[string]string{"a": "A", "b": "B"} {
			if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		d := NewVirtioFSDevice("test", root, false)
		return root, d, d.allocNode(filepath.Join(root, "a")), d.allocNode(filepath.Join(root, "b"))
	}
	rename := func(d *VirtioFSDevice, flags uint32) int32 {
		out := make([]byte, 4096)
		d.handleRename2(&fuseInHeader{NodeID: fuseRootID}, withRename2Flags(renameBody(true, fuseRootID, "a", "b"), flags), out)
		return replyErrno(out)
	}
	read := func(t *testing.T, path string) string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	t.Run("noreplace", func(t *testing.T) {
		root, d, a, b := setup(t)
		if e := rename(d, renameNoReplace); e != fuseErrEXIST {
			t.Errorf("errno %d, want EEXIST", e)
		}
		if got := read(t, filepath.Join(root, "b")); got != "B" {
			t.Errorf("b = %q after a refused rename, want B", got)
		}
		if p, _ := d.nodePath(a); p != filepath.Join(root, "a") {
			t.Errorf("node a moved to %q", p)
		}
		if p, _ := d.nodePath(b); p != filepath.Join(root, "b") {
			t.Errorf("node b moved to %q", p)
		}
	})
	t.Run("exchange", func(t *testing.T) {
		root, d, a, b := setup(t)
		if e := rename(d, renameExchange); e != 0 {
			t.Fatalf("errno %d", e)
		}
		if got := read(t, filepath.Join(root, "a")); got != "B" {
			t.Errorf("a = %q after exchange, want B", got)
		}
		if p, _ := d.nodePath(a); p != filepath.Join(root, "b") {
			t.Errorf("node of the old a = %q, want %s", p, filepath.Join(root, "b"))
		}
		if p, _ := d.nodePath(b); p != filepath.Join(root, "a") {
			t.Errorf("node of the old b = %q, want %s", p, filepath.Join(root, "a"))
		}
	})
	t.Run("whiteout", func(t *testing.T) {
		root, d, a, _ := setup(t)
		if e := rename(d, renameWhiteout); e != fuseErrINVAL {
			t.Errorf("errno %d, want EINVAL", e)
		}
		if got := read(t, filepath.Join(root, "a")); got != "A" {
			t.Errorf("a = %q after a rejected rename, want A", got)
		}
		if p, _ := d.nodePath(a); p != filepath.Join(root, "a") {
			t.Errorf("node a moved to %q", p)
		}
	})
}
