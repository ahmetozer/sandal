//go:build linux || darwin

package namelock

import (
	"testing"
	"time"

	"github.com/ahmetozer/sandal/pkg/env"
)

func TestTryAcquireMutualExclusion(t *testing.T) {
	env.RunDir = t.TempDir()

	rel, ok, err := TryAcquire("web")
	if err != nil || !ok {
		t.Fatalf("first TryAcquire = (%v,%v), want (true,nil)", ok, err)
	}

	if rel2, ok2, err := TryAcquire("web"); err != nil || ok2 {
		if rel2 != nil {
			rel2()
		}
		t.Fatalf("second TryAcquire while held = (%v,%v), want (false,nil)", ok2, err)
	}

	// A different name is independent.
	relOther, okOther, err := TryAcquire("db")
	if err != nil || !okOther {
		t.Fatalf("TryAcquire(db) = (%v,%v), want (true,nil)", okOther, err)
	}
	relOther()

	// After release, the name is acquirable again.
	rel()
	rel3, ok3, err := TryAcquire("web")
	if err != nil || !ok3 {
		t.Fatalf("re-acquire after release = (%v,%v), want (true,nil)", ok3, err)
	}
	rel3()
}

func TestAcquireBlocksUntilTimeout(t *testing.T) {
	env.RunDir = t.TempDir()

	rel, ok, err := TryAcquire("x")
	if err != nil || !ok {
		t.Fatalf("setup TryAcquire failed: ok=%v err=%v", ok, err)
	}
	defer rel()

	start := time.Now()
	if rel2, err := Acquire("x", 150*time.Millisecond); err == nil {
		rel2()
		t.Fatal("Acquire should time out while the name is held")
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("Acquire returned after %s, expected to wait ~150ms", elapsed)
	}
}
