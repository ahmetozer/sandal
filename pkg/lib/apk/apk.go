package apk

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

	body, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if resp.ContentLength > 0 {
		fmt.Fprintf(os.Stderr, "\n")
	}

	br := &byteReader{data: body}
	for {
		gr, err := gzip.NewReader(br)
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("gzip reader: %w", err)
		}

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
		gr.Close()
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

// byteReader implements io.Reader over a byte slice, allowing gzip to
// consume only what it needs and leave the rest for the next gzip stream.
type byteReader struct {
	data []byte
	pos  int
}

func (b *byteReader) Read(p []byte) (int, error) {
	if b.pos >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.pos:])
	b.pos += n
	return n, nil
}
