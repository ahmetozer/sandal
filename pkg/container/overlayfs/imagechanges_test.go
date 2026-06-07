//go:build linux

package overlayfs

import (
	"sync"
	"testing"
)

// TestImageChangeMountsConcurrentAccess exercises the registry from multiple
// goroutines hitting overlapping keys. This mirrors the daemon's per-name
// recovery goroutines (which run fully concurrently — `recovering` only
// serializes per name) each driving the image-backed change-dir path:
// UmountRootfs -> UnregisterImageChangeMount (delete) racing
// mountRootfs -> RegisterImageChangeMount (write) on the shared global map.
//
// Without synchronization this fatals with "concurrent map writes" — Go's
// runtime detects concurrent map mutation even without the race detector
// (which is unavailable on some hosts) and the fatal is NOT recoverable, so
// it takes down the whole daemon. A single hot key under many goroutines
// triggers it reliably.
func TestImageChangeMountsConcurrentAccess(t *testing.T) {
	const goroutines = 16
	const iters = 20000
	const key = "/change/hot"

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				RegisterImageChangeMount(key, &ImageChangeMount{MountPoint: key})
				_ = GetImageChangeMount(key)
				UnregisterImageChangeMount(key)
			}
		}()
	}
	wg.Wait()
}
