//go:build linux || darwin

package sandal

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/ahmetozer/sandal/pkg/controller"
	squash "github.com/ahmetozer/sandal/pkg/lib/container/image"
	"github.com/ahmetozer/sandal/pkg/lib/squashfs"
	"github.com/ahmetozer/sandal/pkg/vm/mgmt"
)

// ExportArgs holds the parsed arguments for the export command.
type ExportArgs struct {
	ContainerName string
	FromDir       string
	ImageRef      string
	TarGz         bool
	OutputPath    string
	Includes      []string
	Excludes      []string
}

// Export dispatches export based on mode: image, directory, or container.
func Export(args ExportArgs) (string, error) {
	// Image export — works directly on all platforms
	if args.ImageRef != "" {
		return exportImage(args.ImageRef, args.OutputPath, args.TarGz)
	}

	// Directory export — works directly on all platforms
	if args.FromDir != "" {
		if _, err := os.Stat(args.FromDir); err != nil {
			return "", fmt.Errorf("source directory not found: %w", err)
		}
		return createSquashfs(args.FromDir, args.OutputPath, args.Includes, args.Excludes)
	}

	// Container export — dispatch based on VM or native
	c, err := controller.GetContainer(args.ContainerName)
	if err != nil {
		return "", fmt.Errorf("container %q not found: %w", args.ContainerName, err)
	}

	if c.VM != "" {
		return exportViaMgmt(args.ContainerName, args.OutputPath, args.Includes, args.Excludes)
	}

	return exportNative(c, args.OutputPath, args.Includes, args.Excludes)
}

func exportViaMgmt(contName, outputPath string, includes, excludes []string) (string, error) {
	client, err := mgmt.NewHTTPClient(contName)
	if err != nil {
		return "", fmt.Errorf("management socket for %q: %w", contName, err)
	}

	reqBody, _ := json.Marshal(map[string]any{
		"container":  contName,
		"outputPath": outputPath,
		"includes":   includes,
		"excludes":   excludes,
	})

	resp, err := client.Post("http://unix/export", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("export request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("export failed: %s", string(body))
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	return result["path"], nil
}

func exportImage(imageRef, outputPath string, tarGz bool) (string, error) {
	ctx := context.Background()

	outFile, err := os.Create(outputPath)
	if err != nil {
		return "", fmt.Errorf("creating output file: %w", err)
	}
	defer outFile.Close()

	if tarGz {
		gw := gzip.NewWriter(outFile)
		defer gw.Close()
		if err := squash.ExportImageTarGz(ctx, imageRef, gw); err != nil {
			os.Remove(outputPath)
			return "", err
		}
		return outputPath, nil
	}

	if err := squash.ExportImageSquashfs(ctx, imageRef, outFile); err != nil {
		os.Remove(outputPath)
		return "", err
	}
	return outputPath, nil
}

func createSquashfs(sourceDir, outputPath string, includes, excludes []string) (string, error) {
	outFile, err := os.Create(outputPath)
	if err != nil {
		return "", fmt.Errorf("creating output file: %w", err)
	}
	defer outFile.Close()

	var opts []squashfs.WriterOption
	if len(includes) > 0 || len(excludes) > 0 {
		inc := includes
		if len(inc) == 0 {
			inc = []string{"/"}
		}
		opts = append(opts, squashfs.WithPathFilter(
			squashfs.NewIncludeExcludeFilter(inc, excludes),
		))
	}

	w, err := squashfs.NewWriter(outFile, opts...)
	if err != nil {
		os.Remove(outputPath)
		return "", fmt.Errorf("creating squashfs writer: %w", err)
	}

	if err := w.CreateFromDir(sourceDir); err != nil {
		os.Remove(outputPath)
		return "", fmt.Errorf("creating squashfs image: %w", err)
	}

	return outputPath, nil
}
