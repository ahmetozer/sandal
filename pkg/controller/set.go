package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

func SetContainer(c *config.Config) error {
CONTROLLER:
	slog.Debug("SetContainer", slog.Any("currentConrollerType", currentConrollerType))

	if err := config.ValidateName(c.Name); err != nil {
		return err
	}

	switch currentConrollerType {
	// If controller not initialized
	case 0:
		Containers()
		goto CONTROLLER
	case ControllerTypeDisk:
		return setContainerByDisk(c)
	case ControllerTypeMemory:
		return setContainerByMemory(c)
	case ControllerTypeServer:
		return setContainerByServer(c)
	default:
		return fmt.Errorf("unknown controller type")
	}
}

func setContainerByServer(c *config.Config) error {
	slog.Debug("setContainerByServer", slog.Any("container", c.Name))

	jsonValue, _ := json.Marshal(c)
	_, err := httpc.Post("http://unix/containers/"+c.Name, "text/json", bytes.NewBuffer(jsonValue))
	return err
}

func setContainerByMemory(c *config.Config) error {
	slog.Debug("setContainerByMemory", slog.Any("container", c.Name))

	containerListMu.Lock()
	defer containerListMu.Unlock()

	idx := -1
	gen := c.Generation
	for i := range containerList {
		if containerList[i].Name == c.Name {
			idx = i
			gen = max(gen, containerList[i].Generation)
			break
		}
	}
	// Every set is a new generation, above the caller's copy (a CLI POST
	// may be built from an older read), the in-memory entry and the file
	// (an external writer the watcher has not applied yet). The caller's
	// pointer carries it too, so a caller that re-sets its own Config keeps
	// moving forward. Written under containerListMu, so concurrent sets of
	// the same pointer do not race on it.
	diskBytes, _ := os.ReadFile(c.ConfigFileLoc())
	c.Generation = max(gen, fileGeneration(diskBytes)) + 1

	// Deep-copy on the way in. The clone becomes the controller's sole
	// reference to this Config; the caller keeps their original `c`.
	// Combined with clone-on-read in containersFromMemory/GetContainer,
	// no external goroutine ever holds a pointer to the live in-memory
	// entry, so JSON encoders elsewhere can iterate map fields without
	// racing concurrent mutators.
	clone := c.Clone()
	cJSON := clone.Json()

	// Only write to disk when content actually differs from what's
	// already there. A missing file (ReadFile error) reads as nil bytes and
	// reliably triggers the write. The generation bump makes every set
	// differ; the disk-events handler no longer writes back (LoadFromDisk),
	// so there is no feedback loop to break.
	if !bytes.Equal(diskBytes, cJSON) {
		if err := writeContainerFile(clone.ConfigFileLoc(), cJSON); err != nil {
			slog.Warn("setContainerByMemory", slog.String("cont", c.Name), slog.Any("error", err))
		}
	}

	if idx >= 0 {
		containerList[idx] = clone
		return nil
	}
	containerList = append(containerList, clone)
	return nil
}

// LoadFromDisk applies a state file the daemon read from disk to the
// in-memory list. It never writes: the file is already what it read, and a
// write-back of a stale read is how a lost update reached disk. An existing
// entry is replaced only by a newer generation, so a read that lost the race
// with a later SetContainer is ignored. Files from before Generation (0 on
// both sides) fall back to the old rule: replace when the content differs.
// It reports whether memory changed.
func LoadFromDisk(c *config.Config) bool {
	if c == nil || config.ValidateName(c.Name) != nil {
		return false
	}
	containerListMu.Lock()
	defer containerListMu.Unlock()

	for i := range containerList {
		mem := containerList[i]
		if mem.Name != c.Name {
			continue
		}
		switch {
		case c.Generation > mem.Generation:
		case c.Generation == 0 && mem.Generation == 0 && !bytes.Equal(c.Json(), mem.Json()):
		default:
			slog.Debug("LoadFromDisk", slog.String("cont", c.Name), slog.String("action", "ignore"),
				slog.Uint64("disk", c.Generation), slog.Uint64("memory", mem.Generation))
			return false
		}
		containerList[i] = c.Clone()
		return true
	}

	// No entry. The container may have been deleted after the caller read
	// the file; adding it back would resurrect it. Delete removes the file,
	// so only add while the file is still there.
	if _, err := os.Stat(c.ConfigFileLoc()); err != nil {
		slog.Debug("LoadFromDisk", slog.String("cont", c.Name), slog.String("action", "skip"), slog.Any("error", err))
		return false
	}
	containerList = append(containerList, c.Clone())
	return true
}

// setContainerByDisk is the controller without a daemon. It bumps the
// generation above the file's, so a daemon that later sees the write through
// inotify takes it as newer.
func setContainerByDisk(c *config.Config) error {
	slog.Debug("setContainerByDisk", slog.Any("container", c.Name))

	if c.Name == "" {
		return fmt.Errorf("no name set for request")
	}

	// Serialises read, bump and write within this process.
	containerListMu.Lock()
	defer containerListMu.Unlock()
	diskBytes, _ := os.ReadFile(c.ConfigFileLoc())
	c.Generation = max(c.Generation, fileGeneration(diskBytes)) + 1
	return writeContainerFile(c.ConfigFileLoc(), c.Json())
}

// fileGeneration returns the generation recorded in a state file's bytes,
// 0 for a missing, unreadable or pre-Generation file.
func fileGeneration(data []byte) uint64 {
	var g struct{ Generation uint64 }
	if json.Unmarshal(data, &g) != nil {
		return 0
	}
	return g.Generation
}

// writeContainerFile writes a state file, creating the state dir if needed.
func writeContainerFile(path string, data []byte) error {
	savePath := filepath.Dir(path)
	slog.Debug("ConfigFileLoc", slog.String("action", "saving config file"), slog.String("file", path))
	retry := false
WriteFile:
	err := writeStateFile(path, data)
	if err != nil {
		if os.IsNotExist(err) && !retry {
			err := os.MkdirAll(savePath, 0o0755)
			slog.Debug("ConfigFileLoc", slog.String("action", "mkdir conf path"), slog.String("path", savePath), slog.Any("error", err))
			retry = true
			goto WriteFile
		}
		return fmt.Errorf("writing config file: %v", err)
	}
	return nil
}

// writeStateFile replaces path atomically: readers see either the old or the
// new content, never a truncated file. The temp file lives in the same
// directory so the rename cannot cross filesystems.
func writeStateFile(path string, data []byte) error {
	dir, base := filepath.Dir(path), filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
