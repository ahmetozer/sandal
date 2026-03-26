package alpine

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"regexp"
	"runtime"
	"sort"
	"strings"

	sandaltar "github.com/ahmetozer/sandal/pkg/lib/tar"
)

const (
	baseURL  = "https://dl-cdn.alpinelinux.org/alpine"
	tagsAtom = "https://gitlab.alpinelinux.org/alpine/aports/-/tags?format=atom"
)

// atomFeed represents the Atom XML feed structure from GitLab tags.
type atomFeed struct {
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title string `xml:"title"`
}

// DiscoverLatestMinirootfs finds the latest Alpine minirootfs tarball URL
// for the current architecture by querying the Alpine aports GitLab tags.
func DiscoverLatestMinirootfs() (version string, tarballURL string, err error) {
	arch := runtime.GOARCH
	switch arch {
	case "arm64":
		arch = "aarch64"
	case "amd64":
		arch = "x86_64"
	}

	ver, err := latestReleaseVersion()
	if err != nil {
		return "", "", err
	}

	// ver is e.g. "3.23.3", minor is "3.23"
	parts := strings.SplitN(ver, ".", 3)
	if len(parts) < 3 {
		return "", "", fmt.Errorf("unexpected version format: %s", ver)
	}
	minor := parts[0] + "." + parts[1]
	tarball := fmt.Sprintf("alpine-minirootfs-%s-%s.tar.gz", ver, arch)
	url := fmt.Sprintf("%s/v%s/releases/%s/%s", baseURL, minor, arch, tarball)

	return ver, url, nil
}

// latestReleaseVersion fetches the Alpine aports GitLab tags atom feed
// and returns the latest stable release version (e.g. "3.23.3").
func latestReleaseVersion() (string, error) {
	resp, err := http.Get(tagsAtom)
	if err != nil {
		return "", fmt.Errorf("fetching tags feed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("tags feed returned status %d", resp.StatusCode)
	}

	var feed atomFeed
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return "", fmt.Errorf("parsing tags feed: %w", err)
	}

	// Match stable release tags: v3.X.Y (no RC, no date-based)
	re := regexp.MustCompile(`^v(\d+\.\d+\.\d+)$`)
	var versions []string
	for _, e := range feed.Entries {
		if m := re.FindStringSubmatch(e.Title); m != nil {
			versions = append(versions, m[1])
		}
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("no stable release tags found in feed")
	}

	sort.Strings(versions)
	return versions[len(versions)-1], nil
}

// DownloadRootfs downloads and extracts an Alpine minirootfs tarball into destDir.
func DownloadRootfs(tarballURL, destDir string) error {
	resp, err := http.Get(tarballURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	return sandaltar.ExtractTarGz(resp.Body, destDir)
}
