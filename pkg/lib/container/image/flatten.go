package squash

import (
	"archive/tar"
	"io"
	"path"
	"sort"
	"strings"
)

// fileEntry holds a single filesystem entry from the layered image.
type fileEntry struct {
	header *tar.Header
	body   []byte
	order  int // preserves insertion order for stable output
}

// Flatten reads ordered OCI layers (each an uncompressed tar stream) and
// produces a single tar stream representing the final merged filesystem.
// It handles OCI/Docker whiteout files:
//   - .wh.<name>  — deletes the named file from previous layers
//   - .wh..wh..opq — marks directory as opaque (deletes all prior contents)
func Flatten(layers []io.Reader, w io.Writer) error {
	files := make(map[string]*fileEntry)
	order := 0

	for _, layer := range layers {
		tr := tar.NewReader(layer)

		// Track entries added by THIS layer (for opaque whiteout handling).
		currentLayerPaths := make(map[string]bool)

		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}

			name := cleanPath(hdr.Name)
			dir := path.Dir(name)
			base := path.Base(name)

			// Handle opaque whiteout: delete all prior entries under this directory.
			if base == ".wh..wh..opq" {
				prefix := dir + "/"
				for p := range files {
					if p == dir || strings.HasPrefix(p, prefix) {
						if !currentLayerPaths[p] {
							delete(files, p)
						}
					}
				}
				continue
			}

			// Handle file whiteout: delete a specific file from prior layers.
			if strings.HasPrefix(base, ".wh.") {
				target := path.Join(dir, base[len(".wh."):])
				delete(files, target)
				// Also delete children if it was a directory.
				prefix := target + "/"
				for p := range files {
					if strings.HasPrefix(p, prefix) {
						delete(files, p)
					}
				}
				continue
			}

			// Skip entries that clean to an empty path (root directory).
			if name == "" {
				continue
			}

			// Read the body for regular files.
			var body []byte
			if hdr.Typeflag == tar.TypeReg && hdr.Size > 0 {
				body, err = io.ReadAll(tr)
				if err != nil {
					return err
				}
			}

			files[name] = &fileEntry{
				header: hdr,
				body:   body,
				order:  order,
			}
			currentLayerPaths[name] = true
			order++
		}
	}

	// Write all entries sorted by path for deterministic output.
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	tw := tar.NewWriter(w)
	for _, p := range paths {
		entry := files[p]
		hdr := *entry.header
		hdr.Name = p

		if err := tw.WriteHeader(&hdr); err != nil {
			return err
		}
		if len(entry.body) > 0 {
			if _, err := tw.Write(entry.body); err != nil {
				return err
			}
		}
	}

	return tw.Close()
}

// cleanPath normalizes a tar path: removes leading ./ and /, trims trailing /.
func cleanPath(p string) string {
	p = path.Clean(p)
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	if p == "." {
		p = ""
	}
	return p
}
