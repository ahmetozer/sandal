package alpine

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// Entry represents a file entry extracted from an APK archive.
type Entry struct {
	Name     string
	Typeflag byte
	Mode     int64
	Data     []byte
}

// LatestVersion fetches APKINDEX from baseURL and returns the version string
// for the given package name. The caller is responsible for caching the result.
func LatestVersion(baseURL, pkgName string) (string, error) {
	resp, err := http.Get(baseURL + "/APKINDEX.tar.gz")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d fetching APKINDEX", resp.StatusCode)
	}

	gr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return "", err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if hdr.Name != "APKINDEX" {
			continue
		}
		return parseAPKIndex(tr, pkgName)
	}
	return "", fmt.Errorf("APKINDEX entry not found in archive")
}

// Download fetches an APK from the given URL and calls fn for each tar entry
// found in the archive. Alpine APKs consist of multiple concatenated gzip streams.
func Download(url string, fn func(Entry) error) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}

	var reader io.Reader = resp.Body
	if resp.ContentLength > 0 {
		reader = &progressReader{r: resp.Body, total: resp.ContentLength}
	}

	// Wrap in bufio.Reader so gzip won't over-read past stream boundaries.
	// This allows streaming multiple concatenated gzip streams without
	// buffering the entire response in memory.
	// Wrap in bufio.Reader (implements io.ByteReader) so gzip uses it
	// directly without creating its own internal bufio that could over-read
	// past gzip stream boundaries.
	br := bufio.NewReader(reader)
	for {
		gr, err := gzip.NewReader(br)
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("gzip reader: %w", err)
		}
		// Disable multistream so gzip stops at the end of each stream
		// instead of transparently starting the next one.
		gr.Multistream(false)

		tr := tar.NewReader(gr)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}

			data, err := io.ReadAll(tr)
			if err != nil {
				return fmt.Errorf("reading entry %s: %w", hdr.Name, err)
			}

			if err := fn(Entry{
				Name:     hdr.Name,
				Typeflag: hdr.Typeflag,
				Mode:     hdr.Mode,
				Data:     data,
			}); err != nil {
				gr.Close()
				return err
			}
		}
		// Drain any remaining decompressed data (e.g. tar padding) so
		// the underlying bufio.Reader is positioned at the start of
		// the next gzip stream.
		io.Copy(io.Discard, gr)
		gr.Close()
	}

	if resp.ContentLength > 0 {
		fmt.Fprintf(os.Stderr, "\n")
	}

	return nil
}

// parseAPKIndex scans the APKINDEX for the given package and returns its version.
func parseAPKIndex(r io.Reader, pkgName string) (string, error) {
	scanner := bufio.NewScanner(r)
	var currentPkg string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			currentPkg = ""
			continue
		}
		if strings.HasPrefix(line, "P:") {
			currentPkg = line[2:]
		}
		if strings.HasPrefix(line, "V:") && currentPkg == pkgName {
			return line[2:], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("package %s not found in APKINDEX", pkgName)
}

// progressReader wraps an io.Reader and prints download progress to stderr.
type progressReader struct {
	r       io.Reader
	total   int64
	current int64
	lastPct int
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.current += int64(n)
	pct := int(pr.current * 100 / pr.total)
	if pct != pr.lastPct {
		pr.lastPct = pct
		fmt.Fprintf(os.Stderr, "\r  %d%% (%d / %d MB)", pct, pr.current/(1024*1024), pr.total/(1024*1024))
	}
	return n, err
}

