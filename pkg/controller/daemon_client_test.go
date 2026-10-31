//go:build linux || darwin

package controller

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/env"
)

// useStateDir points the state files at an empty temp dir.
func useStateDir(t *testing.T) {
	t.Helper()
	prev := env.BaseStateDir
	env.BaseStateDir = t.TempDir()
	t.Cleanup(func() { env.BaseStateDir = prev })
}

// useDaemonSocket points the client at a socket path in a short temp dir
// (sun_path is ~104 bytes on darwin, too short for t.TempDir()) and returns it.
func useDaemonSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sdl")
	if err != nil {
		t.Fatal(err)
	}
	prev := env.DaemonSocket
	env.DaemonSocket = dir + "/d.sock"
	t.Cleanup(func() {
		env.DaemonSocket = prev
		os.RemoveAll(dir)
	})
	return env.DaemonSocket
}

// fakeDaemon serves h on the daemon socket and counts the connections that
// are still open.
func fakeDaemon(t *testing.T, h http.HandlerFunc) *atomic.Int64 {
	t.Helper()
	ln, err := net.Listen("unix", useDaemonSocket(t))
	if err != nil {
		t.Fatal(err)
	}
	var open atomic.Int64
	srv := &http.Server{
		Handler: h,
		ConnState: func(_ net.Conn, s http.ConnState) {
			switch s {
			case http.StateNew:
				open.Add(1)
			case http.StateClosed, http.StateHijacked:
				open.Add(-1)
			}
		},
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return &open
}

func stateFileExists(name string) bool {
	_, err := os.Stat(config.ConfigFileLoc(name))
	return err == nil
}

func TestSetContainerByServerUsesDaemon(t *testing.T) {
	useStateDir(t)
	var got atomic.Value
	fakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Method + " " + r.URL.Path)
	})

	if err := setContainerByServer(&config.Config{Name: "up"}); err != nil {
		t.Fatal(err)
	}
	if got.Load() != "POST /containers/up" {
		t.Fatalf("daemon got %v, want POST /containers/up", got.Load())
	}
	if stateFileExists("up") {
		t.Fatal("wrote the state file although the daemon took it")
	}
}

func TestSetContainerByServerFallsBackWhenDaemonGone(t *testing.T) {
	useStateDir(t)
	useDaemonSocket(t) // nothing listens on it

	if err := setContainerByServer(&config.Config{Name: "gone"}); err != nil {
		t.Fatalf("save failed with the daemon gone: %v", err)
	}
	c, err := LoadFile(config.ConfigFileLoc("gone"))
	if err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	if c.Name != "gone" || c.Generation == 0 {
		t.Fatalf("state file = name %q generation %d", c.Name, c.Generation)
	}
}

func TestSetContainerByServerFallsBackWhenDaemonStuck(t *testing.T) {
	useStateDir(t)
	release := make(chan struct{})
	fakeDaemon(t, func(w http.ResponseWriter, r *http.Request) { <-release })
	t.Cleanup(func() { close(release) }) // before srv.Close, which waits for handlers
	prev := httpc.Timeout
	httpc.Timeout = 200 * time.Millisecond
	t.Cleanup(func() { httpc.Timeout = prev })

	done := make(chan error, 1)
	go func() { done <- setContainerByServer(&config.Config{Name: "stuck"}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("save failed with the daemon stuck: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("save hung on a stuck daemon")
	}
	if !stateFileExists("stuck") {
		t.Fatal("state file not written")
	}
}

func TestSetContainerByServerReportsRefusal(t *testing.T) {
	useStateDir(t)
	fakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	if err := setContainerByServer(&config.Config{Name: "refused"}); err == nil {
		t.Fatal("no error for a daemon that refused the state")
	}
	if stateFileExists("refused") {
		t.Fatal("wrote the state file behind the daemon's back")
	}
}

func TestDaemonClientReleasesConnections(t *testing.T) {
	useStateDir(t)
	open := fakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers" {
			w.Write([]byte("[]"))
			return
		}
		w.Write([]byte(`{"Status":200}`))
	})

	for i := 0; i < 20; i++ {
		if _, err := containersFromServer(); err != nil {
			t.Fatal(err)
		}
		if err := setContainerByServer(&config.Config{Name: "conn"}); err != nil {
			t.Fatal(err)
		}
		if err := pingServerSocket(); err != nil {
			t.Fatal(err)
		}
	}
	// Closed responses go back to the one shared pool; leaked ones each pin
	// a connection.
	deadline := time.Now().Add(2 * time.Second)
	for open.Load() > 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := open.Load(); n > 2 {
		t.Fatalf("%d daemon connections still open after 60 calls", n)
	}
}

// useDiskController makes SetContainer write state files, as the daemon's
// handler would without the daemon's memory list.
func useDiskController(t *testing.T) {
	t.Helper()
	useStateDir(t)
	prevType, prevContainers := currentConrollerType, Containers
	currentConrollerType, Containers = ControllerTypeDisk, containersFromDir
	t.Cleanup(func() { currentConrollerType, Containers = prevType, prevContainers })
}

func post(t *testing.T, h http.Handler, c config.Config) int {
	t.Helper()
	body, _ := json.Marshal(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/containers/"+c.Name, bytes.NewReader(body)))
	return rec.Code
}

func TestServerUnknownPathIsNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	serverMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

func TestServerPostReportsSaveFailure(t *testing.T) {
	useDiskController(t)
	mux := serverMux()

	if code := post(t, mux, config.Config{Name: "ok"}); code != http.StatusOK {
		t.Fatalf("valid state: status %d, want 200", code)
	}
	if code := post(t, mux, config.Config{Name: "-bad"}); code != http.StatusInternalServerError {
		t.Fatalf("invalid name: status %d, want 500", code)
	}
}

func TestServerPostConcurrent(t *testing.T) {
	useDiskController(t)
	mux := serverMux()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name, want := "ok", http.StatusOK
			if i%2 == 1 {
				name, want = "-bad", http.StatusInternalServerError
			}
			if code := post(t, mux, config.Config{Name: name}); code != want {
				t.Errorf("%s: status %d, want %d", name, code, want)
			}
		}(i)
	}
	wg.Wait()
}
